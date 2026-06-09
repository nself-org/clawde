// Package auth — per-workspace daily quota enforcement via clawde_quota table.
//
// Purpose: Enforce daily request limits based on the subscription tier encoded
//          in the JWT (clawde/tier claim). Limits:
//            free       → 100  requests/day
//            pro        → 10000 requests/day
//            enterprise → unlimited (-1)
//          Window resets at midnight UTC (stored as window_start timestamptz).
//
// Inputs:  workspace ID string (from resolved Workspace), Tier.
// Outputs: error wrapping ErrQuotaExceeded when limit reached; nil on success.
// Constraints: Upserts on quota row (INSERT … ON CONFLICT DO UPDATE).
//              Thread-safe via DB-level advisory or optimistic count.
//              Never auto-decrements; count is monotonically increasing per window.
// SPORT: REGISTRY-FUNCTIONS.md — QuotaEnforcer, CheckAndIncrement.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrQuotaExceeded is returned when the workspace has reached its daily limit.
// Callers should map this to HTTP 429 / gRPC ResourceExhausted.
var ErrQuotaExceeded = errors.New("daily quota exceeded")

// QuotaEnforcer checks and increments the daily request count for a workspace.
type QuotaEnforcer struct {
	pool *pgxpool.Pool
}

// NewQuotaEnforcer creates a QuotaEnforcer backed by the given pool.
func NewQuotaEnforcer(pool *pgxpool.Pool) *QuotaEnforcer {
	return &QuotaEnforcer{pool: pool}
}

// CheckAndIncrement atomically checks and increments the daily request count
// for workspaceID. Returns ErrQuotaExceeded if the workspace has reached its
// tier limit. Enterprise tier (-1) always passes.
//
// The quota row is upserted: if no row exists, one is created with count=1.
// If the current window_start is before today UTC, the counter resets.
func (e *QuotaEnforcer) CheckAndIncrement(ctx context.Context, workspaceID string, tier Tier) error {
	limit := tier.DailyLimit()
	if limit < 0 {
		// Enterprise: unlimited — skip DB round-trip.
		return nil
	}

	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("quota: acquire conn: %w", err)
	}
	defer conn.Release()

	todayUTC := todayMidnightUTC()

	// Upsert: insert or update the quota row, resetting count when the window
	// has rolled over to a new day. Returns the new count after increment.
	var newCount int
	err = conn.QueryRow(ctx, `
		INSERT INTO clawde_quota (workspace_id, tier, daily_count, window_start)
		VALUES ($1, $2, 1, $3)
		ON CONFLICT (workspace_id) DO UPDATE SET
			daily_count  = CASE
				WHEN clawde_quota.window_start < $3
				THEN 1
				ELSE clawde_quota.daily_count + 1
			END,
			window_start = CASE
				WHEN clawde_quota.window_start < $3
				THEN $3
				ELSE clawde_quota.window_start
			END,
			tier = $2
		RETURNING daily_count
	`, workspaceID, string(tier), todayUTC).Scan(&newCount)
	if err != nil {
		return fmt.Errorf("quota: upsert: %w", err)
	}

	if newCount > limit {
		return ErrQuotaExceeded
	}
	return nil
}

func todayMidnightUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
