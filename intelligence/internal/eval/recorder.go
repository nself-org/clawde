// recorder.go — persist EvalResult rows to clawde_eval_runs via a DB seam.
//
// Purpose: Write completed eval run metrics to Postgres so W15 can read them
//          and apply the tie-break rule (BGE-M3 default; Gemini wins only if
//          recall@10 > BGE × 1.05 AND p95_ms < 200).
// Inputs:  EvalResult; workspace UUID; DBExecer seam.
// Outputs: INSERT into clawde_eval_runs; error on failure.
// Constraints: Provider/DB-dependent tests skip-with-reason.
// SPORT: REGISTRY-FUNCTIONS.md → eval.Recorder, eval.DBExecer.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DBExecer is the minimal DB interface required by Recorder.
// Satisfied by *pgx.Conn, pgxpool.Pool, and test stubs.
//
// Purpose: Seam so recorder unit tests run without a live Postgres instance.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.DBExecer.
type DBExecer interface {
	Exec(ctx context.Context, sql string, args ...any) error
}

// Recorder writes EvalResult rows into clawde_eval_runs.
//
// Purpose: Persistence layer for eval metrics; used by the run_eval CLI and W15.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.Recorder.
type Recorder struct {
	db DBExecer
}

// NewRecorder constructs a Recorder backed by the given DBExecer.
func NewRecorder(db DBExecer) *Recorder {
	return &Recorder{db: db}
}

// Record inserts one EvalResult row into clawde_eval_runs.
//
// Inputs:  ctx, workspaceID (owner of the eval run), result.
// Outputs: error on INSERT failure.
func (r *Recorder) Record(ctx context.Context, workspaceID uuid.UUID, result EvalResult) error {
	meta, err := json.Marshal(map[string]any{
		"sample_count": result.SampleCount,
		"recorded_at":  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("recorder: marshal metadata: %w", err)
	}

	const sql = `
INSERT INTO clawde_eval_runs
    (workspace_id, provider, dataset, recall_at_5, recall_at_10, mrr_at_10, p50_ms, p95_ms, run_at, metadata, name, status)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), $9, $10, 'done')`

	if err := r.db.Exec(ctx, sql,
		workspaceID,
		result.Provider,
		result.Dataset,
		result.RecallAt5,
		result.RecallAt10,
		result.MRRAt10,
		result.P50Ms,
		result.P95Ms,
		meta,
		result.Dataset, // name = dataset label
	); err != nil {
		return fmt.Errorf("recorder: insert eval run: %w", err)
	}
	return nil
}
