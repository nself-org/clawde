// Package rerank — Reranker: applies TEI BGE Reranker v2-m3 to ScoredChunk candidates.
//
// Purpose: Accept the top-N RRF candidates from HybridKernel, send their content to
//          the TEI reranker, replace scores with cross-encoder scores, and return
//          re-sorted candidates. Batch cap = CLAWDE_RERANK_BATCH (default 32).
// Inputs:  query string, candidates []retrieval.ScoredChunk.
// Outputs: []retrieval.ScoredChunk re-sorted by cross-encoder score descending.
// Constraints: ≤ CLAWDE_RERANK_BATCH texts per TEI call; larger sets split.
//              Graceful degradation: connection refused → log.Warn + return input unchanged.
// SPORT: REGISTRY-FUNCTIONS.md → rerank.Reranker.Rerank.
package rerank

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strconv"

	"github.com/nself-org/clawde/intelligence/internal/retrieval"
)

const defaultBatchSize = 32

// rerankBatchFromEnv reads CLAWDE_RERANK_BATCH (default 32).
func rerankBatchFromEnv() int {
	if v := os.Getenv("CLAWDE_RERANK_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultBatchSize
}

// RerankClient is the interface Reranker uses to score batches.
// Satisfied by *TEIRerankClient and by the mock in tests.
//
// Purpose: Seam for testing without a live TEI sidecar.
// Inputs:  ctx, query, texts.
// Outputs: []float32 scores parallel with texts, error.
type RerankClient interface {
	Rerank(ctx context.Context, query string, texts []string) ([]float32, error)
}

// Reranker wraps a RerankClient and applies it to retrieval.ScoredChunk slices.
//
// Purpose: Integrate cross-encoder reranking into the HybridKernel pipeline.
//          Splits candidates >batch cap into sub-batches, merges scores, re-sorts.
// SPORT:   REGISTRY-FUNCTIONS.md → rerank.Reranker.Rerank.
type Reranker struct {
	client    RerankClient
	batchSize int
	logger    *slog.Logger
}

// NewReranker constructs a Reranker with a TEIRerankClient.
// addr: TEI reranker base URL ("" → DefaultRerankAddr).
func NewReranker(addr string) *Reranker {
	return &Reranker{
		client:    NewTEIRerankClient(addr),
		batchSize: rerankBatchFromEnv(),
		logger:    slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// NewRerankerWithClient constructs a Reranker with an injected RerankClient.
// Used by tests to avoid hitting a live TEI endpoint.
func NewRerankerWithClient(client RerankClient, batchSize int) *Reranker {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	return &Reranker{
		client:    client,
		batchSize: batchSize,
		logger:    slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

// Rerank applies cross-encoder scoring to candidates and returns them sorted
// by rerank score descending.
//
// If the TEI client returns an error (e.g. connection refused), a warning is
// logged and the original candidates are returned unchanged — no panic.
//
// Inputs:  ctx, query string, candidates []retrieval.ScoredChunk.
// Outputs: []retrieval.ScoredChunk (same length, re-scored and sorted).
func (r *Reranker) Rerank(ctx context.Context, query string, candidates []retrieval.ScoredChunk) []retrieval.ScoredChunk {
	if len(candidates) == 0 {
		return candidates
	}

	scores, err := r.scoreAll(ctx, query, candidates)
	if err != nil {
		r.logger.WarnContext(ctx, "reranker: scoring failed; returning RRF order unchanged",
			"err", err,
			"candidates", len(candidates),
		)
		return candidates
	}

	// Apply cross-encoder scores.
	result := make([]retrieval.ScoredChunk, len(candidates))
	copy(result, candidates)
	for i := range result {
		result[i].Score = float64(scores[i])
		result[i].Source = "rerank"
	}

	// Sort descending by cross-encoder score.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result
}

// scoreAll returns scores for all candidates, splitting into batches of batchSize.
// The returned slice is index-parallel with candidates.
func (r *Reranker) scoreAll(ctx context.Context, query string, candidates []retrieval.ScoredChunk) ([]float32, error) {
	allScores := make([]float32, len(candidates))

	for start := 0; start < len(candidates); start += r.batchSize {
		end := start + r.batchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[start:end]

		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Content
		}

		scores, err := r.client.Rerank(ctx, query, texts)
		if err != nil {
			return nil, err
		}
		// scores is parallel with batch; copy into the correct slice of allScores.
		copy(allScores[start:end], scores)
	}
	return allScores, nil
}
