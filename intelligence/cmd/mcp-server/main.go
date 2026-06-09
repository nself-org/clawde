// Command mcp-server — Claude Code MCP stdio tool server for clawde-intelligence.
//
// Purpose:    Entrypoint that runs the MCP 0.7 stdio JSON-RPC loop over
//             os.Stdin/os.Stdout. NO TCP port is opened (ADR-001 local-only).
//             clawd resolves the client identity (CLAWDE_MCP_CLIENT_ID) and the
//             active workspace (CLAWDE_WORKSPACE_ID); HMAC to clawde-intelligence
//             is configured downstream via CLAWDE_GATEWAY_HMAC_SECRET.
// Inputs:     env CLAWDE_MCP_CLIENT_ID, CLAWDE_WORKSPACE_ID, CLAWDE_GRPC_ADDR.
// Outputs:    JSON-RPC 2.0 responses on stdout; audit/log lines on stderr.
// Constraints: stdio only — never net.Listen. ContextSource defaults to nil
//              (graceful degradation) until wired to the live gRPC client.
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence MCP stdio tool server.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/nself-org/clawde/intelligence/internal/hostadapter"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := hostadapter.AdapterConfig{
		GRPCAddr:    envOr("CLAWDE_GRPC_ADDR", "127.0.0.1:8090"),
		WorkspaceID: os.Getenv("CLAWDE_WORKSPACE_ID"),
	}

	// Production wiring connects a gRPC-backed ContextSource here; nil source
	// degrades gracefully (ADR-001) rather than crashing the stdio loop.
	adapter := hostadapter.NewClaudeCodeAdapter(nil, logger)
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
