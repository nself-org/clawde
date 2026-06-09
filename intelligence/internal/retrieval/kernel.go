// Package retrieval — BM25 kernel: wires lanes, feature flag, A/B harness.
//
// Purpose: Route BM25 retrieval requests to the correct lane based on Config,
//          handle ParadeDB fallback to tsvector on error with OTel event emission,
//          and write comparative A/B logs to clawde_lane_ab_log when ABMode is active.
//
// Inputs:  Config (from LoadConfig), DBQuerier (pgx in prod / stub in tests),
//          ABLogWriter (writes to clawde_lane_ab_log).
// Outputs: []lanes.LaneResult from the active lane; OTel-compatible log events;
//          A/B rows in clawde_lane_ab_log.
// Constraints: File ≤500 lines. All ParadeDB errors degrade gracefully to tsvector.
//
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.Kernel, retrieval.NewBM25Kernel.
package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

// ── OTel event names ─────────────────────────────────────────────────────────

const (
	// EventBM25Fallback is emitted as a structured log event when ParadeDB returns
	// an error and the kernel falls back to the tsvector lane.
	// Consumers (e.g., OTel collector) treat this as a span event.
	EventBM25Fallback = "bm25_fallback"
)

// ── ABLogWriter seam ─────────────────────────────────────────────────────────

// ABLogWriter persists comparative A/B results to clawde_lane_ab_log.
// The real implementation executes an INSERT via pgx; tests inject a stub.
//
// Purpose: Seam so A/B log writes are testable without a live Postgres instance.
// Inputs:  ctx, row fields.
// Outputs: error if the INSERT fails.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.ABLogWriter.
type ABLogWriter interface {
	WriteABLog(ctx context.Context, row ABLogRow) error
}

// ABLogRow mirrors the clawde_lane_ab_log schema (migration 0087).
type ABLogRow struct {
	WorkspaceID   uuid.UUID          `json:"workspace_id"`
	Query         string             `json:"query"`
	TSVectorTop10 []lanes.LaneResult `json:"tsvector_top10"`
	BM25Top10     []lanes.LaneResult `json:"bm25_top10"`
}

// ── Kernel ────────────────────────────────────────────────────────────────────

// Kernel routes BM25 retrieval requests to the appropriate lane.
//
// Feature-flag behaviour:
//   - BM25Enabled=false (default): always use tsvector lane.
//   - BM25Enabled=true:            use ParadeDB lane; on error, fall back to
//     tsvector and emit EventBM25Fallback OTel log event.
//   - BM25ABMode=true:             query both lanes; log top-10 to ABLogWriter;
//     return ParadeDB results (or tsvector on fallback).
//
// Purpose: Single kernel that owns the swap contract and A/B harness so callers
//          never need to reason about lane selection.
// Inputs:  Config, tsvector lane, paradedb lane, ABLogWriter, logger.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.Kernel.
type Kernel struct {
	cfg      Config
	tsvector lanes.BM25Lane
	paradedb lanes.BM25Lane
	abWriter ABLogWriter
	logger   *slog.Logger
}

// NewBM25Kernel constructs a Kernel wired with both lane implementations.
// abWriter may be nil when BM25ABMode is false; it will not be called.
func NewBM25Kernel(
	cfg Config,
	db lanes.DBQuerier,
	abWriter ABLogWriter,
) *Kernel {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return &Kernel{
		cfg:      cfg,
		tsvector: lanes.NewTSVectorBM25Lane(db),
		paradedb: lanes.NewParadeDBBM25Lane(db),
		abWriter: abWriter,
		logger:   logger,
	}
}

// NewBM25KernelWithLanes constructs a Kernel with explicit lane instances.
// Used by tests to inject stub lanes without a real DB connection.
//
// Purpose: Test seam — allows unit tests to verify routing logic without
//          standing up a Postgres instance or the ParadeDB extension.
// Inputs:  Config, tsvector lane, paradedb lane (may be nil when BM25Enabled=false),
//          ABLogWriter (NoopABLogWriter when ABMode=false).
func NewBM25KernelWithLanes(
	cfg Config,
	tsvector lanes.BM25Lane,
	paradedb lanes.BM25Lane,
	abWriter ABLogWriter,
) *Kernel {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return &Kernel{
		cfg:      cfg,
		tsvector: tsvector,
		paradedb: paradedb,
		abWriter: abWriter,
		logger:   logger,
	}
}

// Query routes retrieval to the appropriate lane per Config.
//
// Returns top-k results ordered highest-score first.
// On ParadeDB error, falls back to tsvector and emits a bm25_fallback event.
func (k *Kernel) Query(
	ctx context.Context,
	workspaceID uuid.UUID,
	query string,
	topK int,
) ([]lanes.LaneResult, error) {
	if !k.cfg.BM25Enabled && !k.cfg.BM25ABMode {
		// Default path: tsvector only.
		return k.tsvector.BM25Query(ctx, workspaceID, query, topK)
	}

	if k.cfg.BM25ABMode {
		return k.runABQuery(ctx, workspaceID, query, topK)
	}

	// BM25Enabled=true, ABMode=false: ParadeDB with fallback.
	return k.queryWithFallback(ctx, workspaceID, query, topK)
}

// queryWithFallback tries ParadeDB; on error emits EventBM25Fallback and
// returns tsvector results.
func (k *Kernel) queryWithFallback(
	ctx context.Context,
	workspaceID uuid.UUID,
	query string,
	topK int,
) ([]lanes.LaneResult, error) {
	results, err := k.paradedb.BM25Query(ctx, workspaceID, query, topK)
	if err != nil {
		// Emit OTel-compatible fallback event as structured log.
		k.logger.LogAttrs(ctx, slog.LevelWarn, EventBM25Fallback,
			slog.String("reason", err.Error()),
			slog.String("fallback_lane", k.tsvector.Name()),
			slog.String("workspace_id", workspaceID.String()),
		)
		// Degrade to tsvector — never surface ParadeDB absence as a user error.
		return k.tsvector.BM25Query(ctx, workspaceID, query, topK)
	}
	return results, nil
}

// runABQuery runs both lanes concurrently (sequential in this impl to keep
// the seam simple; a future ticket can parallelize), logs top-10 from each
// to clawde_lane_ab_log, and returns ParadeDB results (with tsvector fallback).
func (k *Kernel) runABQuery(
	ctx context.Context,
	workspaceID uuid.UUID,
	query string,
	topK int,
) ([]lanes.LaneResult, error) {
	const abTop = 10

	// Tsvector results — always succeeds.
	tsResults, tsErr := k.tsvector.BM25Query(ctx, workspaceID, query, abTop)
	if tsErr != nil {
		return nil, fmt.Errorf("ab mode: tsvector lane: %w", tsErr)
	}

	// ParadeDB results — may fail gracefully.
	bm25Results, bm25Err := k.paradedb.BM25Query(ctx, workspaceID, query, abTop)
	if bm25Err != nil {
		k.logger.LogAttrs(ctx, slog.LevelWarn, EventBM25Fallback,
			slog.String("reason", bm25Err.Error()),
			slog.String("fallback_lane", k.tsvector.Name()),
			slog.String("workspace_id", workspaceID.String()),
		)
		// A/B mode still logs; bm25_top10 will be empty JSONB.
		bm25Results = nil
	}

	// Log the A/B comparison row asynchronously best-effort.
	if k.abWriter != nil {
		row := ABLogRow{
			WorkspaceID:   workspaceID,
			Query:         query,
			TSVectorTop10: tsResults,
			BM25Top10:     bm25Results,
		}
		if logErr := k.abWriter.WriteABLog(ctx, row); logErr != nil {
			k.logger.Warn("ab log write failed", "err", logErr)
		}
	}

	// Return paradedb results if available; otherwise tsvector.
	if bm25Results != nil {
		// Caller asked for topK; ab query fetched abTop — re-query at topK if needed.
		if topK <= abTop {
			return truncate(bm25Results, topK), nil
		}
		// topK > abTop: run a full-size paradedb query.
		return k.queryWithFallback(ctx, workspaceID, query, topK)
	}
	return truncate(tsResults, topK), nil
}

// truncate returns the first n elements of results, or results if len < n.
func truncate(results []lanes.LaneResult, n int) []lanes.LaneResult {
	if n <= 0 || len(results) <= n {
		return results
	}
	return results[:n]
}

// ── Default no-op ABLogWriter ─────────────────────────────────────────────────

// NoopABLogWriter satisfies ABLogWriter without writing anything.
// Use when BM25ABMode=false.
type NoopABLogWriter struct{}

func (NoopABLogWriter) WriteABLog(_ context.Context, _ ABLogRow) error { return nil }

// ── PgxABLogWriter ────────────────────────────────────────────────────────────

// PgxABLogWriter writes A/B log rows to clawde_lane_ab_log via a DBQuerier.
// It serialises the top-10 slices to JSONB using encoding/json.
//
// Purpose: Real implementation of ABLogWriter for production use.
// Inputs:  DBQuerier (wraps pgx), ABLogRow.
// Outputs: INSERT into clawde_lane_ab_log; error on failure.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.PgxABLogWriter.
type PgxABLogWriter struct {
	db lanes.DBQuerier
}

// NewPgxABLogWriter constructs a PgxABLogWriter.
func NewPgxABLogWriter(db lanes.DBQuerier) *PgxABLogWriter {
	return &PgxABLogWriter{db: db}
}

// WriteABLog inserts one row into clawde_lane_ab_log.
func (w *PgxABLogWriter) WriteABLog(ctx context.Context, row ABLogRow) error {
	tsJSON, err := json.Marshal(row.TSVectorTop10)
	if err != nil {
		return fmt.Errorf("ab log: marshal tsvector results: %w", err)
	}
	bm25JSON, err := json.Marshal(row.BM25Top10)
	if err != nil {
		return fmt.Errorf("ab log: marshal bm25 results: %w", err)
	}

	const sql = `
INSERT INTO clawde_lane_ab_log (workspace_id, query, tsvector_top10, bm25_top10)
VALUES ($1, $2, $3, $4)`

	return w.db.Exec(ctx, sql, row.WorkspaceID, row.Query, tsJSON, bm25JSON)
}
