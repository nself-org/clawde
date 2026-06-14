// Command mcp-server — Claude Code MCP stdio tool server for clawde-intelligence.
//
// Purpose:    Entrypoint that runs the MCP 0.7 stdio JSON-RPC loop over
//             os.Stdin/os.Stdout. NO TCP port is opened (ADR-001 local-only).
//             clawd resolves the client identity (CLAWDE_MCP_CLIENT_ID) and the
//             active workspace (CLAWDE_WORKSPACE_ID); HMAC to clawde-intelligence
//             is configured downstream via CLAWDE_GATEWAY_HMAC_SECRET.
// Inputs:     env CLAWDE_MCP_CLIENT_ID, CLAWDE_WORKSPACE_ID, CLAWDE_GRPC_ADDR.
// Outputs:    JSON-RPC 2.0 responses on stdout; audit/log lines on stderr.
// Constraints: stdio only — never net.Listen. When CLAWDE_GRPC_ADDR is
//              unreachable, adapter degrades gracefully (ADR-001): log warning,
//              pass nil ContextSource, never crash the stdio loop.
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence MCP stdio tool server.
package main

import (
	"context"
	"log/slog"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nself-org/clawde/intelligence/internal/hostadapter"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	grpcAddr := envOr("CLAWDE_GRPC_ADDR", "127.0.0.1:8090")
	cfg := hostadapter.AdapterConfig{
		GRPCAddr:    grpcAddr,
		WorkspaceID: os.Getenv("CLAWDE_WORKSPACE_ID"),
	}

	// Wire a gRPC-backed ContextSource. Insecure transport is intentional:
	// loopback 127.0.0.1 only (ADR-001). On dial failure, degrade gracefully
	// — log a warning and pass nil, never crash the stdio loop.
	var source hostadapter.ContextSource
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Warn("mcp-server: gRPC dial failed, running without context enrichment",
			"addr", grpcAddr, "error", err)
	} else {
		seam := hostadapter.NewGRPCKernelSeam(conn)
		source = hostadapter.NewGRPCSource(seam)
	}

	adapter := hostadapter.NewClaudeCodeAdapter(source, nil, logger)
	if err := adapter.Install(context.Background(), cfg); err != nil {
		logger.Error("mcp-server install failed", "error", err)
		os.Exit(1)
	}

	clientID := envOr("CLAWDE_MCP_CLIENT_ID", "")
	srv := hostadapter.NewMCPServer(adapter, clientID)

	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		logger.Error("mcp-server stdio loop exited", "error", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
