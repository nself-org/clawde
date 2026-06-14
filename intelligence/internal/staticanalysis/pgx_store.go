// pgx_store.go — pgx-backed FindingsStore for staticanalysis.
//
// Purpose: Implement staticanalysis.FindingsStore against the clawde_findings
//          table using pgx/v5. UpsertFindings uses INSERT … ON CONFLICT DO NOTHING
//          so repeated analysis runs are idempotent. GetFindings returns findings
//          for a workspace filtered to the requested file paths.
//
// Inputs:  *pgxpool.Pool.
// Outputs: staticanalysis.FindingsStore.
// Constraints: File ≤200 lines. No panic. pool must not be nil at call time.
//
// SPORT: REGISTRY-FUNCTIONS.md → staticanalysis.FindingsStore (pgx impl).
package staticanalysis

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxFindingsStore implements FindingsStore using a pgx connection pool.
type pgxFindingsStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface assertion.
var _ FindingsStore = (*pgxFindingsStore)(nil)

// NewPgxFindingsStore wraps pool in a pgxFindingsStore.
// pool must not be nil when the returned store is used.
func NewPgxFindingsStore(pool *pgxpool.Pool) FindingsStore {
	return &pgxFindingsStore{pool: pool}
}

// UpsertFindings inserts findings for workspaceID into clawde_findings.
// Rows that already exist (matched by workspace_id, rule_id, source, file_path,
// line_start) are silently skipped (ON CONFLICT DO NOTHING).
// Returns the count of rows actually inserted.
func (s *pgxFindingsStore) UpsertFindings(
	ctx context.Context,
	workspaceID string,
	findings []Finding,
) (int, error) {
	if len(findings) == 0 {
		return 0, nil
	}

	const q = `
INSERT INTO clawde_findings
  (workspace_id, rule_id, source, severity, message, file_path,
   line_start, line_end, col_start, col_end)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, rule_id, source, file_path, line_start)
DO NOTHING`

	var inserted int
	for _, f := range findings {
		tag, err := s.pool.Exec(ctx, q,
			workspaceID,
			f.RuleID,
			f.Source,
			f.Severity,
			f.Message,
			f.FilePath,
			f.LineStart,
			f.LineEnd,
			f.ColStart,
			f.ColEnd,
		)
		if err != nil {
			return inserted, fmt.Errorf("pgxFindingsStore: upsert finding %s/%s: %w",
				f.Source, f.RuleID, err)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// GetFindings returns findings for workspaceID whose file_path is in filePaths.
// Returns an empty slice (not nil) when no findings match.
func (s *pgxFindingsStore) GetFindings(
	ctx context.Context,
	workspaceID string,
	filePaths []string,
) ([]Finding, error) {
	if len(filePaths) == 0 {
		return []Finding{}, nil
	}

	// Build a parameterised ANY clause for file_path.
	const q = `
SELECT rule_id, source, severity, message, file_path,
       line_start, line_end, col_start, col_end
FROM   clawde_findings
WHERE  workspace_id = $1
  AND  file_path    = ANY($2)
ORDER  BY severity, file_path, line_start`

	rows, err := s.pool.Query(ctx, q, workspaceID, filePaths)
	if err != nil {
		return nil, fmt.Errorf("pgxFindingsStore: get findings: %w", err)
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(
			&f.RuleID,
			&f.Source,
			&f.Severity,
			&f.Message,
			&f.FilePath,
			&f.LineStart,
			&f.LineEnd,
			&f.ColStart,
			&f.ColEnd,
		); err != nil {
			return nil, fmt.Errorf("pgxFindingsStore: scan finding: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgxFindingsStore: rows error: %w", err)
	}

	if out == nil {
		out = []Finding{}
	}
	return out, nil
}
