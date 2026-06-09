// benchmark_test.go — Go testing.B benchmarks for eval retrieval latency and
// embedding throughput. Capture a baseline via `make eval` / go test -bench.
//
// Purpose: Quantify (1) per-query retrieval latency through the ChunkSearcher
//          seam and (2) embedding throughput through the EmbeddingDriver seam,
//          using in-memory stubs so CI runs without TEI, Gemini, or Postgres.
//          Live numbers come from running against real endpoints (env-gated).
// Constraints: Stub-only; deterministic; no network. ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → eval benchmark suite.
package eval

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// benchSearcher returns a fixed top-K list of chunk IDs with no I/O.
type benchSearcher struct{ k int }

func (b *benchSearcher) Search(_ context.Context, _ uuid.UUID, _ []float32, topK int) ([]string, error) {
	n := topK
	if b.k > 0 && b.k < n {
		n = b.k
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("chunk-%d", i)
	}
	return ids, nil
}

// benchDriver is a stub embedding driver producing a fixed-dim vector.
type benchDriver struct {
	name string
	dim  int
}

func (d *benchDriver) Name() string { return d.name }
func (d *benchDriver) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, d.dim)
	// Cheap deterministic fill derived from text length (no allocation in loop body).
	for i := range vec {
		vec[i] = float32((len(text) + i) % 97)
	}
	return vec, nil
}

// BenchmarkRetrievalLatency measures the cost of a single searcher round-trip
// (the hot path of one eval query) at top-10.
func BenchmarkRetrievalLatency(b *testing.B) {
	s := &benchSearcher{}
	emb := make([]float32, 1024)
	ws := uuid.New()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Search(ctx, ws, emb, 10); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEmbeddingThroughput measures embed calls/sec through the driver seam
// at the BGE-M3 1024 dimension.
func BenchmarkEmbeddingThroughput(b *testing.B) {
	d := &benchDriver{name: "bge-m3", dim: 1024}
	ctx := context.Background()
	const sample = "What does eval.CompositeScore compute and what weights does it default to?"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Embed(ctx, sample); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompositeScore measures the pure-math composite hot path.
func BenchmarkCompositeScore(b *testing.B) {
	m := RagasMetrics{Faithfulness: 0.8, AnswerRelevancy: 0.7, ContextPrecision: 0.6, ContextRecall: 0.9}
	w := DefaultCompositeWeights()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CompositeScore(m, w); err != nil {
			b.Fatal(err)
		}
	}
}
