// cmd/worker/main.go — Temporal worker for clawde-intelligence task queue.
//
// Purpose: Start a Temporal worker that processes workflows and activities on the
//          "clawde-intelligence" task queue. Run standalone or alongside the gRPC
//          server as a CS_N nSelf custom service.
//
// Usage:
//
//	go run ./cmd/worker
//	TEMPORAL_HOST_URL=temporal:7233 TEMPORAL_NAMESPACE=clawde go run ./cmd/worker
//
// Environment variables:
//
//	TEMPORAL_HOST_URL      — Temporal frontend (default localhost:7233)
//	TEMPORAL_NAMESPACE     — Temporal namespace (default "clawde")
//	CLAWDE_SANDBOX_ENABLED — set to "1" to enable execute_shell tool (off by default)
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

	"go.temporal.io/sdk/worker"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/orchestration"
	"github.com/nself-org/clawde/intelligence/internal/retrieval"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ── Temporal client ───────────────────────────────────────────────────────
	c, err := orchestration.NewTemporalClient()
	if err != nil {
		logger.Error("failed to create Temporal client", "error", err)
		os.Exit(1)
	}
	defer c.Close()

	// ── Activity dependencies ─────────────────────────────────────────────────
	// In production, replace noopKernel with retrieval.NewHybridKernel(db, cfg)
	// and noopRunner with staticanalysis.NewRunner(store, logger).
	var kernel orchestration.HybridKerneler = &noopKernel{}
	var runner orchestration.AnalysisRunner = &noopRunner{}

	acts := orchestration.NewActivities(kernel, runner, nil, nil)
	reg := orchestration.NewToolRegistry(acts)

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

// ── no-op dependencies (production wires real implementations) ────────────────

// noopKernel satisfies orchestration.HybridKerneler without a DB.
// Replace with retrieval.NewHybridKernel in production.
type noopKernel struct{}

func (noopKernel) RetrieveContext(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ []float32,
) (*retrieval.RetrievalContext, error) {
	return &retrieval.RetrievalContext{}, nil
}

// noopRunner satisfies orchestration.AnalysisRunner without external tools.
// Replace with staticanalysis.NewRunner in production.
type noopRunner struct{}

func (noopRunner) Handle(_ context.Context, _ []byte) error { return nil }
