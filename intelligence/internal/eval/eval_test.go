// eval_test.go — unit tests for dataset loading, metrics math, driver interface,
// and recorder write, all without a live DB or HTTP endpoint.
//
// Purpose: Verify correctness of pure-function metrics, dataset parser, and the
//          recorder seam so CI passes without Postgres or TEI running.
// Constraints: All provider/DB-dependent paths are skipped with t.Skip(reason).
package eval

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ── Dataset ───────────────────────────────────────────────────────────────────

func TestLoadDataset_valid(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.jsonl")
	content := `{"query":"What does nself.NewBM25Kernel do?","relevant_chunk_ids":["chunk-a","chunk-b"],"language":"go","category":"symbol_lookup"}
{"query":"How do I install a plugin?","relevant_chunk_ids":["chunk-c"],"language":"go","category":"usage_example"}
`
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, err := LoadDataset(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ds.Queries) != 2 {
		t.Errorf("want 2 queries, got %d", len(ds.Queries))
	}
	if ds.Queries[0].Language != LangGo {
		t.Errorf("want language go, got %s", ds.Queries[0].Language)
	}
	if ds.Queries[1].Category != CategoryUsageExample {
		t.Errorf("want usage_example, got %s", ds.Queries[1].Category)
	}
}

func TestLoadDataset_missingFile(t *testing.T) {
	_, err := LoadDataset("/does/not/exist.jsonl")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadDataset_emptyQuery(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(f, []byte(`{"query":"","relevant_chunk_ids":["x"],"language":"go","category":"symbol_lookup"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDataset(f)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestLoadDataset_emptyRelevant(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(f, []byte(`{"query":"q","relevant_chunk_ids":[],"language":"go","category":"symbol_lookup"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDataset(f)
	if err == nil {
		t.Fatal("expected error for empty relevant_chunk_ids")
	}
}

// ── Metrics ───────────────────────────────────────────────────────────────────

func TestRecallAtK_perfectRetrieval(t *testing.T) {
	retrieved := [][]string{{"a", "b", "c"}, {"d", "e", "f"}}
	relevant := [][]string{{"a"}, {"d"}}
	got := RecallAtK(retrieved, relevant, 3)
	if got != 1.0 {
		t.Errorf("perfect recall: want 1.0, got %f", got)
	}
}

func TestRecallAtK_noHits(t *testing.T) {
	retrieved := [][]string{{"x", "y"}}
	relevant := [][]string{{"z"}}
	got := RecallAtK(retrieved, relevant, 2)
	if got != 0.0 {
		t.Errorf("no hits: want 0.0, got %f", got)
	}
}

func TestRecallAtK_halfHits(t *testing.T) {
	retrieved := [][]string{{"a"}, {"x"}}
	relevant := [][]string{{"a"}, {"z"}}
	got := RecallAtK(retrieved, relevant, 1)
	const want = 0.5
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("half hits: want %f, got %f", want, got)
	}
}

func TestRecallAtK_cutoffRespected(t *testing.T) {
	// relevant ID only appears at position 3 (0-indexed), k=2 — should miss.
	retrieved := [][]string{{"a", "b", "RELEVANT"}}
	relevant := [][]string{{"RELEVANT"}}
	got := RecallAtK(retrieved, relevant, 2)
	if got != 0.0 {
		t.Errorf("cutoff: want 0.0 (hit outside k), got %f", got)
	}
}

func TestMRRAtK_firstHit(t *testing.T) {
	retrieved := [][]string{{"a", "b"}, {"b", "a"}}
	relevant := [][]string{{"a"}, {"a"}}
	got := MRRAtK(retrieved, relevant, 10)
	// Query 1: rank 1 → 1.0; Query 2: rank 2 → 0.5; mean = 0.75
	const want = 0.75
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("mrr: want %f, got %f", want, got)
	}
}

func TestMRRAtK_noHit(t *testing.T) {
	retrieved := [][]string{{"x", "y"}}
	relevant := [][]string{{"z"}}
	got := MRRAtK(retrieved, relevant, 10)
	if got != 0.0 {
		t.Errorf("mrr no hit: want 0.0, got %f", got)
	}
}

func TestLatencyPercentile(t *testing.T) {
	samples := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		150 * time.Millisecond,
		200 * time.Millisecond,
		250 * time.Millisecond,
		300 * time.Millisecond,
	}
	p50 := LatencyPercentile(samples, 50)
	if p50 != 50*time.Millisecond {
		t.Errorf("p50: want 50ms, got %v", p50)
	}
	// ceil(0.95 * 10) = 10 → idx=9 → 300ms (10-sample distribution).
	p95 := LatencyPercentile(samples, 95)
	if p95 != 300*time.Millisecond {
		t.Errorf("p95: want 300ms, got %v", p95)
	}
}

func TestLatencyPercentile_empty(t *testing.T) {
	if got := LatencyPercentile(nil, 95); got != 0 {
		t.Errorf("empty: want 0, got %v", got)
	}
}

// ── Driver interface via mock ─────────────────────────────────────────────────

type mockDriver struct{ name string }

func (m *mockDriver) Name() string { return m.name }
func (m *mockDriver) Embed(_ context.Context, text string) ([]float32, error) {
	// Return a fixed 4-dim vector based on text length — deterministic.
	v := float32(len(text))
	return []float32{v, v, v, v}, nil
}

func TestDriverInterface(t *testing.T) {
	d := &mockDriver{name: "mock"}
	if d.Name() != "mock" {
		t.Fatal("name mismatch")
	}
	vec, err := d.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 4 {
		t.Errorf("want 4-dim, got %d", len(vec))
	}
}

// ── Recorder via mock DB ──────────────────────────────────────────────────────

type mockDB struct{ called int; lastSQL string }

func (m *mockDB) Exec(_ context.Context, sql string, _ ...any) error {
	m.called++
	m.lastSQL = sql
	return nil
}

func TestRecorder_write(t *testing.T) {
	db := &mockDB{}
	rec := NewRecorder(db)
	result := EvalResult{
		Provider:    "bge-m3",
		Dataset:     "testdata/golden_queries.jsonl",
		RecallAt5:   0.8,
		RecallAt10:  0.9,
		MRRAt10:     0.75,
		P50Ms:       45,
		P95Ms:       120,
		SampleCount: 30,
	}
	wid := uuid.New()
	if err := rec.Record(context.Background(), wid, result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db.called != 1 {
		t.Errorf("expected 1 Exec call, got %d", db.called)
	}
}

// ── RunEval with mocks ────────────────────────────────────────────────────────

type mockSearcher struct{}

func (m *mockSearcher) Search(_ context.Context, _ uuid.UUID, _ []float32, topK int) ([]string, error) {
	// Return IDs that match the first relevant chunk in every query (simulates perfect recall).
	ids := make([]string, topK)
	ids[0] = "chunk-a" // golden_queries.jsonl line 1 starts with chunk-a
	for i := 1; i < topK; i++ {
		ids[i] = "other"
	}
	return ids, nil
}

func TestRunEval_smokeWithMocks(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "mini.jsonl")
	content := `{"query":"What is NewBM25Kernel?","relevant_chunk_ids":["chunk-a"],"language":"go","category":"symbol_lookup"}
{"query":"How to add a plugin?","relevant_chunk_ids":["chunk-b"],"language":"go","category":"usage_example"}
`
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, err := LoadDataset(f)
	if err != nil {
		t.Fatal(err)
	}

	driver := &mockDriver{name: "bge-m3"}
	searcher := &mockSearcher{}
	wid := uuid.New()
	cfg := DefaultRunConfig()

	result, err := RunEval(context.Background(), ds, driver, searcher, wid, cfg, nil)
	if err != nil {
		t.Fatalf("RunEval: %v", err)
	}
	if result.Provider != "bge-m3" {
		t.Errorf("provider: want bge-m3, got %s", result.Provider)
	}
	if result.SampleCount != 2 {
		t.Errorf("sample count: want 2, got %d", result.SampleCount)
	}
	// mockSearcher returns chunk-a first, relevant for query 1 → Recall@10 ≥ 0.5.
	if result.RecallAt10 < 0.4 {
		t.Errorf("recall@10 unexpectedly low: %f", result.RecallAt10)
	}
}

// ── BGEDriver / GeminiDriver: skip live endpoint tests ───────────────────────

func TestBGEDriver_skipsLiveEndpoint(t *testing.T) {
	t.Skip("BGEDriver requires live TEI at :8080 — skipped in CI")
}

func TestGeminiDriver_skipsLiveGateway(t *testing.T) {
	t.Skip("GeminiDriver requires live gateway provider — skipped in CI")
}
