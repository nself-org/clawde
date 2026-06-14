// Command server — clawde-intelligence gRPC + REST gateway server.
//
// Purpose: Start the gRPC server on 127.0.0.1:8090 (loopback, always) and
//          optionally on a Tailscale mesh address as well when
//          TAILSCALE_AUTHKEY is set.  The REST gateway starts on 127.0.0.1:8091.
//          When CLAWDE_PG_DSN is set, the pgmq worker Pool is also started to
//          drain the embed, analyze, and ingest queues.
//
// Env vars consumed:
//
//	TAILSCALE_AUTHKEY          — auth key from the Tailscale admin console;
//	                             empty (unset) → Tailscale skipped entirely.
//	CLAWDE_TAILSCALE_HOSTNAME  — node hostname on the tailnet;
//	                             default "clawde-intelligence".
//	CLAWDE_ENV                 — "production" disables gRPC reflection.
//	CLAWDE_HMAC_SECRET         — HMAC-SHA256 secret for auth (see server.HMACSecret).
//	CLAWDE_PG_DSN              — Postgres connection string (postgres://…);
//	                             empty (unset) → worker pool skipped (non-fatal).
//
// Tailscale ACL (configure in tailnet admin console):
//
//	tag:clawde-client → tag:clawde-intelligence : tcp 8090
//
// Dual-listener behaviour:
//   - 127.0.0.1:8090 is ALWAYS started (existing behaviour, unchanged).
//   - When TAILSCALE_AUTHKEY is non-empty and Tailscale Up succeeds, a second
//     gRPC listener is started on the Tailscale interface (:8090).
//     Both listeners share the same *grpc.Server instance.
//   - If Tailscale Up fails (timeout, network error, bad key), a warning is
//     logged and the server continues with loopback-only (no crash).
//
// Worker pool behaviour:
//   - When CLAWDE_PG_DSN is set: a pgxpool is created, pgxQueueStore wired,
//     and Pool.Start() called in a goroutine.
//   - When CLAWDE_PG_DSN is unset: logs "worker pool skipped: CLAWDE_PG_DSN not set"
//     and continues (non-fatal).
//   - Pool.Stop() is called in the shutdown path before gRPC GracefulStop.
//
// SPORT: REGISTRY-SERVICES.md — clawde-intelligence, Tailscale mesh, clawde-worker-pool.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nself-org/clawde/intelligence/internal/compiler"
	gw "github.com/nself-org/clawde/intelligence/internal/gateway"
	"github.com/nself-org/clawde/intelligence/internal/networking"
	"github.com/nself-org/clawde/intelligence/internal/pty"
	"github.com/nself-org/clawde/intelligence/internal/server"
	"github.com/nself-org/clawde/intelligence/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ── Provider registry ─────────────────────────────────────────────────────
	// Providers are injected at deploy time via model_registry.yaml.
	// An empty slice starts the server in passthrough mode (health endpoint only).
	providers := []gw.Provider{}

	// ── Auto-context compiler ─────────────────────────────────────────────────
	// Wire a non-nil Compiler so CompileContext returns real enriched context.
	// TODO: wire retrieval.NewHybridKernel via DB pool (depends on T03 database wiring).
	// For now: use a no-op retriever that satisfies the compiler.ContextRetriever
	// interface and returns an empty result; symbols and policy are nil (both have
	// nil-safe paths in compiler.NewCompiler per compiler.go:77-88).
	noopRetriever := &noopContextRetriever{}
	clawdeCompiler := compiler.NewCompiler(noopRetriever, nil, nil)

	// ── gRPC + REST server ────────────────────────────────────────────────────
	cfg, err := server.DefaultConfig(providers)
	if err != nil {
		logger.Error("server config failed", "error", err)
		os.Exit(1)
	}
	cfg.Compiler = clawdeCompiler

	srv := server.New(*cfg)
	if err := srv.Start(); err != nil {
		logger.Error("server start failed", "error", err)
		os.Exit(1)
	}
	logger.Info("clawde-intelligence listening",
		"grpc", cfg.GRPCAddr,
		"rest", cfg.RESTAddr,
	)

	// ── PTY pool (optional — requires CLAWDE_SANDBOX_ENABLED=1) ─────────────
	// When CLAWDE_SANDBOX_ENABLED=1: start a pre-warmed pool of N PTY slots
	// (size from CLAWDE_PTY_POOL_SIZE, default 4) for use by ExecuteShellActivity.
	// When disabled or unset: log and skip — no PTY FDs opened.
	// Pool.Stop() is called in the shutdown path before gRPC GracefulStop.
	var ptyPool *pty.Pool
	if os.Getenv("CLAWDE_SANDBOX_ENABLED") == "1" {
		poolSize := pty.PoolSizeFromEnv()
		ptyPool = pty.NewPool(poolSize, 0, logger)
		if startErr := ptyPool.Start(); startErr != nil {
			logger.Warn("PTY pool start failed — sandbox will fall back to per-call executor",
				"error", startErr)
			ptyPool = nil
		} else {
			logger.Info("PTY pool started", "size", poolSize)
		}
	} else {
		logger.Info("PTY pool skipped: CLAWDE_SANDBOX_ENABLED not set")
	}
	_ = ptyPool // ptyPool wired into Activities via WithPTYPool when Temporal worker is added

	// ── pgmq worker pool (optional — requires CLAWDE_PG_DSN) ─────────────────
	// When DSN is set: connect, wire the QueueStore, register handlers, start pool.
	// When DSN is unset: log a warning and continue — not fatal.
	// Pool.Stop() is deferred to the shutdown path (before gRPC GracefulStop).
	var workerPool *worker.Pool
	pgDSN := os.Getenv("CLAWDE_PG_DSN")
	if pgDSN == "" {
		logger.Warn("worker pool skipped: CLAWDE_PG_DSN not set")
	} else {
		pgPool, pgErr := pgxpool.New(context.Background(), pgDSN)
		if pgErr != nil {
			logger.Warn("worker pool skipped: failed to connect to postgres",
				"error", pgErr)
		} else {
			store := worker.NewPgxQueueStore(pgPool)
			handlers := worker.DefaultHandlers(nil, nil, logger)
			workerPool = worker.New(worker.Config{
				Store:    store,
				Handlers: handlers,
				Logger:   logger,
			})
			workerPool.Start(context.Background())
			logger.Info("worker pool started",
				"queues", []string{"clawde_embed_queue", "clawde_analyze_queue", "clawde_ingest_queue"},
			)
			// pgPool is intentionally not closed here; it is closed when the
			// program exits (OS reclaims connections). Pool.Stop() drains workers first.
		}
	}

	// ── Tailscale mesh listener (optional) ────────────────────────────────────
	// When TAILSCALE_AUTHKEY is unset: skip, loopback-only (existing behaviour).
	// When set: bring up tsnet, attach a second gRPC listener on the Tailscale
	// interface so tag:clawde-client nodes can reach port 8090 over the mesh.
	// Failure at any Tailscale step is non-fatal: warn + continue loopback-only.
	authKey := os.Getenv("TAILSCALE_AUTHKEY")
	hostname := envOr("CLAWDE_TAILSCALE_HOSTNAME", "clawde-intelligence")

	var tsCloser interface{ Close() error }

	if authKey == "" {
		logger.Info("TAILSCALE_AUTHKEY unset — loopback-only mode")
	} else {
		result, tsErr := networking.InitTailscale(context.Background(), networking.InitConfig{
			Hostname: hostname,
			AuthKey:  authKey,
		})
		switch {
		case tsErr != nil:
			logger.Warn("Tailscale init failed — continuing with loopback only",
				"error", tsErr)
		case result == nil:
			// Should not happen when authKey != "", but guard anyway.
			logger.Warn("Tailscale returned nil result — continuing with loopback only")
		default:
			logger.Info("Tailscale ready", "ip", result.TailscaleIP)
			tsCloser = result.Server

			tsLis, lisErr := networking.ListenTCP(result.Server, ":8090")
			if lisErr != nil {
				logger.Warn("Tailscale TCP listen failed — continuing with loopback only",
					"error", lisErr)
				_ = result.Server.Close()
				tsCloser = nil
			} else {
				logger.Info("Tailscale gRPC listener active",
					"addr", tsLis.Addr().String())
				serveOnListener(srv.GRPCServer(), tsLis, logger)
			}
		}
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	sig := <-quit
	logger.Info("shutting down", "signal", sig.String())

	// Stop worker pool first — drains in-flight jobs before closing the DB and
	// gRPC connections. Per CR-C guidance: pool.Stop() before grpcSrv.Stop().
	if workerPool != nil {
		workerPool.Stop()
		logger.Info("worker pool stopped")
	}

	// Stop PTY pool — kills all pre-warmed slots before server shutdown.
	if ptyPool != nil {
		ptyPool.Stop()
		logger.Info("PTY pool stopped")
	}

	srv.Shutdown(context.Background())
	if tsCloser != nil {
		_ = tsCloser.Close()
	}
}

// serveOnListener starts grpcSrv.Serve on the provided listener in a goroutine.
// A nil grpcSrv is a no-op (guarded to avoid panic before Start() is called).
func serveOnListener(grpcSrv interface {
	Serve(net.Listener) error
}, lis net.Listener, logger *slog.Logger) {
	if grpcSrv == nil {
		logger.Warn("grpcSrv nil — skipping extra listener")
		_ = lis.Close()
		return
	}
	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			logger.Warn("secondary gRPC listener stopped", "error", err)
		}
	}()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// noopContextRetriever satisfies the compiler.ContextRetriever interface with
// an empty-result implementation. Replace with retrieval.NewHybridKernel once
// the DB pool is wired (TODO: T03 database wiring).
type noopContextRetriever struct{}

func (n *noopContextRetriever) RetrieveContext(_ context.Context, _, _ string) (*compiler.RetrievalResult, error) {
	return &compiler.RetrievalResult{}, nil
}
