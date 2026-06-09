// Package telemetry — cost recording into clawde_cost_ledger.
//
// Purpose:    RecordCost computes USD cost from CLAWDE_COST_RATES_JSON rates and
//             writes a row to clawde_cost_ledger (the canonical token-usage audit
//             table from migration 0082). It also emits the cost/token OTel
//             metrics. CostSummary aggregates ledger rows for the CLI.
// Inputs:     pgx.Conn (nil = metrics-only, no DB write), CostEvent.
// Outputs:    computed cost_usd; error on DB failure (nil conn → no error).
// Constraints: REUSES clawde_cost_ledger — no new migration. Telemetry needs no
//             columns the ledger lacks (provider, model, lane, tokens_in/out,
//             cost_usd_estimate, latency_ms all present). Metadata only, no PII.
// SPORT:      REGISTRY-FUNCTIONS.md → RecordCost.
package telemetry

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/nself-org/clawde/intelligence/internal/config"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// CostEvent describes a single LLM call's billing metadata. No prompt/completion
// content is carried — metadata only, per the cost-ledger PII contract.
type CostEvent struct {
	WorkspaceID string
	Provider    string
	Model       string
	Lane        string
	UserID      string
	TokensIn    int
	TokensOut   int
	LatencyMs   int64
}

// CostSummaryRow is one aggregated cost line returned by CostSummary.
type CostSummaryRow struct {
	Provider   string
	Model      string
	Lane       string
	Calls      int64
	TokensIn   int64
	TokensOut  int64
	CostUSD    float64
}

// RecordCost derives the USD cost from the rate table (keyed "provider/model"),
// writes a clawde_cost_ledger row (skipped when conn is nil), and emits the
// gateway cost + token OTel metrics. Returns the computed cost and any DB error.
func RecordCost(ctx context.Context, conn *pgx.Conn, rates map[string]config.CostRate, ev CostEvent) (float64, error) {
	cost := ComputeCost(rates, ev.Provider, ev.Model, ev.TokensIn, ev.TokensOut)

	// Emit metrics (no-op safe when instruments are nil).
	m := GetMetrics()
	attrs := metric.WithAttributes(
		attribute.String("provider", ev.Provider),
		attribute.String("model", ev.Model),
		attribute.String("lane", ev.Lane),
	)
	if m.GatewayCost != nil {
		m.GatewayCost.Add(ctx, cost, attrs)
	}
	if m.GatewayTokens != nil {
		m.GatewayTokens.Add(ctx, int64(ev.TokensIn+ev.TokensOut), attrs)
	}

	if conn == nil {
		return cost, nil
	}

	const q = `
		INSERT INTO clawde_cost_ledger
		  (workspace_id, provider, model, lane, user_id,
		   tokens_in, tokens_out, cost_usd_estimate, latency_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := conn.Exec(ctx, q,
		ev.WorkspaceID, ev.Provider, ev.Model, ev.Lane, ev.UserID,
		ev.TokensIn, ev.TokensOut, cost, ev.LatencyMs,
	)
	return cost, err
}

// ComputeCost returns the USD cost for a call given the per-1k-token rate table.
// An unknown provider/model key resolves to a zero rate (cost 0) — graceful
// degradation when CLAWDE_COST_RATES_JSON is incomplete.
func ComputeCost(rates map[string]config.CostRate, provider, model string, tokensIn, tokensOut int) float64 {
	rate := rates[provider+"/"+model]
	return (float64(tokensIn)/1000.0)*rate.InputPer1k +
		(float64(tokensOut)/1000.0)*rate.OutputPer1k
}

// CostSummary aggregates clawde_cost_ledger over an optional workspace filter,
// grouped by provider/model/lane. A nil conn returns an empty slice (no error)
// so the CLI degrades cleanly without a database.
func CostSummary(ctx context.Context, conn *pgx.Conn, workspaceID string) ([]CostSummaryRow, error) {
	if conn == nil {
		return nil, nil
	}
	q := `
		SELECT provider, model, lane,
		       COUNT(*)                       AS calls,
		       COALESCE(SUM(tokens_in), 0)    AS tokens_in,
		       COALESCE(SUM(tokens_out), 0)   AS tokens_out,
		       COALESCE(SUM(cost_usd_estimate), 0) AS cost_usd
		FROM clawde_cost_ledger`
	args := []any{}
	if workspaceID != "" {
		q += ` WHERE workspace_id = $1`
		args = append(args, workspaceID)
	}
	q += ` GROUP BY provider, model, lane ORDER BY cost_usd DESC`

	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CostSummaryRow
	for rows.Next() {
		var r CostSummaryRow
		if err := rows.Scan(&r.Provider, &r.Model, &r.Lane,
			&r.Calls, &r.TokensIn, &r.TokensOut, &r.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
