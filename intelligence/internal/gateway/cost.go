// Package gateway — cost ledger writer.
//
// Purpose: Compute token counts for a completed LaneResponse and write a row
//          to the clawde_cost_ledger Postgres table. Cost estimate is derived
//          from the registry's cost_per_1k_tokens value for the matched entry.
// Inputs:  pgx.Conn, ProviderEntry, LaneRequest, LaneResponse, latency.
// Outputs: error; the row is inserted asynchronously-safe (caller owns the conn).
// Constraints: workspace_id isolation column (not tenant_id). Table defined in
//              migrations/0082_workspaces_cost_ledger.sql. No raw model strings here.
// SPORT: REGISTRY-FUNCTIONS.md → TrackCost.
package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CostRecord represents a single billing / audit row in clawde_cost_ledger.
type CostRecord struct {
	WorkspaceID    string
	Provider       string
	Model          string
	Lane           Lane
	UserID         string
	TokensIn       int
	TokensOut      int
	CostUSDEstimate float64
	LatencyMs      int64
	CreatedAt      time.Time
}

// TrackCost writes a cost record to clawde_cost_ledger via the supplied pgx
// connection. It derives cost_usd_estimate from the ProviderEntry's
// CostPer1kTokens field so no model-name strings are embedded here.
//
// If conn is nil the function returns nil (no-op); this allows tests and
// local-mode runs to omit a database without panicking.
func TrackCost(ctx context.Context, conn *pgx.Conn, entry ProviderEntry, req LaneRequest, resp *LaneResponse, latencyMs int64) error {
	if conn == nil {
		return nil
	}
	if resp == nil {
		return fmt.Errorf("cost: response is nil")
	}

	tokensIn, tokensOut := resolveTokenCounts(req, resp)
	totalTokens := tokensIn + tokensOut
	costUSD := float64(totalTokens) / 1000.0 * entry.CostPer1kTokens

	rec := CostRecord{
		WorkspaceID:     req.WorkspaceID,
		Provider:        entry.Provider,
		Model:           entry.Model,
		Lane:            req.Lane,
		UserID:          req.RequestID, // RequestID doubles as user-scoped key
		TokensIn:        tokensIn,
		TokensOut:       tokensOut,
		CostUSDEstimate: costUSD,
		LatencyMs:       latencyMs,
		CreatedAt:       time.Now().UTC(),
	}

	return insertCostRecord(ctx, conn, rec)
}

// resolveTokenCounts returns (input, output) token counts. The response may
// already carry counts from the provider's usage metadata. If both are zero,
// we use len(content)/4 as a rough heuristic (4 chars ≈ 1 token).
func resolveTokenCounts(req LaneRequest, resp *LaneResponse) (int, int) {
	in := resp.InputTokens
	out := resp.OutputTokens

	if in == 0 {
		// Heuristic: count prompt chars / 4.
		for _, m := range req.Messages {
			in += len(m.Content) / 4
		}
		in += len(req.SystemPrompt) / 4
		in += len(req.Text) / 4
	}
	if out == 0 {
		out = len(resp.Content) / 4
	}
	return in, out
}

// insertCostRecord executes the INSERT into clawde_cost_ledger.
func insertCostRecord(ctx context.Context, conn *pgx.Conn, r CostRecord) error {
	const q = `
INSERT INTO clawde_cost_ledger
  (workspace_id, provider, model, lane, user_id,
   tokens_in, tokens_out, cost_usd_estimate, latency_ms, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`

	_, err := conn.Exec(ctx, q,
		r.WorkspaceID, r.Provider, r.Model, string(r.Lane), r.UserID,
		r.TokensIn, r.TokensOut, r.CostUSDEstimate, r.LatencyMs, r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("cost: insert clawde_cost_ledger: %w", err)
	}
	return nil
}
