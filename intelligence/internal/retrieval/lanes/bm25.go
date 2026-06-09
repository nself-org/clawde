// Package lanes provides BM25 retrieval lane implementations for clawde-intelligence.
//
// Purpose: Define the BM25Lane interface and two concrete implementations:
//   - TSVectorBM25Lane  — always-available baseline using Postgres tsvector + ts_rank.
//     Requires only the GIN index on clawde_chunks.content_tsv (migration 0085).
//     No external extension required; CI passes without ParadeDB.
//   - ParadeDBBM25Lane  — optional upgrade using ParadeDB's pg_bm25 BM25 index.
//     Checks pg_bm25 availability at query time; returns a descriptive error when
//     the extension is absent (does not panic). Falls back transparently via Kernel.
//
// Inputs:  DB interface (seam for testing), workspace UUID, query string, k int.
// Outputs: []LaneResult ordered by relevance score (desc).
// Constraints:
//   - File ≤500 lines.
//   - ParadeDB is Apache 2.0 licensed (safe for MIT/commercial use).
//   - The swap tsvector→BM25 requires NO schema migration — same clawde_chunks table.
//   - PG-dependent tests skip-with-reason so CI passes without a live DB.
//
// SPORT: REGISTRY-FUNCTIONS.md → BM25Lane, TSVectorBM25Lane.BM25Query,
//        ParadeDBBM25Lane.BM25Query.
package lanes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
)

// ── Result type ───────────────────────────────────────────────────────────────

// LaneResult is a single retrieved chunk with its relevance score.
//
// Purpose: Uniform result type returned by all BM25Lane implementations,
//          enabling comparison in A/B mode.
// SPORT:   REGISTRY-FUNCTIONS.md → lanes.LaneResult.
type LaneResult struct {
	// ChunkID is the primary key of clawde_chunks.
	ChunkID uuid.UUID `json:"chunk_id"`

	// Score is the relevance score (ts_rank or paradedb.score — both higher=better).
	Score float64 `json:"score"`

	// Content is the raw chunk text (for A/B log inspection; may be empty if caller
	// opts for ID-only retrieval).
	Content string `json:"content,omitempty"`
}

// MarshalJSON implements json.Marshaler so LaneResult serialises correctly in
// JSONB columns of clawde_lane_ab_log.
func (r LaneResult) MarshalJSON() ([]byte, error) {
	type alias LaneResult
	return json.Marshal(alias(r))
}

// ── Interface ─────────────────────────────────────────────────────────────────

// BM25Lane is the retrieval abstraction over clawde_chunks.
//
// Purpose: Decouple callers from the underlying lexical engine so the kernel
//          can swap implementations (tsvector ↔ ParadeDB) via feature flag.
// Inputs:  ctx, workspaceID (RLS isolation), query, k (max results).
// Outputs: []LaneResult ordered highest-score first; error on failure.
// SPORT:   REGISTRY-FUNCTIONS.md → BM25Lane.
type BM25Lane interface {
	// BM25Query retrieves the top-k chunks for query within workspaceID.
	// Implementations must honour RLS (set app.workspace_id GUC before querying).
	BM25Query(ctx context.Context, workspaceID uuid.UUID, query string, k int) ([]LaneResult, error)

	// Name returns a stable identifier used in OTel events and A/B log rows.
	Name() string
}

// ── DB seam ───────────────────────────────────────────────────────────────────

// DBQuerier is the minimal DB interface both lanes depend on.
// The real implementation wraps pgx; tests inject a stub.
//
// Purpose: Seam so all retrieval tests run without a live Postgres instance.
// Inputs:  ctx, sql, args.
// Outputs: Rows (iterable), error.
type DBQuerier interface {
	// Query executes sql with args and returns an iterable row set.
	Query(ctx context.Context, sql string, args ...any) (Rows, error)

	// Exec executes a statement (used for SET LOCAL).
	Exec(ctx context.Context, sql string, args ...any) error
}

// Rows is the iterable result of a DB query.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}

// ── TSVectorBM25Lane ─────────────────────────────────────────────────────────

// TSVectorBM25Lane is the always-available baseline retrieval lane.
// It uses Postgres tsvector + ts_rank against clawde_chunks.content_tsv
// (GIN-indexed since migration 0085). No external extensions required.
//
// Purpose: Provide a production-quality lexical retrieval baseline that compiles
//          and passes CI without ParadeDB or pg_bm25 installed.
// Inputs:  DBQuerier, optional logger.
// Outputs: []LaneResult ordered by ts_rank DESC.
// Constraints: RLS is honoured via SET LOCAL app.workspace_id before the SELECT.
// SPORT:   REGISTRY-FUNCTIONS.md → TSVectorBM25Lane.BM25Query.
type TSVectorBM25Lane struct {
	db     DBQuerier
	logger *slog.Logger
}

// NewTSVectorBM25Lane constructs a TSVectorBM25Lane.
func NewTSVectorBM25Lane(db DBQuerier) *TSVectorBM25Lane {
	return &TSVectorBM25Lane{
		db:     db,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

// Name returns the stable lane identifier.
func (l *TSVectorBM25Lane) Name() string { return "tsvector" }

// BM25Query retrieves the top-k chunks using tsvector full-text search.
//
// SQL:
//
//	SET LOCAL app.workspace_id = '<uuid>';
//	SELECT id, ts_rank(content_tsv, query), content
//	FROM clawde_chunks, plainto_tsquery('english', $1) query
//	WHERE content_tsv @@ query
//	ORDER BY ts_rank(content_tsv, query) DESC
//	LIMIT $2;
//
// RLS policy on clawde_chunks filters by app.workspace_id automatically once
// the GUC is set. The explicit workspace_id arg is belt-and-suspenders.
func (l *TSVectorBM25Lane) BM25Query(
	ctx context.Context,
	workspaceID uuid.UUID,
	query string,
	k int,
) ([]LaneResult, error) {
	// Set RLS isolation GUC for this transaction/connection.
	if err := l.db.Exec(ctx, "SET LOCAL app.workspace_id = $1", workspaceID.String()); err != nil {
		return nil, fmt.Errorf("tsvector: set workspace_id GUC: %w", err)
	}

	const sql = `
SELECT id,
       ts_rank(content_tsv, plainto_tsquery('english', $1)) AS score,
       content
FROM   clawde_chunks
WHERE  content_tsv @@ plainto_tsquery('english', $1)
  AND  workspace_id = $2
ORDER  BY score DESC
LIMIT  $3`

	rows, err := l.db.Query(ctx, sql, query, workspaceID, k)
	if err != nil {
		return nil, fmt.Errorf("tsvector: query: %w", err)
	}
	defer rows.Close()

	return scanResults(rows)
}

// ── ParadeDBBM25Lane ─────────────────────────────────────────────────────────

// ParadeDBBM25Lane is the optional ParadeDB pg_bm25 retrieval lane.
// It checks at query time whether the pg_bm25 extension is installed.
// If absent, it returns a descriptive, non-panicking error so the kernel
// can fall back to TSVectorBM25Lane gracefully.
//
// ParadeDB is licensed under Apache 2.0 — safe for all commercial use.
//
// Purpose: Optional BM25 quality upgrade over tsvector; swapped via
//          CLAWDE_BM25_ENABLED=true without any schema migration.
// Inputs:  DBQuerier, optional logger.
// Outputs: []LaneResult ordered by paradedb.score DESC; error if pg_bm25 absent.
// Constraints: MUST NOT panic when pg_bm25 is not installed.
// SPORT:   REGISTRY-FUNCTIONS.md → ParadeDBBM25Lane.BM25Query.
type ParadeDBBM25Lane struct {
	db     DBQuerier
	logger *slog.Logger
}

// NewParadeDBBM25Lane constructs a ParadeDBBM25Lane.
func NewParadeDBBM25Lane(db DBQuerier) *ParadeDBBM25Lane {
	return &ParadeDBBM25Lane{
		db:     db,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

// Name returns the stable lane identifier.
func (l *ParadeDBBM25Lane) Name() string { return "paradedb" }

// BM25Query retrieves the top-k chunks using ParadeDB's pg_bm25 BM25 index.
//
// It first checks whether pg_bm25 is installed. If absent, it returns a
// descriptive error (never panics) so callers can fall back gracefully.
//
// SQL (when pg_bm25 present):
//
//	SET LOCAL app.workspace_id = '<uuid>';
//	SELECT id, paradedb.score(id) AS score, content
//	FROM   clawde_chunks
//	WHERE  content @@@ $1
//	  AND  workspace_id = $2
//	LIMIT  $3;
//
// The @@@ operator is provided by pg_bm25's BM25 index on clawde_chunks.content.
// No schema migration required — the BM25 index is created separately when
// ParadeDB is enabled (CLAWDE_BM25_ENABLED=true + a one-time CREATE INDEX).
func (l *ParadeDBBM25Lane) BM25Query(
	ctx context.Context,
	workspaceID uuid.UUID,
	query string,
	k int,
) ([]LaneResult, error) {
	// Check pg_bm25 availability before attempting the query.
	// Returns a descriptive error (not a panic) when absent.
	avail, err := l.checkPgBM25(ctx)
	if err != nil {
		return nil, fmt.Errorf("paradedb: extension check failed: %w", err)
	}
	if !avail {
		return nil, fmt.Errorf("paradedb: pg_bm25 extension is not installed — " +
			"install ParadeDB or set CLAWDE_BM25_ENABLED=false to use tsvector baseline")
	}

	// Set RLS isolation GUC.
	if err := l.db.Exec(ctx, "SET LOCAL app.workspace_id = $1", workspaceID.String()); err != nil {
		return nil, fmt.Errorf("paradedb: set workspace_id GUC: %w", err)
	}

	const sql = `
SELECT id,
       paradedb.score(id)::float8 AS score,
       content
FROM   clawde_chunks
WHERE  content @@@ $1
  AND  workspace_id = $2
LIMIT  $3`

	rows, err := l.db.Query(ctx, sql, query, workspaceID, k)
	if err != nil {
		return nil, fmt.Errorf("paradedb: query: %w", err)
	}
	defer rows.Close()

	return scanResults(rows)
}

// checkPgBM25 returns true when the pg_bm25 extension is installed in the
// current database. The query is lightweight (pg_extension system catalog).
func (l *ParadeDBBM25Lane) checkPgBM25(ctx context.Context) (bool, error) {
	rows, err := l.db.Query(ctx,
		"SELECT 1 FROM pg_extension WHERE extname = 'pg_bm25' LIMIT 1")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	found := rows.Next()
	if err := rows.Err(); err != nil {
		return false, err
	}
	return found, nil
}

// ── Shared helpers ─────────────────────────────────────────────────────────

// scanResults converts DB rows into []LaneResult.
// Shared by both lane implementations.
func scanResults(rows Rows) ([]LaneResult, error) {
	var results []LaneResult
	for rows.Next() {
		var r LaneResult
		if err := rows.Scan(&r.ChunkID, &r.Score, &r.Content); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return results, nil
}
