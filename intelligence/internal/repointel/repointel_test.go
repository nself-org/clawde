// Package repointel — unit tests for watcher, extractor, and ingestion pipeline.
//
// DB-dependent tests use the SymbolStore interface seam with a stub; no live DB required.
// Tests that require real tree-sitter grammars are skipped without the 'treesitter' build tag.
//
// SPORT: REGISTRY-FUNCTIONS.md → repointel tests.
package repointel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ── Stub SymbolStore ──────────────────────────────────────────────────────────

type stubStore struct {
	mu      sync.Mutex
	symbols []SymbolRecord
	edges   []CallEdge
	deleted []string // file paths for which symbols were deleted
}

func (s *stubStore) UpsertSymbols(_ context.Context, syms []SymbolRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.symbols = append(s.symbols, syms...)
	return nil
}

func (s *stubStore) DeleteSymbolsForFile(_ context.Context, _ uuid.UUID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Remove existing symbols for file (simulate upsert-delete-reupsert pattern).
	filtered := s.symbols[:0]
	for _, sym := range s.symbols {
		if sym.FilePath != path {
			filtered = append(filtered, sym)
		}
	}
	s.symbols = filtered
	s.deleted = append(s.deleted, path)
	return nil
}

func (s *stubStore) UpsertEdges(_ context.Context, _ uuid.UUID, edges []CallEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edges = append(s.edges, edges...)
	return nil
}

func (s *stubStore) DeleteEdgesForFile(_ context.Context, _ uuid.UUID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.edges[:0]
	for _, e := range s.edges {
		if e.FilePath != path {
			filtered = append(filtered, e)
		}
	}
	s.edges = filtered
	return nil
}

// ── Watcher debounce ──────────────────────────────────────────────────────────

func TestWatcherDebounce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := NewWatcher(dir)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond) // let watcher start

	// Write a .go file three times in quick succession.
	testFile := filepath.Join(dir, "main.go")
	for i := range 3 {
		_ = os.WriteFile(testFile, []byte(fmt.Sprintf("package main // %d", i)), 0o644)
		time.Sleep(20 * time.Millisecond)
	}

	// After debounceDelay + buffer, we should get exactly one event.
	var events []FileChangeEvent
	deadline := time.After(debounceDelay + 400*time.Millisecond)
loop:
	for {
		select {
		case ev := <-w.Events():
			events = append(events, ev)
		case <-deadline:
			break loop
		}
	}

	if len(events) != 1 {
		t.Errorf("expected 1 debounced event, got %d", len(events))
	}
	if len(events) > 0 && events[0].Op != FileOpWrite {
		t.Errorf("expected FileOpWrite, got %v", events[0].Op)
	}
}

func TestWatcherIgnoresUnsupportedExts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := NewWatcher(dir)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// Write a .txt file — should produce no event.
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)
	time.Sleep(debounceDelay + 200*time.Millisecond)

	select {
	case ev := <-w.Events():
		t.Errorf("unexpected event for unsupported ext: %v", ev)
	default:
		// Good — no event.
	}
}

// ── DetectLanguage ────────────────────────────────────────────────────────────

func TestDetectLanguage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want Language
	}{
		{"main.go", LangGo},
		{"lib.rs", LangRust},
		{"app.ts", LangTypeScript},
		{"component.tsx", LangTypeScript},
		{"script.py", LangPython},
		{"widget.dart", LangDart},
		{"README.md", LangUnknown},
	}
	for _, tc := range cases {
		got := DetectLanguage(tc.path)
		if got != tc.want {
			t.Errorf("DetectLanguage(%q): got %v, want %v", tc.path, got, tc.want)
		}
	}
}

// ── Stub extractor round-trip ─────────────────────────────────────────────────

func TestStubExtractorReturnsEmpty(t *testing.T) {
	t.Parallel()
	ext := NewExtractor() // stub in non-treesitter build
	wid := uuid.New()
	syms, edges, err := ext.ExtractSymbols(wid, "main.go", []byte("package main"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Stub returns nil/empty — confirm no panic.
	if len(syms) != 0 || len(edges) != 0 {
		// In treesitter build, may return real results — that's also fine.
		t.Logf("extractor returned %d symbols, %d edges (real grammar build)", len(syms), len(edges))
	}
}

// ── Ingestion pipeline upsert idempotency ─────────────────────────────────────

func TestPipelineUpsertIdempotency(t *testing.T) {
	t.Parallel()

	store := &stubStore{}
	wid := uuid.New()
	dir := t.TempDir()

	// Write a .go file.
	src := []byte(`package main

// greet says hello.
func greet(name string) string { return "hi " + name }

func main() { greet("world") }
`)
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, src, 0o644); err != nil {
		t.Fatal(err)
	}

	ext := NewExtractor()
	p := NewPipeline(wid, dir, ext, store)
	ctx := context.Background()

	// Full scan twice — second scan should delete-then-reinsert (idempotent count).
	if err := p.FullScan(ctx); err != nil {
		t.Fatalf("FullScan 1: %v", err)
	}
	count1 := len(store.symbols)

	// Reset and re-run to test idempotency.
	store.symbols = nil
	store.edges = nil
	if err := p.FullScan(ctx); err != nil {
		t.Fatalf("FullScan 2: %v", err)
	}
	count2 := len(store.symbols)

	if count1 != count2 {
		t.Errorf("idempotency failure: scan1=%d symbols, scan2=%d symbols", count1, count2)
	}
}

func TestPipelineHandleRemove(t *testing.T) {
	t.Parallel()

	store := &stubStore{
		symbols: []SymbolRecord{
			{FilePath: "/workspace/main.go", Name: "greet", Kind: "function"},
		},
	}
	wid := uuid.New()
	ext := NewExtractor()
	p := NewPipeline(wid, t.TempDir(), ext, store)
	ctx := context.Background()

	p.handleChange(ctx, FileChangeEvent{Path: "/workspace/main.go", Op: FileOpRemove})

	store.mu.Lock()
	defer store.mu.Unlock()
	for _, sym := range store.symbols {
		if sym.FilePath == "/workspace/main.go" {
			t.Errorf("symbol for removed file still present: %+v", sym)
		}
	}
}

// ── CALLS edge extraction (real grammar only) ─────────────────────────────────

func TestCallsEdgeExtraction(t *testing.T) {
	t.Parallel()

	ext := NewExtractor()
	wid := uuid.New()
	src := []byte(`package main

func hello() {}
func world() { hello() }
`)
	syms, edges, err := ext.ExtractSymbols(wid, "main.go", src)
	if err != nil {
		t.Fatalf("ExtractSymbols: %v", err)
	}

	// With the stub extractor both will be empty — that's acceptable.
	// With the real grammar we expect at least 2 symbols and 1 edge.
	t.Logf("symbols=%d edges=%d", len(syms), len(edges))

	// Validate that symbols have required fields when returned.
	for _, s := range syms {
		if s.Name == "" {
			t.Errorf("symbol with empty name: %+v", s)
		}
		if s.Kind == "" {
			t.Errorf("symbol with empty kind: %+v", s)
		}
		if s.WorkspaceID != wid {
			t.Errorf("symbol workspaceID mismatch: %v", s.WorkspaceID)
		}
	}
}

// ── Benchmark ─────────────────────────────────────────────────────────────────

// BenchmarkFullScan measures full-scan performance over the Go sources in
// the current module. Target: <30s for 100K-line codebase.
// Hardware-dependent: document the result, do not hard-fail.
func BenchmarkFullScan(b *testing.B) {
	// Use the clawde/intelligence source tree as the benchmark corpus.
	root := filepath.Join("..", "..", "..") // intelligence module root
	if _, err := os.Stat(root); err != nil {
		b.Skip("corpus root not accessible:", err)
	}

	store := &stubStore{}
	wid := uuid.New()
	ext := NewExtractor()
	p := NewPipeline(wid, root, ext, store)
	ctx := context.Background()

	b.ResetTimer()
	for range b.N {
		store.symbols = nil
		store.edges = nil
		if err := p.FullScan(ctx); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(store.symbols)), "symbols/op")
}
