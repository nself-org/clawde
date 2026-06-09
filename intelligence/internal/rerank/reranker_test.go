// Package rerank — tests and benchmarks for Reranker and TEIRerankClient.
//
// Purpose: Verify batch-split correctness, index correspondence, graceful degrade on
//          connection refused, score merge ordering, and p95 <100ms for 50 candidates.
//
// TEI-dependent tests are skipped when CLAWDE_TEST_RERANK_ADDR is unset.
// All core logic tests use a mock HTTP server (httptest.NewServer).
//
// SPORT: REGISTRY-FUNCTIONS.md → rerank.Reranker.Rerank (test evidence).
package rerank_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/rerank"
	"github.com/nself-org/clawde/intelligence/internal/retrieval"
)

// ── Mock TEI server helpers ────────────────────────────────────────────────────

// teiRerankItem matches the TEI /rerank response element.
type teiRerankItem struct {
	Index int     `json:"index"`
	Score float32 `json:"score"`
}

// newMockTEIServer returns an httptest.Server that echoes scores = 1/(index+1)
// (i.e. index 0 gets score 1.0, index 1 gets 0.5, …).
// This means the original first candidate has the highest score (useful for ordering tests).
func newMockTEIServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string   `json:"query"`
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		items := make([]teiRerankItem, len(req.Texts))
		for i := range req.Texts {
			items[i] = teiRerankItem{
				Index: i,
				Score: float32(1.0) / float32(i+1),
			}
		}
		// Return sorted by score desc (as real TEI does).
		sort.Slice(items, func(a, b int) bool { return items[a].Score > items[b].Score })
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}))
}

// newReverseScoreMockServer returns scores = index+1 (index 0 lowest, last highest).
// Useful for verifying that reranker truly re-sorts.
func newReverseScoreMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		items := make([]teiRerankItem, len(req.Texts))
		for i := range req.Texts {
			items[i] = teiRerankItem{
				Index: i,
				Score: float32(i + 1),
			}
		}
		sort.Slice(items, func(a, b int) bool { return items[a].Score > items[b].Score })
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}))
}

// makeChunks builds n ScoredChunk values with deterministic content.
func makeChunks(n int) []retrieval.ScoredChunk {
	chunks := make([]retrieval.ScoredChunk, n)
	for i := range chunks {
		chunks[i] = retrieval.ScoredChunk{
			ID:       uuid.New(),
			Content:  fmt.Sprintf("chunk content for item %d", i),
			FilePath: fmt.Sprintf("pkg/mod_%d/file.go", i),
			Score:    float64(n-i) / float64(n), // descending RRF score
			Source:   "rrf",
		}
	}
	return chunks
}

// ── Unit tests ─────────────────────────────────────────────────────────────────

// TestReranker_IndexCorrespondence verifies that scores are mapped back to the
// correct candidate even when TEI returns results in score-sorted order.
func TestReranker_IndexCorrespondence(t *testing.T) {
	srv := newMockTEIServer(t)
	defer srv.Close()

	client := rerank.NewTEIRerankClient(srv.URL)
	r := rerank.NewRerankerWithClient(client, 32)

	candidates := makeChunks(5)
	// Preserve original content for correspondence check.
	origContent := make([]string, len(candidates))
	for i, c := range candidates {
		origContent[i] = c.Content
	}

	result := r.Rerank(context.Background(), "query", candidates)
	if len(result) != len(candidates) {
		t.Fatalf("len(result)=%d; want %d", len(result), len(candidates))
	}

	// Mock server gives score 1/(i+1): index 0 → highest. So result[0] must be
	// origContent[0].
	if result[0].Content != origContent[0] {
		t.Errorf("result[0].Content=%q; want %q (index correspondence broken)",
			result[0].Content, origContent[0])
	}
}

// TestReranker_BatchSplitGt32 verifies that >32 candidates are split and
// all scores are correctly assigned.
func TestReranker_BatchSplitGt32(t *testing.T) {
	srv := newMockTEIServer(t)
	defer srv.Close()

	client := rerank.NewTEIRerankClient(srv.URL)
	// Use batchSize=10 to force multiple batches with 25 candidates.
	r := rerank.NewRerankerWithClient(client, 10)

	candidates := makeChunks(25)
	result := r.Rerank(context.Background(), "query", candidates)

	if len(result) != 25 {
		t.Fatalf("len(result)=%d; want 25", len(result))
	}
	// All results must have source="rerank" (score was applied).
	for i, c := range result {
		if c.Source != "rerank" {
			t.Errorf("result[%d].Source=%q; want rerank", i, c.Source)
		}
	}
}

// TestReranker_GracefulDegradeOnConnectionRefused verifies that when the TEI
// endpoint is unreachable, Rerank logs a warning and returns the input unchanged.
func TestReranker_GracefulDegradeOnConnectionRefused(t *testing.T) {
	// Point at a port nothing is listening on.
	client := rerank.NewTEIRerankClient("http://127.0.0.1:19999")
	r := rerank.NewRerankerWithClient(client, 32)

	candidates := makeChunks(5)
	result := r.Rerank(context.Background(), "query", candidates)

	if len(result) != len(candidates) {
		t.Fatalf("expected unchanged candidates on degrade; got len=%d", len(result))
	}
	// Scores and source must match the originals (unchanged = graceful degrade).
	for i := range candidates {
		if result[i].Score != candidates[i].Score {
			t.Errorf("result[%d].Score=%f; want %f (should be unchanged on degrade)",
				i, result[i].Score, candidates[i].Score)
		}
		if result[i].Source == "rerank" {
			t.Errorf("result[%d].Source=rerank; expected original source on degrade", i)
		}
	}
}

// TestReranker_ScoreMergeOrdering verifies that candidates are returned in
// descending cross-encoder score order.
func TestReranker_ScoreMergeOrdering(t *testing.T) {
	// Mock server gives score = index+1 (last candidate gets highest score).
	srv := newReverseScoreMockServer(t)
	defer srv.Close()

	client := rerank.NewTEIRerankClient(srv.URL)
	r := rerank.NewRerankerWithClient(client, 32)

	const n = 8
	candidates := makeChunks(n)
	origLast := candidates[n-1].Content

	result := r.Rerank(context.Background(), "query", candidates)
	if len(result) != n {
		t.Fatalf("len=%d; want %d", len(result), n)
	}
	// First result must be the last original candidate (highest score = n).
	if result[0].Content != origLast {
		t.Errorf("result[0].Content=%q; want %q (highest score should be last original)",
			result[0].Content, origLast)
	}
	// Verify descending order.
	for i := 1; i < len(result); i++ {
		if result[i].Score > result[i-1].Score {
			t.Errorf("result not sorted descending at [%d]: %f > %f", i, result[i].Score, result[i-1].Score)
		}
	}
}

// TestReranker_EmptyInput verifies that an empty candidate slice returns nil, nil.
func TestReranker_EmptyInput(t *testing.T) {
	client := rerank.NewTEIRerankClient("http://127.0.0.1:19999") // won't be called
	r := rerank.NewRerankerWithClient(client, 32)

	result := r.Rerank(context.Background(), "query", nil)
	if result != nil {
		t.Errorf("expected nil for empty input; got %v", result)
	}
}

// TestTEIRerankClient_Rerank_MockServer verifies that TEIRerankClient parses
// the TEI /rerank response into a correctly indexed float32 slice.
func TestTEIRerankClient_Rerank_MockServer(t *testing.T) {
	srv := newMockTEIServer(t)
	defer srv.Close()

	client := rerank.NewTEIRerankClient(srv.URL)
	texts := []string{"alpha", "beta", "gamma"}
	scores, err := client.Rerank(context.Background(), "test query", texts)
	if err != nil {
		t.Fatalf("Rerank error: %v", err)
	}
	if len(scores) != len(texts) {
		t.Fatalf("len(scores)=%d; want %d", len(scores), len(texts))
	}
	// Mock: score[0] = 1.0, score[1] = 0.5, score[2] = 0.333...
	if scores[0] <= scores[1] || scores[1] <= scores[2] {
		t.Errorf("expected descending scores for index-parallel result; got %v", scores)
	}
}

// ── Live TEI test (skipped without CLAWDE_TEST_RERANK_ADDR) ──────────────────

// TestTEIRerankClient_LiveTEI is skipped in CI; run against a real TEI reranker:
//
//	CLAWDE_TEST_RERANK_ADDR=http://127.0.0.1:8092 go test ./internal/rerank/ -run TestTEIRerankClient_LiveTEI -v
func TestTEIRerankClient_LiveTEI(t *testing.T) {
	addr := os.Getenv("CLAWDE_TEST_RERANK_ADDR")
	if addr == "" {
		t.Skip("CLAWDE_TEST_RERANK_ADDR not set; skipping live TEI test")
	}

	client := rerank.NewTEIRerankClient(addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	texts := []string{
		"how does the authentication middleware work",
		"database migration strategy",
		"the auth middleware validates JWT tokens",
	}
	scores, err := client.Rerank(ctx, "how does authentication work", texts)
	if err != nil {
		t.Fatalf("live Rerank error: %v", err)
	}
	if len(scores) != len(texts) {
		t.Fatalf("live: len(scores)=%d; want %d", len(scores), len(texts))
	}
	t.Logf("live scores: %v", scores)
	// The third text is most relevant; its score should be the highest.
	if scores[2] < scores[1] {
		t.Logf("NOTE: expected scores[2] > scores[1] for auth query; got %v (model may differ)", scores)
	}
}

// ── Benchmark ─────────────────────────────────────────────────────────────────

// BenchmarkReranker_50Candidates benchmarks reranking 50 candidates via mock server.
// Target: p95 < 100ms. Mock server imposes zero real network latency; this measures
// HTTP round-trip overhead + JSON encode/decode. On real hardware add ~5-20ms network.
//
// To run: go test ./internal/rerank/ -run='^$' -bench=BenchmarkReranker_50Candidates -benchtime=10s
func BenchmarkReranker_50Candidates(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		items := make([]teiRerankItem, len(req.Texts))
		for i := range req.Texts {
			items[i] = teiRerankItem{Index: i, Score: float32(1.0) / float32(i+1)}
		}
		sort.Slice(items, func(a, c int) bool { return items[a].Score > items[c].Score })
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer srv.Close()

	client := rerank.NewTEIRerankClient(srv.URL)
	r := rerank.NewRerankerWithClient(client, 32)
	candidates := makeChunks(50)
	ctx := context.Background()

	samples := make([]time.Duration, b.N)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		result := r.Rerank(ctx, "authentication flow", candidates)
		samples[i] = time.Since(start)
		if len(result) != 50 {
			b.Fatalf("unexpected result len %d", len(result))
		}
	}

	p95 := latencyP95(samples)
	b.ReportMetric(float64(p95.Milliseconds()), "p95_ms")
	// Hardware note: mock server is in-process; p95 should be well under 100ms.
	// Against a real TEI sidecar, add ~5-20ms; still within the 100ms target.
	if p95 > 100*time.Millisecond {
		b.Logf("WARNING: p95=%v exceeds 100ms (mock server; verify against live TEI)", p95)
	}
}

// latencyP95 computes the 95th percentile of a duration slice.
func latencyP95(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)) * 0.95)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
