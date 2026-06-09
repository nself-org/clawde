// hybrid.go — HybridKernel: dense + lexical + RRF + rerank + graph expansion + symbol boost.
//
// Purpose: Orchestrate the full hybrid retrieval pipeline per ADR-005:
//   1. DenseRetriever   — pgvector HNSW cosine similarity (top-100).
//   2. LexicalRetriever — tsvector ts_rank_cd (top-100).
//   3. RRFMerge         — Reciprocal Rank Fusion (k=60, env CLAWDE_RRF_K).
//   4. Reranker         — BGE Reranker v2-m3 via TEI at 127.0.0.1:8092 (top-50 RRF input).
//                         Optional: if nil or unavailable, skipped (graceful degrade).
//   5. GraphExpander    — BFS over clawde_graph_edges, maxHops=2, from top-10.
//   6. SymbolBoost      — if query matches clawde_symbols name (pg_trgm similarity > 0.7),
//                         boost that chunk's score by symbolBoostFactor.
//   7. RetrieveContext  — return RetrievalContext{Chunks, Symbols}.
//
// Inputs:  HybridConfig, DBQuerier, query string, query vector, workspaceID.
// Outputs: RetrievalContext.
// Constraints: File ≤500 lines. No live PG in unit tests (HybridKernel accepts
//              stub retrievers via NewHybridKernelWithComponents).
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.HybridKernel, retrieval.NewHybridKernel.
package retrieval

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

// symbolBoostFactor multiplies the RRF score of chunks that contain a matched
// symbol. 1.3 gives matched symbol chunks a 30% score lift.
const symbolBoostFactor = 1.3

// graphSeedCount is the number of top-RRF chunks used as BFS seeds.
const graphSeedCount = 10

// HybridConfig holds configuration for the hybrid kernel.
//
// All fields have sensible defaults loaded via LoadHybridConfig.
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.HybridConfig.
type HybridConfig struct {
	// RRFK is the Borda constant for RRF fusion. Default 60 (CLAWDE_RRF_K).
	RRFK float64

	// TopK is the maximum number of chunks returned. Default 20.
	TopK int

	// GraphMaxHops is the BFS depth for graph expansion. Default 2.
	GraphMaxHops int

	// SymbolSimilarityThreshold is the pg_trgm similarity floor for symbol boost.
	// Default 0.7.
	SymbolSimilarityThreshold float64
}

// LoadHybridConfig builds a HybridConfig from environment + defaults.
func LoadHybridConfig() HybridConfig {
	return HybridConfig{
		RRFK:                      LoadRRFConfig(),
		TopK:                      20,
		GraphMaxHops:              2,
		SymbolSimilarityThreshold: 0.7,
	}
}

// rrfRerankCandidates is the number of top-RRF chunks fed into the reranker.
const rrfRerankCandidates = 50

// ChunkReranker is the optional interface for cross-encoder reranking.
// If nil or if Rerank returns an error, the pipeline falls back to RRF order.
//
// Purpose: Seam so HybridKernel can accept the rerank.Reranker without an import cycle.
//          (internal/rerank imports internal/retrieval; retrieval references the interface only.)
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.ChunkReranker.
type ChunkReranker interface {
	Rerank(ctx context.Context, query string, candidates []ScoredChunk) []ScoredChunk
}

// ── Component interfaces (for testability) ────────────────────────────────────

// denseQuerier is the testable interface for the dense retrieval lane.
type denseQuerier interface {
	Query(ctx context.Context, workspaceID uuid.UUID, vec []float32, topK int) ([]ScoredChunk, error)
}

// lexicalQuerier is the testable interface for the lexical retrieval lane.
type lexicalQuerier interface {
	Query(ctx context.Context, workspaceID uuid.UUID, query string, topK int) ([]ScoredChunk, error)
}

// graphExpQuerier is the testable interface for graph expansion.
type graphExpQuerier interface {
	Expand(ctx context.Context, workspaceID uuid.UUID, seedIDs []uuid.UUID, maxHops int) ([]uuid.UUID, error)
}

// symbolQuerier is the testable interface for pg_trgm symbol lookup.
type symbolQuerier interface {
	QuerySymbols(ctx context.Context, workspaceID uuid.UUID, query string, simThreshold float64) ([]SymbolMatch, error)
}

// chunkFetcher fetches full chunk content by ID (for graph-expanded chunks).
type chunkFetcher interface {
	FetchChunks(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID) ([]ScoredChunk, error)
}

// ── HybridKernel ─────────────────────────────────────────────────────────────

// HybridKernel orchestrates the full multi-lane retrieval pipeline.
//
// Purpose: Single entry point for RetrieveContext so callers (server handlers)
//          need not compose the pipeline manually.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.HybridKernel.
type HybridKernel struct {
	cfg      HybridConfig
	dense    denseQuerier
	lexical  lexicalQuerier
	graph    graphExpQuerier
	symbols  symbolQuerier
	fetcher  chunkFetcher
	reranker ChunkReranker // optional; nil → skip reranking
	logger   *slog.Logger
}

// NewHybridKernel constructs a HybridKernel backed by live DB implementations.
func NewHybridKernel(db lanes.DBQuerier, cfg HybridConfig) *HybridKernel {
	return &HybridKernel{
		cfg:     cfg,
		dense:   NewDenseRetriever(db),
		lexical: NewLexicalRetriever(db),
		graph:   NewGraphExpander(db),
		symbols: NewPgTrgmSymbolQuerier(db),
		fetcher: NewPgChunkFetcher(db),
		logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// WithReranker attaches an optional cross-encoder reranker to the kernel.
// If r is nil, the pipeline skips reranking and returns RRF order.
// Call after NewHybridKernel or NewHybridKernelWithComponents.
func (h *HybridKernel) WithReranker(r ChunkReranker) *HybridKernel {
	h.reranker = r
	return h
}

// NewHybridKernelWithComponents constructs a HybridKernel with injected components.
// Used by tests to avoid live DB connections.
// Pass a non-nil reranker via WithReranker after construction when needed.
func NewHybridKernelWithComponents(
	cfg HybridConfig,
	dense denseQuerier,
	lexical lexicalQuerier,
	graph graphExpQuerier,
	symbols symbolQuerier,
	fetcher chunkFetcher,
) *HybridKernel {
	return &HybridKernel{
		cfg:     cfg,
		dense:   dense,
		lexical: lexical,
		graph:   graph,
		symbols: symbols,
		fetcher: fetcher,
		logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

// RetrieveContext executes the full hybrid retrieval pipeline and returns a
// RetrievalContext suitable for building an LLM prompt.
//
// Pipeline:
//  1. Dense: top-100 by cosine similarity using queryVec.
//  2. Lexical: top-100 by ts_rank_cd using queryStr.
//  3. RRF: fuse dense + lexical → merged list.
//  4. Rerank: cross-encoder on top-50 RRF chunks via TEI:8092 (skipped if reranker=nil).
//  5. GraphExpand: BFS from top-10 merged chunks, maxHops=2.
//  6. SymbolBoost: query pg_trgm for symbol name matches; boost chunk scores.
//  7. Truncate to cfg.TopK and return.
//
// If dense retrieval is skipped (queryVec == nil), only lexical is used.
func (h *HybridKernel) RetrieveContext(
	ctx context.Context,
	workspaceID uuid.UUID,
	queryStr string,
	queryVec []float32,
) (*RetrievalContext, error) {
	const laneTopK = 100

	// 1. Dense retrieval (skip if no embedding provided).
	var denseResults []ScoredChunk
	if len(queryVec) > 0 {
		dr, err := h.dense.Query(ctx, workspaceID, queryVec, laneTopK)
		if err != nil {
			// Non-fatal: log and proceed with lexical only.
			h.logger.WarnContext(ctx, "hybrid: dense retrieval failed; continuing lexical-only",
				"err", err)
		} else {
			denseResults = dr
		}
	}

	// 2. Lexical retrieval.
	lexResults, err := h.lexical.Query(ctx, workspaceID, queryStr, laneTopK)
	if err != nil {
		return nil, fmt.Errorf("hybrid: lexical retrieval: %w", err)
	}

	// 3. RRF fusion.
	merged := RRFMerge(denseResults, lexResults, h.cfg.RRFK)

	// 4. Reranking (optional). Feed top-50 RRF candidates to the cross-encoder.
	//    If the reranker is nil or returns an error, continue with RRF order.
	if h.reranker != nil {
		rerankInput := merged
		if len(rerankInput) > rrfRerankCandidates {
			rerankInput = merged[:rrfRerankCandidates]
		}
		reranked := h.reranker.Rerank(ctx, queryStr, rerankInput)
		// Reranker returns input unchanged on error (graceful degrade handled inside).
		// Rebuild merged: reranked slice + any chunks beyond rrfRerankCandidates.
		if len(merged) > rrfRerankCandidates {
			merged = append(reranked, merged[rrfRerankCandidates:]...)
		} else {
			merged = reranked
		}
	}

	// 5b. Graph expansion from top-10 seeds.
	seeds := extractTopIDs(merged, graphSeedCount)
	expanded, err := h.graph.Expand(ctx, workspaceID, seeds, h.cfg.GraphMaxHops)
	if err != nil {
		// Non-fatal: log and continue without expansion.
		h.logger.WarnContext(ctx, "hybrid: graph expansion failed; continuing without expansion",
			"err", err)
		expanded = nil
	}

	// Fetch content for graph-expanded chunks and append with score=0 (they
	// receive any symbol boost but no RRF score; ranked after RRF results).
	if len(expanded) > 0 {
		expChunks, fetchErr := h.fetcher.FetchChunks(ctx, workspaceID, expanded)
		if fetchErr != nil {
			h.logger.WarnContext(ctx, "hybrid: fetch expanded chunks failed", "err", fetchErr)
		} else {
			for i := range expChunks {
				expChunks[i].Source = "graph"
			}
			merged = append(merged, expChunks...)
		}
	}

	// 6. Symbol boost.
	symbols, symErr := h.symbols.QuerySymbols(ctx, workspaceID, queryStr, h.cfg.SymbolSimilarityThreshold)
	if symErr != nil {
		// Non-fatal.
		h.logger.WarnContext(ctx, "hybrid: symbol query failed", "err", symErr)
		symbols = nil
	}
	if len(symbols) > 0 {
		merged = applySymbolBoost(merged, symbols, symbolBoostFactor)
	}

	// Re-sort after potential score mutations from symbol boost.
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	// 7. Truncate.
	if h.cfg.TopK > 0 && len(merged) > h.cfg.TopK {
		merged = merged[:h.cfg.TopK]
	}

	return &RetrievalContext{
		Chunks:  merged,
		Symbols: symbols,
	}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// extractTopIDs returns the IDs of the first n chunks (or all if len < n).
func extractTopIDs(chunks []ScoredChunk, n int) []uuid.UUID {
	if n > len(chunks) {
		n = len(chunks)
	}
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = chunks[i].ID
	}
	return ids
}

// applySymbolBoost multiplies the score of any chunk whose FilePath matches a
// symbol's FilePath by the boost factor.
func applySymbolBoost(chunks []ScoredChunk, symbols []SymbolMatch, factor float64) []ScoredChunk {
	// Build set of file paths that contain matched symbols.
	boostFiles := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		if s.FilePath != "" {
			boostFiles[s.FilePath] = true
		}
	}
	if len(boostFiles) == 0 {
		return chunks
	}
	for i := range chunks {
		if boostFiles[chunks[i].FilePath] {
			chunks[i].Score *= factor
		}
	}
	return chunks
}

// ── PgTrgmSymbolQuerier ───────────────────────────────────────────────────────

// PgTrgmSymbolQuerier queries clawde_symbols using pg_trgm similarity().
//
// Purpose: Detect when the query contains a known symbol name so HybridKernel
//          can boost chunks from that symbol's file.
// Inputs:  DBQuerier, workspace UUID, query string, similarity threshold.
// Outputs: []SymbolMatch for symbols whose name similarity > threshold.
// Constraints: pg_trgm must be enabled (CREATE EXTENSION IF NOT EXISTS pg_trgm).
//              In tests without live PG, the stub returns empty/nil.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.PgTrgmSymbolQuerier.
type PgTrgmSymbolQuerier struct {
	db lanes.DBQuerier
}

// NewPgTrgmSymbolQuerier constructs a PgTrgmSymbolQuerier.
func NewPgTrgmSymbolQuerier(db lanes.DBQuerier) *PgTrgmSymbolQuerier {
	return &PgTrgmSymbolQuerier{db: db}
}

// QuerySymbols returns symbols whose name has pg_trgm similarity > threshold
// with any word in the query.
func (q *PgTrgmSymbolQuerier) QuerySymbols(
	ctx context.Context,
	workspaceID uuid.UUID,
	query string,
	simThreshold float64,
) ([]SymbolMatch, error) {
	// Set RLS GUC.
	if err := q.db.Exec(ctx, "SET LOCAL app.workspace_id = $1", workspaceID.String()); err != nil {
		return nil, fmt.Errorf("symbol: set workspace_id GUC: %w", err)
	}

	// similarity(name, query) from pg_trgm.
	// Limit 10 to cap the boost surface.
	const sql = `
SELECT name, kind, signature, file_path
FROM   clawde_symbols
WHERE  workspace_id = $1
  AND  similarity(name, $2) > $3
ORDER  BY similarity(name, $2) DESC
LIMIT  10`

	rows, err := q.db.Query(ctx, sql, workspaceID, query, simThreshold)
	if err != nil {
		return nil, fmt.Errorf("symbol: pg_trgm query: %w", err)
	}
	defer rows.Close()

	var symbols []SymbolMatch
	for rows.Next() {
		var s SymbolMatch
		if err := rows.Scan(&s.Name, &s.Kind, &s.Signature, &s.FilePath); err != nil {
			return nil, fmt.Errorf("symbol: scan: %w", err)
		}
		symbols = append(symbols, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("symbol: rows error: %w", err)
	}
	return symbols, nil
}

// ── PgChunkFetcher ────────────────────────────────────────────────────────────

// PgChunkFetcher fetches chunk content by ID from clawde_chunks.
//
// Purpose: Retrieve full content for graph-expanded chunk IDs that were not
//          in the original dense/lexical results.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.PgChunkFetcher.
type PgChunkFetcher struct {
	db lanes.DBQuerier
}

// NewPgChunkFetcher constructs a PgChunkFetcher.
func NewPgChunkFetcher(db lanes.DBQuerier) *PgChunkFetcher {
	return &PgChunkFetcher{db: db}
}

// FetchChunks fetches content + file_path for the given chunk IDs.
func (f *PgChunkFetcher) FetchChunks(
	ctx context.Context,
	workspaceID uuid.UUID,
	ids []uuid.UUID,
) ([]ScoredChunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = id.String()
	}

	const sql = `
SELECT id, content, file_path, 0::float8 AS score
FROM   clawde_chunks
WHERE  workspace_id = $1
  AND  id = ANY($2::uuid[])`

	rows, err := f.db.Query(ctx, sql, workspaceID, idStrs)
	if err != nil {
		return nil, fmt.Errorf("fetch chunks: query: %w", err)
	}
	defer rows.Close()

	return scanScoredChunks(rows, "graph")
}
