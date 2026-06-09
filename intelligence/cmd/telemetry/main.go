// cmd/telemetry — `clawde telemetry` CLI subcommands.
//
// Purpose:    Operator commands for the observability pipeline:
//               telemetry cost   — aggregate clawde_cost_ledger by provider/model/lane.
//               telemetry health — check OTLP endpoint reachability.
// Usage:
//
//	clawde telemetry cost   [--workspace <uuid>]
//	clawde telemetry health
//
// Exit codes: cost → 0 on success. health → 0 reachable, 1 unreachable/unset.
// Environment: CLAWDE_OTEL_*, CLAWDE_COST_RATES_JSON, DATABASE_URL (cost).
// SPORT: REGISTRY-SERVICES.md → clawde telemetry CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nself-org/clawde/intelligence/internal/config"
	"github.com/nself-org/clawde/intelligence/pkg/telemetry"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the subcommand and returns the process exit code.
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: clawde telemetry <cost|health> [flags]")
		return 2
	}
	switch args[0] {
	case "cost":
		return cmdCost(args[1:])
	case "health":
		return cmdHealth()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q (want cost|health)\n", args[0])
		return 2
	}
}

// cmdCost prints aggregated cost rows from clawde_cost_ledger.
func cmdCost(args []string) int {
	workspace := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--workspace" && i+1 < len(args) {
			workspace = args[i+1]
			i++
		}
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL not set")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	rows, err := telemetry.CostSummary(ctx, conn, workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		return 1
	}
	fmt.Printf("%-14s %-28s %-10s %8s %12s %12s %14s\n",
		"PROVIDER", "MODEL", "LANE", "CALLS", "TOKENS_IN", "TOKENS_OUT", "COST_USD")
	var total float64
	for _, r := range rows {
		fmt.Printf("%-14s %-28s %-10s %8d %12d %12d %14.6f\n",
			r.Provider, r.Model, r.Lane, r.Calls, r.TokensIn, r.TokensOut, r.CostUSD)
		total += r.CostUSD
	}
	fmt.Printf("%-14s %-28s %-10s %8s %12s %12s %14.6f\n", "TOTAL", "", "", "", "", "", total)
	return 0
}

// cmdHealth checks OTLP endpoint reachability. Exit 0 reachable, 1 otherwise.
func cmdHealth() int {
	cfg := config.LoadTelemetryConfig()
	if !cfg.OTLPEnabled() {
		fmt.Fprintln(os.Stderr, "CLAWDE_OTEL_ENDPOINT not set — telemetry disabled (no-op)")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shutdown, err := telemetry.InitTelemetry(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		return 1
	}
	defer shutdown(ctx)

	if err := telemetry.CheckEndpoint(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "unreachable %s: %v\n", cfg.OTLPEndpoint, err)
		return 1
	}
	fmt.Printf("OK: %s reachable (%s)\n", cfg.OTLPEndpoint, cfg.OTLPProtocol)
	return 0
}
