// dense.go — DenseRetriever: pgvector cosine similarity over clawde_chunks.
//
// Purpose: Execute an HNSW approximate nearest-neighbour search against
//          clawde_chunks.embedding (vector(1024)) with RLS isolation.
//          Sets hnsw.ef_search=64 per session for the speed/recall trade-off
//          documented in ADR-005.
// Inputs:  DBQuerier (pgx in prod / stub in tests), workspace UUID, query
//          vector []float32, topK int.
// Outputs: []ScoredChunk ordered cosine similarity descending (score in [0,1]).
// Constraints: File ≤500 lines. No live PG in unit tests (skip-with-reason).
//              ef_search is a session GUC — set once per Query call.
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.DenseRetriever.
package retrieval

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

// defaultEFSearch is the HNSW ef_search value used unless overridden by env.
// 64 provides a good recall/latency balance for typical corpus sizes ≤500K chunks.
// ADR-005: increasing to 128 improves recall ~3% at ~2× latency cost.
const defaultEFSearch = 64

// DenseRetriever queries clawde_chunks via pgvector cosine distance.
//
// Purpose: Provide the semantic (dense) lane of the hybrid retrieval pipeline.
//          Relies on the HNSW index created in migration 0086.
// Inputs:  DBQuerier, workspace UUID (for RLS), query vector, topK.
// Outputs: []ScoredChunk desc by cosine similarity; error on query failure.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.DenseRetriever.
type DenseRetriever struct {
	db       lanes.DBQuerier
	efSearch int
	logger   *slog.Logger
}

// NewDenseRetriever constructs a DenseRetriever.
// Reads CLAWDE_HNSW_EF_SEARCH from env; falls back to defaultEFSearch (64).
func NewDenseRetriever(db lanes.DBQuerier) *DenseRetriever {
	ef := defaultEFSearch
	if v := os.Getenv("CLAWDE_HNSW_EF_SEARCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ef = n
		}
	}
	return &DenseRetriever{
		db:       db,
		efSearch: ef,
		logger:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

// Query executes a pgvector approximate nearest-neighbour search.
//
// Steps:
//  1. SET LOCAL app.workspace_id for RLS.
//  2. SET LOCAL hnsw.ef_search = <ef> for this session.
//  3. SELECT top-100 chunks ordered by embedding <=> $vec (cosine distance).
//
// The score returned is 1 - cosine_distance, normalised to [0,1].
// RLS policy on clawde_chunks filters by app.workspace_id automatically once
// the GUC is set. The explicit workspace_id predicate is belt-and-suspenders.
func (r *DenseRetriever) Query(
	ctx context.Context,
	workspaceID uuid.UUID,
	vec []float32,
	topK int,
) ([]ScoredChunk, error) {
	// Set RLS GUC.
	if err := r.db.Exec(ctx, "SET LOCAL app.workspace_id = $1", workspaceID.String()); err != nil {
		return nil, fmt.Errorf("dense: set workspace_id GUC: %w", err)
	}

	// Set HNSW ef_search for this session.
	efSQL := fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", r.efSearch)
	if err := r.db.Exec(ctx, efSQL); err != nil {
		// Non-fatal: log and proceed. The HNSW index still works with the server default.
		r.logger.WarnContext(ctx, "dense: set hnsw.ef_search failed; using server default",
			"ef_search", r.efSearch, "err", err)
	}

	// pgvector stores vectors as the native vector type; we pass a []float32.
	// The <=> operator is cosine distance; 1 - distance = similarity.
	const sql = `
SELECT id,
       content,
       file_path,
       1 - (embedding <=> $1::vector) AS score
FROM   clawde_chunks
WHERE  workspace_id = $2
ORDER  BY embedding <=> $1::vector
LIMIT  $3`

	rows, err := r.db.Query(ctx, sql, pgVec(vec), workspaceID, topK)
	if err != nil {
		return nil, fmt.Errorf("dense: vector search: %w", err)
	}
	defer rows.Close()

	return scanScoredChunks(rows, "dense")
}

// pgVec wraps []float32 into a string representation pgvector understands.
// pgx with pgvector registers pgvector.Vector; for our DBQuerier interface we
// pass a float32 slice which pgx encodes as text if no pgvector type codec is
// registered.  The cast ::vector in the SQL handles the text→vector conversion.
// This keeps us from importing github.com/pgvector/pgvector-go just for tests.
func pgVec(v []float32) any {
	// Return the slice directly; pgx will encode it using the registered encoder
	// (pgvector-go registers one at init when the pgvector extension is used).
	// In unit tests the DBQuerier stub ignores the value entirely.
	return v
}

// scanScoredChunks scans rows that return (id uuid, content text, file_path text, score float8).
func scanScoredChunks(rows lanes.Rows, source string) ([]ScoredChunk, error) {
	var chunks []ScoredChunk
	for rows.Next() {
		var c ScoredChunk
		c.Source = source
		if err := rows.Scan(&c.ID, &c.Content, &c.FilePath, &c.Score); err != nil {
			return nil, fmt.Errorf("dense: scan chunk: %w", err)
		}
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dense: rows error: %w", err)
	}
	return chunks, nil
}
