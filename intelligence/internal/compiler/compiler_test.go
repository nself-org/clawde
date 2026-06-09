// compiler_test.go — unit tests for the auto-context compiler pipeline.
//
// Purpose: Verify ExtractQuery condensation + truncation, the 60/25/15 budget
//          split, the 30s TTL cache hit, the CLAWDE_AUTO_CONTEXT disable flag,
//          the 2s SessionStart deadline graceful exit, and provenance labels.
// SPORT: REGISTRY-FUNCTIONS.md → compiler tests.
package compiler

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ---- stubs ----

type stubSymbolStore struct {
	syms map[string][]string
}

func (s *stubSymbolStore) TopSymbolsForFile(_ context.Context, _, filePath string, limit int) ([]string, error) {
	out := s.syms[filePath]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type stubRetriever struct {
	rr    *RetrievalResult
	err   error
	calls int
}

func (s *stubRetriever) RetrieveContext(_ context.Context, _, _ string) (*RetrievalResult, error) {
	s.calls++
	return s.rr, s.err
}

type stubPolicy struct {
	denied bool
	calls  int
}

func (p *stubPolicy) Evaluate(_ context.Context, _, _ string) error {
	p.calls++
	if p.denied {
		return context.Canceled
	}
	return nil
}

// ---- ExtractQuery ----

func TestExtractQuery_ActiveFileSymbolsDiff(t *testing.T) {
	t.Setenv("CLAWDE_AUTO_CONTEXT", "true")
	store := &stubSymbolStore{syms: map[string][]string{
		"internal/retrieval/hybrid.go": {"RetrieveContext", "RRFMerge", "Expand"},
	}}
	sig := SessionSignals{
		ActiveFilePath: "internal/retrieval/hybrid.go",
		VisibleSymbols: []string{"DenseRetriever", "LexicalRetriever", "HybridKernel"},
		RecentDiff:     "+ func (h *HybridKernel) Allocate(\n- func trimChunks(",
		LastError:      "panic: nil map",
	}
	q := ExtractQuery(context.Background(), store, "ws1", sig)
	if len(q) > maxQueryLen {
		t.Fatalf("query exceeds %d chars: %d", maxQueryLen, len(q))
	}
	for _, want := range []string{"RetrieveContext", "DenseRetriever", "Allocate", "trimChunks", "panic: nil map"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %s", want, q)
		}
	}
	// file stem present
	if !strings.Contains(q, "hybrid") {
		t.Errorf("query missing file stem 'hybrid': %s", q)
	}
}

func TestExtractQuery_Truncates512(t *testing.T) {
	long := strings.Repeat("X", 50)
	visible := make([]string, 40)
	for i := range visible {
		visible[i] = long
	}
	q := ExtractQuery(context.Background(), nil, "ws1", SessionSignals{
		ActiveFilePath: "a/b/c.go",
		VisibleSymbols: visible,
	})
	if len(q) > maxQueryLen {
		t.Fatalf("expected <=%d, got %d", maxQueryLen, len(q))
	}
}

// ---- budget split ----

func TestAllocate_BudgetSplitEnforced(t *testing.T) {
	// Build oversized sections so trimming must happen.
	mkChunks := func(n int) []ScoredChunk {
		out := make([]ScoredChunk, n)
		for i := range out {
			out[i] = ScoredChunk{
				FilePath: "f.go", Content: strings.Repeat("word ", 200),
				Score: float64(n - i), Method: "dense",
			}
		}
		return out
	}
	rr := RetrievalResult{
		Chunks:   mkChunks(100),
		Symbols:  []ScoredSymbol{{Name: "A", Score: 5}, {Name: "B", Score: 1}},
		Findings: []ScoredFinding{{Rule: "R", Message: "m", Score: 3}},
	}
	total := 8192
	b := Allocate(total, rr)

	chunkTokens := 0
	for _, c := range b.Chunks {
		chunkTokens += CountTokens(c.Content) + CountTokens(c.FilePath) + 8
	}
	if chunkTokens > int(float64(total)*chunkShare)+1 {
		t.Errorf("chunk tokens %d exceed 60%% budget %d", chunkTokens, int(float64(total)*chunkShare))
	}
	// highest score retained first
	if len(b.Chunks) > 0 && b.Chunks[0].Score < b.Chunks[len(b.Chunks)-1].Score {
		t.Errorf("chunks not sorted by score desc")
	}
}

// ---- cache hit / TTL ----

func TestCompileContext_CacheHitWithinTTL(t *testing.T) {
	t.Setenv("CLAWDE_AUTO_CONTEXT", "true")
	ret := &stubRetriever{rr: &RetrievalResult{
		Chunks: []ScoredChunk{{FilePath: "f.go", Content: "x", Score: 1, Method: "lexical"}},
	}}
	now := time.Unix(0, 0)
	c := NewCompiler(ret, nil, nil)
	c.now = func() time.Time { return now }

	req := CompileContextRequest{WorkspaceID: "ws1", Signals: SessionSignals{ActiveFilePath: "f.go"}}
	r1, _ := c.CompileContext(context.Background(), req)
	if r1.CacheHit {
		t.Fatal("first call should not be a cache hit")
	}
	// within 30s → hit, retriever not called again
	now = now.Add(10 * time.Second)
	r2, _ := c.CompileContext(context.Background(), req)
	if !r2.CacheHit {
		t.Errorf("expected cache hit within TTL")
	}
	if ret.calls != 1 {
		t.Errorf("retriever called %d times, want 1 (cached)", ret.calls)
	}
	// after 30s → miss
	now = now.Add(40 * time.Second)
	r3, _ := c.CompileContext(context.Background(), req)
	if r3.CacheHit {
		t.Errorf("expected cache miss after TTL expiry")
	}
	if ret.calls != 2 {
		t.Errorf("retriever called %d times after expiry, want 2", ret.calls)
	}
}

// ---- disabled flag ----

func TestCompileContext_DisabledFlag(t *testing.T) {
	t.Setenv("CLAWDE_AUTO_CONTEXT", "false")
	ret := &stubRetriever{rr: &RetrievalResult{Chunks: []ScoredChunk{{Content: "x"}}}}
	c := NewCompiler(ret, nil, nil)
	r, err := c.CompileContext(context.Background(), CompileContextRequest{WorkspaceID: "ws1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Enriched {
		t.Errorf("expected enriched=false when disabled")
	}
	if ret.calls != 0 {
		t.Errorf("retriever should not be called when disabled")
	}
}

// ---- policy routed ----

func TestCompileContext_RoutesThroughPolicy(t *testing.T) {
	t.Setenv("CLAWDE_AUTO_CONTEXT", "true")
	ret := &stubRetriever{rr: &RetrievalResult{Chunks: []ScoredChunk{{Content: "x", Method: "dense"}}}}
	pol := &stubPolicy{}
	c := NewCompiler(ret, nil, pol)
	_, err := c.CompileContext(context.Background(), CompileContextRequest{WorkspaceID: "ws1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pol.calls != 1 {
		t.Errorf("policy Evaluate called %d times, want 1 (chain not bypassed)", pol.calls)
	}
	// denied → unenriched, retriever never reached
	pol2 := &stubPolicy{denied: true}
	ret2 := &stubRetriever{rr: &RetrievalResult{}}
	c2 := NewCompiler(ret2, nil, pol2)
	r, err := c2.CompileContext(context.Background(), CompileContextRequest{WorkspaceID: "ws1"})
	if err == nil {
		t.Errorf("expected policy-denied error")
	}
	if r.Enriched || ret2.calls != 0 {
		t.Errorf("denied call must not retrieve or enrich")
	}
}

// ---- provenance labels ----

func TestFormatBlock_ProvenanceLabels(t *testing.T) {
	b := BudgetedContext{
		Chunks: []ScoredChunk{{FilePath: "a.go", Lang: "go", Content: "code", Score: 0.9876, Method: "rerank"}},
		Symbols: []ScoredSymbol{{Name: "Foo", Kind: "func", Signature: "func Foo()", FilePath: "a.go", Score: 0.5}},
		Findings: []ScoredFinding{{Rule: "SSRF", Severity: "high", FilePath: "a.go", Line: 3, Message: "bad", Score: 0.4}},
	}
	out := formatBlock(b)
	for _, want := range []string{"a.go (score=0.9876 method=rerank type=code)", "func Foo", "[high] SSRF a.go:3", contextOpen, contextClose} {
		if !strings.Contains(out, want) {
			t.Errorf("block missing %q:\n%s", want, out)
		}
	}
}

// ---- doc_type propagation (W14-T04) ----

func TestFormatBlock_DocTypePropagation(t *testing.T) {
	b := BudgetedContext{
		Chunks: []ScoredChunk{
			{FilePath: "a.go", Lang: "go", Content: "func F(){}", Score: 0.9, Method: "dense", DocType: "code"},
			{FilePath: "F.doc", Content: "F does X.", Score: 0.8, Method: "dense", DocType: "docstring"},
			{FilePath: "README.md", Content: "intro", Score: 0.7, Method: "lexical", DocType: "markdown"},
		},
	}
	out := formatBlock(b)
	// doc_type label appears for each chunk.
	for _, want := range []string{"type=code", "type=docstring", "type=markdown"} {
		if !strings.Contains(out, want) {
			t.Errorf("block missing doc_type label %q:\n%s", want, out)
		}
	}
	// Code chunk renders fenced; docstring/markdown render as plain prose.
	if !strings.Contains(out, "```go\nfunc F(){}\n```") {
		t.Errorf("code chunk should be fenced:\n%s", out)
	}
	if strings.Contains(out, "```\nF does X.") || strings.Contains(out, "```\nintro") {
		t.Errorf("docstring/markdown chunks must not be fenced:\n%s", out)
	}
}

// ---- 2s deadline graceful exit ----

func TestSessionStart_DeadlineGracefulExit(t *testing.T) {
	t.Setenv("CLAWDE_AUTO_CONTEXT", "true")
	slow := &slowRetriever{delay: 5 * time.Second}
	c := NewCompiler(slow, nil, nil)
	start := time.Now()
	r := c.SessionStart(context.Background(), CompileContextRequest{WorkspaceID: "ws1"}, nil)
	elapsed := time.Since(start)
	if r.Enriched {
		t.Errorf("expected unenriched on deadline")
	}
	if elapsed > 3*time.Second {
		t.Errorf("SessionStart took %v, expected ~2s deadline", elapsed)
	}
}

type slowRetriever struct{ delay time.Duration }

func (s *slowRetriever) RetrieveContext(ctx context.Context, _, _ string) (*RetrievalResult, error) {
	select {
	case <-time.After(s.delay):
		return &RetrievalResult{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSessionStart_FastPathEnriched(t *testing.T) {
	t.Setenv("CLAWDE_AUTO_CONTEXT", "true")
	ret := &stubRetriever{rr: &RetrievalResult{Chunks: []ScoredChunk{{FilePath: "f.go", Content: "x", Score: 1, Method: "dense"}}}}
	c := NewCompiler(ret, nil, nil)
	r := c.SessionStart(context.Background(), CompileContextRequest{WorkspaceID: "ws1"}, nil)
	if !r.Enriched {
		t.Errorf("expected enriched on fast path")
	}
	if r.TokenCount <= 0 {
		t.Errorf("expected positive token count")
	}
}
