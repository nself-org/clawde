// Command server — clawde-intelligence gRPC + REST gateway server.
//
// Purpose: Start the gRPC server on 127.0.0.1:8090 (loopback, always) and
//          optionally on a Tailscale mesh address as well when
//          TAILSCALE_AUTHKEY is set.  The REST gateway starts on 127.0.0.1:8091.
//
// Env vars consumed:
//
//	TAILSCALE_AUTHKEY          — auth key from the Tailscale admin console;
//	                             empty (unset) → Tailscale skipped entirely.
//	CLAWDE_TAILSCALE_HOSTNAME  — node hostname on the tailnet;
//	                             default "clawde-intelligence".
//	CLAWDE_ENV                 — "production" disables gRPC reflection.
//	CLAWDE_HMAC_SECRET         — HMAC-SHA256 secret for auth (see server.HMACSecret).
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
// SPORT: REGISTRY-SERVICES.md — clawde-intelligence, Tailscale mesh.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	gw "github.com/nself-org/clawde/intelligence/internal/gateway"
	"github.com/nself-org/clawde/intelligence/internal/networking"
	"github.com/nself-org/clawde/intelligence/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ── Provider registry ─────────────────────────────────────────────────────
	// Providers are injected at deploy time via model_registry.yaml.
	// An empty slice starts the server in passthrough mode (health endpoint only).
	providers := []gw.Provider{}

	// ── gRPC + REST server ────────────────────────────────────────────────────
	cfg, err := server.DefaultConfig(providers)
	if err != nil {
		logger.Error("server config failed", "error", err)
		os.Exit(1)
	}

	srv := server.New(*cfg)
	if err := srv.Start(); err != nil {
		logger.Error("server start failed", "error", err)
		os.Exit(1)
	}
	logger.Info("clawde-intelligence listening",
		"grpc", cfg.GRPCAddr,
		"rest", cfg.RESTAddr,
	)

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
