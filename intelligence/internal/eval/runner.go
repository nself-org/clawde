// runner.go — orchestrate a full eval run: embed, retrieve, score, record.
//
// Purpose: Wire dataset, driver, chunk store lookup, metrics, and recorder into
//          a single RunEval function that returns an EvalResult.
// Inputs:  Dataset, EmbeddingDriver, ChunkSearcher (seam), Recorder, workspaceID.
// Outputs: EvalResult; error.
// Constraints: File ≤500 lines. ChunkSearcher is injectable for unit tests.
// SPORT: REGISTRY-FUNCTIONS.md → eval.RunEval, eval.ChunkSearcher.
package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ChunkSearcher retrieves the top-k chunk IDs nearest to the query embedding.
//
// Purpose: Seam over the pgvector cosine-similarity query so tests can inject
//          a stub without a live DB.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.ChunkSearcher.
type ChunkSearcher interface {
	// Search returns ordered chunk IDs (closest first) for the given embedding.
	Search(ctx context.Context, workspaceID uuid.UUID, embedding []float32, topK int) ([]string, error)
}

// RunConfig controls the scope of a single eval run.
type RunConfig struct {
	TopK        int // max candidates to retrieve (must be ≥ 10 for Recall@10)
	SmokeLimit  int // if > 0, only run this many queries (for make eval-smoke)
}

// DefaultRunConfig returns sensible defaults.
func DefaultRunConfig() RunConfig {
	return RunConfig{TopK: 10}
}

// RunEval executes a full A/B eval run for one driver over a dataset.
//
// Purpose: Embed each query, search for top-K chunks, measure recall and MRR,
//          capture per-embed latency, and optionally persist via Recorder.
// Inputs:  ctx, dataset, driver, searcher, workspaceID, cfg.
//          rec may be nil (metrics returned but not persisted).
// Outputs: EvalResult; error on embed or search failure.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.RunEval.
func RunEval(
	ctx context.Context,
	ds *Dataset,
	driver EmbeddingDriver,
	searcher ChunkSearcher,
	workspaceID uuid.UUID,
	cfg RunConfig,
	rec *Recorder,
) (EvalResult, error) {
	if cfg.TopK < 10 {
		cfg.TopK = 10
	}

	queries := ds.Queries
	if cfg.SmokeLimit > 0 && len(queries) > cfg.SmokeLimit {
		queries = queries[:cfg.SmokeLimit]
	}

	var (
		retrieved [][]string
		relevant  [][]string
		latencies []time.Duration
	)

	for _, q := range queries {
		start := time.Now()
		vec, err := driver.Embed(ctx, q.Query)
		elapsed := time.Since(start)
		if err != nil {
			return EvalResult{}, fmt.Errorf("eval run: embed %q: %w", q.Query, err)
		}
		latencies = append(latencies, elapsed)

		ids, err := searcher.Search(ctx, workspaceID, vec, cfg.TopK)
		if err != nil {
			return EvalResult{}, fmt.Errorf("eval run: search for %q: %w", q.Query, err)
		}
		retrieved = append(retrieved, ids)
		relevant = append(relevant, q.RelevantChunkIDs)
	}

	p50 := LatencyPercentile(latencies, 50)
	p95 := LatencyPercentile(latencies, 95)

	result := EvalResult{
		Provider:    driver.Name(),
		Dataset:     ds.Name,
		RecallAt5:   RecallAtK(retrieved, relevant, 5),
		RecallAt10:  RecallAtK(retrieved, relevant, 10),
		MRRAt10:     MRRAtK(retrieved, relevant, 10),
		P50Ms:       int(p50.Milliseconds()),
		P95Ms:       int(p95.Milliseconds()),
		SampleCount: len(queries),
	}

	if rec != nil {
		if err := rec.Record(ctx, workspaceID, result); err != nil {
			// Non-fatal: return result even if persist fails.
			return result, fmt.Errorf("eval run: record: %w", err)
		}
	}
	return result, nil
}
