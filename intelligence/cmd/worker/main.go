// cmd/worker/main.go — Temporal worker for clawde-intelligence task queue.
//
// Purpose: Start a Temporal worker that processes workflows and activities on the
//          "clawde-intelligence" task queue. Run standalone or alongside the gRPC
//          server as a CS_N nSelf custom service.
//
// Usage:
//
//	go run ./cmd/worker
//	TEMPORAL_HOST_URL=temporal:7233 TEMPORAL_NAMESPACE=clawde CLAWDE_PG_DSN=... go run ./cmd/worker
//
// Environment variables:
//
//	TEMPORAL_HOST_URL      — Temporal frontend (default localhost:7233)
//	TEMPORAL_NAMESPACE     — Temporal namespace (default "clawde")
//	CLAWDE_PG_DSN          — Postgres DSN (required; fatal if unset)
//	CLAWDE_SANDBOX_ENABLED — set to "1" to enable execute_shell tool (off by default)
//	CLAWDE_GRPC_ADDR       — host:port of the clawde-intelligence gRPC server for LLM
//	                         calls (default localhost:8090). When unset or unreachable,
//	                         LLMCallActivity falls back to stub mode with a log warning.
//
// nSelf deployment: add to docker-compose via `nself build` as CS_N.
// Do NOT hand-edit docker-compose.yml — see nSelf-First Doctrine (PPI).
//
// SPORT: REGISTRY-SERVICES.md → Temporal CS_N, orchestration.NewWorker.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nself-org/clawde/intelligence/internal/gateway"
	"github.com/nself-org/clawde/intelligence/internal/orchestration"
	"github.com/nself-org/clawde/intelligence/internal/pty"
	"github.com/nself-org/clawde/intelligence/internal/retrieval"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
	"github.com/nself-org/clawde/intelligence/internal/sandbox"
	"github.com/nself-org/clawde/intelligence/internal/staticanalysis"
)

func main() {
	// MUST be first: on Linux+seccomp builds, this detects CLAWDE_SECCOMP_INIT=1
	// (set by seccompExecutor.Execute) and installs the BPF filter then exec's
	// the real command. On all other builds/platforms this is a no-op.
	sandbox.MaybeRunSeccompShim()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ── Postgres pool (required — HybridKernel cannot operate without DB) ─────
	pgDSN := os.Getenv("CLAWDE_PG_DSN")
	if pgDSN == "" {
		logger.Error("CLAWDE_PG_DSN is required but not set; cannot start Temporal worker")
		os.Exit(1)
	}
	pgPool, err := pgxpool.New(context.Background(), pgDSN)
	if err != nil {
		logger.Error("failed to create pgx pool", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	// ── Temporal client ───────────────────────────────────────────────────────
	c, err := orchestration.NewTemporalClient()
	if err != nil {
		logger.Error("failed to create Temporal client", "error", err)
		os.Exit(1)
	}
	defer c.Close()

	// ── Activity dependencies ─────────────────────────────────────────────────
	dbQuerier := lanes.NewPgxAdapter(pgPool)
	kernel := retrieval.NewHybridKernel(dbQuerier, retrieval.HybridConfig{})

	findingsStore := staticanalysis.NewPgxFindingsStore(pgPool)
	runner := staticanalysis.NewRunner(findingsStore, logger)

	// ── GatewayClient for LLM calls (LLMCallActivity) ────────────────────────
	// Connect to the clawde-intelligence gRPC server (same process or sidecar).
	// When CLAWDE_GRPC_ADDR is unset, gwClient stays nil and LLMCallActivity
	// falls back to stub mode with a log.Warn — no worker startup failure.
	var gwClient orchestration.GatewayClient
	if grpcAddr := os.Getenv("CLAWDE_GRPC_ADDR"); grpcAddr != "" {
		grpcConn, grpcErr := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if grpcErr != nil {
			logger.Warn("CLAWDE_GRPC_ADDR dial failed; LLMCallActivity will run in stub mode",
				"addr", grpcAddr, "error", grpcErr)
		} else {
			defer grpcConn.Close()
			gwClient = gateway.NewGRPCGatewayClient(grpcConn)
		}
	} else {
		logger.Warn("CLAWDE_GRPC_ADDR not set; LLMCallActivity will run in stub mode (set to host:port to enable real LLM calls)")
	}

	acts := orchestration.NewActivities(kernel, runner, nil, nil, gwClient)
	reg := orchestration.NewToolRegistry(acts)
	// Wire the registry back into Activities so ToolDispatchActivity can
	// perform real registry-backed dispatch in the agent loop.
	orchestration.WithToolRegistry(acts, reg)

	// Wire the PTY pool when sandbox is enabled.
	// The pool pre-warms CLAWDE_PTY_POOL_SIZE (default 4) /bin/sh slots so
	// ExecuteShellActivity can route through them without per-call fork overhead.
	if os.Getenv("CLAWDE_SANDBOX_ENABLED") == "1" {
		pool := pty.NewPool(pty.PoolSizeFromEnv(), 0, logger)
		if err := pool.Start(); err != nil {
			logger.Warn("PTY pool failed to start; ExecuteShellActivity falls back to sandbox.NewDefault",
				"error", err)
		} else {
			orchestration.WithPTYPool(acts, pool)
			defer pool.Stop()
			logger.Info("PTY pool started", "size", pty.PoolSizeFromEnv())
		}
	}

	// ── Worker ────────────────────────────────────────────────────────────────
	w := orchestration.NewWorker(c, acts, reg, worker.Options{})
	if err := w.Start(); err != nil {
		logger.Error("failed to start Temporal worker", "error", err)
		os.Exit(1)
	}
	logger.Info("Temporal worker started",
		"task_queue", orchestration.TaskQueue,
		"namespace", orchestration.DefaultNamespace,
	)

	// Block until SIGINT or SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down Temporal worker")
	w.Stop()
}
