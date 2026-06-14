// Package hostadapter — Claude Code MCP adapter (stdio JSON-RPC 2.0).
//
// Purpose:    ClaudeCodeAdapter implements HostAdapter for the Claude Code (CC)
//             host, and MCPServer exposes clawde-intelligence as an MCP 0.7
//             stdio tool server. Transport is stdio ONLY — no TCP port is
//             opened (ADR-001 local-only for the clawd side). Every tool call
//             runs the ADR-003 deny-by-default dispatch chain:
//             auth → trust_registry → PolicyEngine.evaluate → SupplyChainPolicy.
// Inputs:     ContextSource (gRPC client or in-process mock), AdapterConfig,
//             os.Stdin / os.Stdout (or any io.Reader/io.Writer in tests).
// Outputs:    HookResult per lifecycle hook; JSON-RPC 2.0 responses for
//             initialize / tools/list / tools/call.
// Constraints: EXACTLY the 6 HostAdapter methods are reused from adapter.go
//              (HostAdapter is NOT redefined here). stdio only, never net.Listen.
//              Tool responses validated before return; malformed downstream
//              response → MCP error. File ≤500 lines. Stdlib only.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.ClaudeCodeAdapter, hostadapter.MCPServer.
//        REGISTRY-SERVICES.md → clawde-intelligence MCP stdio tool server.
package hostadapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// ccHostName is the stable identifier Claude Code emits in audit logs.
const ccHostName = "claude-code"

// MCP protocol constants (MCP 0.7 over JSON-RPC 2.0).
const (
	jsonRPCVersion = "2.0"
	mcpProtocol    = "2024-11-05"
	mcpServerName  = "clawde-intelligence"
	mcpServerVer   = "0.7.0"
)

// JSON-RPC 2.0 standard error codes plus the deny-by-default app code.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
	rpcAccessDenied   = -32000 // ADR-003 deny-by-default rejection.
)

// ── JSON-RPC 2.0 envelopes ───────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── ClaudeCodeAdapter — HostAdapter (reuses the 6-method interface verbatim) ──

// ClaudeCodeAdapter is the Claude Code host integration. It reuses the pinned
// HostAdapter contract from adapter.go (Name, Install, Uninstall, SessionStart,
// SessionEnd, HealthCheck) — no method is added.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.ClaudeCodeAdapter.
type ClaudeCodeAdapter struct {
	source   ContextSource
	compiler ContextCompiler
	cfg      AdapterConfig
	logger   *slog.Logger
	now      func() time.Time
}

// NewClaudeCodeAdapter constructs a ClaudeCodeAdapter over the given source and
// an optional compiler hook (nil is safe — graceful-degrade per ADR-001). In
// production pass NewCompilerHook(comp); tests may inject any ContextCompiler
// mock. A nil logger falls back to a stderr JSON logger (audit records never
// dropped).
func NewClaudeCodeAdapter(source ContextSource, comp ContextCompiler, logger *slog.Logger) *ClaudeCodeAdapter {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return &ClaudeCodeAdapter{source: source, compiler: comp, logger: logger, now: time.Now}
}

// Name implements HostAdapter. Returns "claude-code".
func (a *ClaudeCodeAdapter) Name() string { return ccHostName }

// Install implements HostAdapter. Records config (defaulting the gRPC addr).
func (a *ClaudeCodeAdapter) Install(ctx context.Context, cfg AdapterConfig) error {
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:8090"
	}
	a.cfg = cfg
	a.logger.InfoContext(ctx, "claude-code adapter installed",
		"host", ccHostName, "grpc_addr", cfg.GRPCAddr)
	return nil
}

// Uninstall implements HostAdapter. Removes host-side wiring (no-op seam).
func (a *ClaudeCodeAdapter) Uninstall(ctx context.Context) error {
	a.logger.InfoContext(ctx, "claude-code adapter uninstalled", "host", ccHostName)
	return nil
}

// SessionStart implements HostAdapter. Pre-warms the context cache via
// compiler.SessionStart (2s deadline, ADR-001 graceful-degrade: error logged,
// never returned), then fetches context and emits an audit line.
// Retrieval failure degrades gracefully (ADR-001): Enriched:false + warning.
func (a *ClaudeCodeAdapter) SessionStart(ctx context.Context, event HookEvent) (HookResult, error) {
	// Pre-warm the context cache so the first IDE prompt gets enriched context
	// immediately. Runs under a hard 2s deadline; errors are swallowed (log+continue).
	if a.compiler != nil {
		sCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if enriched := a.compiler.PreWarmSession(sCtx, a.workspaceFor(event)); !enriched {
			a.logger.WarnContext(ctx, "claude-code compiler.SessionStart unenriched",
				"host", ccHostName, "session_id", event.SessionID)
		}
	}

	start := a.now()
	ws := a.workspaceFor(event)
	rc, err := a.retrieve(ctx, ws, event.SessionID)
	latency := a.elapsedMs(start)
	if err != nil {
		warn := fmt.Sprintf("clawde context unavailable: %v", err)
		a.audit(ctx, event, "session_start", false, latency)
		a.logger.WarnContext(ctx, "claude-code session_start degraded",
			"host", ccHostName, "session_id", event.SessionID, "error", warn)
		return HookResult{Enriched: false, LatencyMs: latency, Error: warn}, nil
	}
	tokens := estimateTokens(formatContextBlock(rc))
	a.audit(ctx, event, "session_start", true, latency)
	a.logger.InfoContext(ctx, "claude-code context injected",
		"host", ccHostName, "session_id", event.SessionID, "injected_tokens", tokens)
	return HookResult{Enriched: true, LatencyMs: latency}, nil
}

// SessionEnd implements HostAdapter. Emits an audit line; no retrieval.
func (a *ClaudeCodeAdapter) SessionEnd(ctx context.Context, event HookEvent) (HookResult, error) {
	start := a.now()
	latency := a.elapsedMs(start)
	a.audit(ctx, event, "session_end", false, latency)
	return HookResult{Enriched: false, LatencyMs: latency}, nil
}

// HealthCheck implements HostAdapter. Pings the ContextSource.
func (a *ClaudeCodeAdapter) HealthCheck(ctx context.Context) error {
	if a.source == nil {
		return fmt.Errorf("claude-code adapter: no context source configured")
	}
	if err := a.source.Ping(ctx); err != nil {
		return fmt.Errorf("claude-code adapter: context source unreachable: %w", err)
	}
	return nil
}

func (a *ClaudeCodeAdapter) retrieve(ctx context.Context, ws, query string) (*RetrievedContext, error) {
	if a.source == nil {
		return nil, fmt.Errorf("no context source configured")
	}
	rc, err := a.source.Retrieve(ctx, a.fallbackWorkspace(ws), query)
	if err != nil {
		return nil, err
	}
	if rc == nil {
		rc = &RetrievedContext{}
	}
	return rc, nil
}

func (a *ClaudeCodeAdapter) workspaceFor(event HookEvent) string {
	if event.WorkspaceID != "" {
		return event.WorkspaceID
	}
	return a.cfg.WorkspaceID
}

func (a *ClaudeCodeAdapter) fallbackWorkspace(ws string) string {
	if ws != "" {
		return ws
	}
	return a.cfg.WorkspaceID
}

func (a *ClaudeCodeAdapter) elapsedMs(start time.Time) int64 {
	return a.now().Sub(start).Milliseconds()
}

// audit emits the structured audit line:
// {ts, host='claude-code', hook, sessionID, workspaceID, enriched, latencyMs}.
func (a *ClaudeCodeAdapter) audit(
	ctx context.Context, event HookEvent, hook string, enriched bool, latencyMs int64,
) {
	a.logger.InfoContext(ctx, "audit",
		"ts", a.now().UTC().Format(time.RFC3339Nano),
		"host", ccHostName,
		"hook", hook,
		"sessionID", event.SessionID,
		"workspaceID", a.workspaceFor(event),
		"enriched", enriched,
		"latencyMs", latencyMs,
	)
}

// ── MCP tool definitions (mirror the OC adapter tool set) ────────────────────

const (
	toolRetrieveContext = "retrieve_context"
	toolRunAnalysis     = "run_analysis"
	toolListSymbols     = "list_symbols"
)

// mcpTool is a single MCP tool descriptor (tools/list).
type mcpTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

func mcpToolSet() []mcpTool {
	objSchema := func(props map[string]interface{}, required ...string) map[string]interface{} {
		s := map[string]interface{}{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return []mcpTool{
		{
			Name:        toolRetrieveContext,
			Description: "Retrieve relevant code chunks, symbols, and findings for a query.",
			InputSchema: objSchema(map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
				"top_n": map[string]interface{}{"type": "integer"},
			}, "query"),
		},
		{
			Name:        toolRunAnalysis,
			Description: "Run static analysis for the active workspace and return findings.",
			InputSchema: objSchema(map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
			}),
		},
		{
			Name:        toolListSymbols,
			Description: "List symbols matching an optional query for the active workspace.",
			InputSchema: objSchema(map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
			}),
		},
	}
}

// ── MCPServer — stdio JSON-RPC 2.0 loop (NO TCP port) ────────────────────────

// MCPServer exposes the ClaudeCodeAdapter as an MCP 0.7 stdio tool server. It
// reads line-delimited JSON-RPC requests from r and writes responses to w.
// There is NO network listener (ADR-001 local-only).
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.MCPServer.
type MCPServer struct {
	adapter     *ClaudeCodeAdapter
	clientID    string
	trust       TrustRegistry
	policy      PolicyEngine
	supplyChain SupplyChainPolicy
	logger      *slog.Logger
}

// NewMCPServer wires the dispatch chain. clientID is the local clawd-resolved
// MCP client identity (only trusted ids may invoke tools — deny-by-default).
func NewMCPServer(adapter *ClaudeCodeAdapter, clientID string) *MCPServer {
	tools := []string{toolRetrieveContext, toolRunAnalysis, toolListSymbols}
	return &MCPServer{
		adapter:     adapter,
		clientID:    clientID,
		trust:       newStaticTrustRegistry(clientID),
		policy:      allowAllPolicy{},
		supplyChain: newToolSetSupplyChain(tools...),
		logger:      adapter.logger,
	}
}

// Serve runs the blocking stdio JSON-RPC loop until r reaches EOF. One request
// per line; one response per request (notifications — no id — get no response).
func (s *MCPServer) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20) // up to 1MiB per request line.
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(errResponse(nil, rpcParseError, "parse error"))
			continue
		}
		resp := s.handle(ctx, &req)
		if resp == nil { // notification: no reply.
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("mcp: write response: %w", err)
		}
	}
	return scanner.Err()
}

// handle routes a single JSON-RPC request. Returns nil for notifications.
func (s *MCPServer) handle(ctx context.Context, req *rpcRequest) *rpcResponse {
	if req.JSONRPC != jsonRPCVersion {
		return errResponse(req.ID, rpcInvalidRequest, "invalid jsonrpc version")
	}
	switch req.Method {
	case "initialize":
		return okResponse(req.ID, map[string]interface{}{
			"protocolVersion": mcpProtocol,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": mcpServerName, "version": mcpServerVer},
		})
	case "notifications/initialized":
		return nil // notification.
	case "tools/list":
		return okResponse(req.ID, map[string]interface{}{"tools": mcpToolSet()})
	case "tools/call":
		return s.handleToolCall(ctx, req)
	default:
		if len(req.ID) == 0 {
			return nil // unknown notification.
		}
		return errResponse(req.ID, rpcMethodNotFound, "method not found: "+req.Method)
	}
}

// toolCallParams is the tools/call request shape.
type toolCallParams struct {
	Name      string `json:"name"`
	Arguments struct {
		Query string `json:"query"`
		TopN  int    `json:"top_n"`
	} `json:"arguments"`
}

// handleToolCall runs the ADR-003 deny-by-default dispatch chain then executes.
func (s *MCPServer) handleToolCall(ctx context.Context, req *rpcRequest) *rpcResponse {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResponse(req.ID, rpcInvalidParams, "invalid tool params")
	}
	ws := s.adapter.cfg.WorkspaceID

	// 1) auth — clientID must be present (HMAC identity resolved by clawd).
	if s.clientID == "" {
		return errResponse(req.ID, rpcAccessDenied, "denied: missing client identity")
	}
	// 2) trust_registry — unknown client_id → DENIED.
	if !s.trust.IsTrusted(s.clientID) {
		return errResponse(req.ID, rpcAccessDenied, "denied: untrusted client_id")
	}
	// 3) PolicyEngine.evaluate (AllowAll stub).
	if err := s.policy.Evaluate(ctx, s.clientID, p.Name, ws); err != nil {
		return errResponse(req.ID, rpcAccessDenied, "denied by policy: "+err.Error())
	}
	// 4) SupplyChainPolicy — tool must be allowlisted (unknown tool → denied).
	if err := s.supplyChain.Permit(p.Name); err != nil {
		return errResponse(req.ID, rpcAccessDenied, "denied: "+err.Error())
	}

	result, err := s.execTool(ctx, p, ws)
	if err != nil {
		// Malformed/unreachable downstream → MCP error (not a tool result).
		return errResponse(req.ID, rpcInternalError, err.Error())
	}
	// Validate against the MCP output schema before returning.
	if err := validateToolResult(result); err != nil {
		return errResponse(req.ID, rpcInternalError, "invalid tool result: "+err.Error())
	}
	return okResponse(req.ID, result)
}

// execTool runs one tool against the ContextSource seam.
func (s *MCPServer) execTool(ctx context.Context, p toolCallParams, ws string) (map[string]interface{}, error) {
	if s.adapter.source == nil {
		return nil, fmt.Errorf("no context source configured")
	}
	rc, err := s.adapter.source.Retrieve(ctx, s.adapter.fallbackWorkspace(ws), p.Arguments.Query)
	if err != nil {
		return nil, fmt.Errorf("clawde-intelligence retrieve: %w", err)
	}
	if rc == nil {
		rc = &RetrievedContext{}
	}
	switch p.Name {
	case toolRetrieveContext:
		chunks, symbols := rc.Chunks, rc.Symbols
		if p.Arguments.TopN > 0 && p.Arguments.TopN < len(chunks) {
			chunks = chunks[:p.Arguments.TopN]
		}
		return map[string]interface{}{
			"chunks":       chunks,
			"symbols":      symbols,
			"findings":     rc.Findings,
			"total_tokens": estimateTokens(formatContextBlock(rc)),
		}, nil
	case toolRunAnalysis:
		return map[string]interface{}{"findings": rc.Findings}, nil
	case toolListSymbols:
		return map[string]interface{}{"symbols": rc.Symbols}, nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", p.Name)
	}
}

// validateToolResult enforces the MCP output schema: a JSON object that
// round-trips cleanly. A nil result or unmarshalable shape is malformed.
func validateToolResult(result map[string]interface{}) error {
	if result == nil {
		return fmt.Errorf("nil result")
	}
	b, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("non-serializable result: %w", err)
	}
	var probe map[string]interface{}
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("result is not a JSON object: %w", err)
	}
	return nil
}

func okResponse(id json.RawMessage, result interface{}) *rpcResponse {
	return &rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result}
}

func errResponse(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// compile-time assertion: ClaudeCodeAdapter satisfies HostAdapter (6 methods).
var _ HostAdapter = (*ClaudeCodeAdapter)(nil)
