// pgx_adapter.go — pgxpool.Pool adapter implementing lanes.DBQuerier.
//
// Purpose: Bridge pgxpool.Pool (which has incompatible Exec and Query signatures)
//          to the lanes.DBQuerier interface used throughout the retrieval package.
//          Allows production code to pass a *pgxpool.Pool wherever DBQuerier is
//          accepted without importing pgx in the caller.
//
// Inputs:  *pgxpool.Pool.
// Outputs: lanes.DBQuerier.
// Constraints: File ≤100 lines. No panic. Pool must not be nil at call time.
//
// SPORT: REGISTRY-FUNCTIONS.md → lanes.PgxAdapter.
package lanes

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxAdapter adapts *pgxpool.Pool to the DBQuerier interface.
//
// pgxpool.Pool.Exec returns (pgconn.CommandTag, error); DBQuerier.Exec returns
// only error — the adapter discards the CommandTag.
// pgxpool.Pool.Query returns (pgx.Rows, error); pgx.Rows satisfies lanes.Rows.
type PgxAdapter struct {
	pool *pgxpool.Pool
}

// Compile-time interface assertion.
var _ DBQuerier = (*PgxAdapter)(nil)

// NewPgxAdapter wraps pool in a PgxAdapter.
// pool must not be nil when the returned adapter is used.
func NewPgxAdapter(pool *pgxpool.Pool) *PgxAdapter {
	return &PgxAdapter{pool: pool}
}

// Query executes sql with args and returns iterable pgx.Rows as lanes.Rows.
func (a *PgxAdapter) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := a.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRowsAdapter{rows: rows}, nil
}

// Exec executes a statement and discards the pgconn.CommandTag.
func (a *PgxAdapter) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := a.pool.Exec(ctx, sql, args...)
	return err
}

// pgxRowsAdapter wraps pgx.Rows to satisfy lanes.Rows.
type pgxRowsAdapter struct {
	rows pgx.Rows
}

func (r *pgxRowsAdapter) Next() bool          { return r.rows.Next() }
func (r *pgxRowsAdapter) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *pgxRowsAdapter) Close()               { r.rows.Close() }
func (r *pgxRowsAdapter) Err() error           { return r.rows.Err() }
