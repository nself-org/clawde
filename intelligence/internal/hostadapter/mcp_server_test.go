// Package hostadapter — tests for the Claude Code MCP stdio adapter.
//
// Purpose:    Verify the MCP 0.7 stdio JSON-RPC loop (initialize / tools/list /
//             tools/call retrieve_context), the ADR-003 deny-by-default chain
//             (unknown client_id → denied; unknown tool → denied), 6-method
//             HostAdapter compliance, the audit line, and stdio-only (no
//             net.Listen anywhere in the adapter source).
// Constraints: In-process JSON-RPC harness over io.Pipe-style buffers — never a
//              real subprocess and never a network listener (CI-safe).
package hostadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// mcpMockSource is an in-process ContextSource returning fixed context.
type mcpMockSource struct {
	rc      *RetrievedContext
	err     error
	healthy error
}

func (m *mcpMockSource) Retrieve(_ context.Context, _, _ string) (*RetrievedContext, error) {
	return m.rc, m.err
}
func (m *mcpMockSource) Ping(_ context.Context) error { return m.healthy }

func newTestServer(t *testing.T, clientID string, src ContextSource) *MCPServer {
	t.Helper()
	adapter := NewClaudeCodeAdapter(src, nil)
	if err := adapter.Install(context.Background(), AdapterConfig{WorkspaceID: "ws-1"}); err != nil {
		t.Fatalf("install: %v", err)
	}
	return NewMCPServer(adapter, clientID)
}

// drive runs the stdio loop over a single batch of newline-delimited requests
// and returns the decoded responses (one per non-notification request).
func drive(t *testing.T, s *MCPServer, lines ...string) []rpcResponse {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resps []rpcResponse
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r rpcResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestMCPInitializeAndToolsList(t *testing.T) {
	s := newTestServer(t, "cc-1", &mcpMockSource{rc: &RetrievedContext{}})
	resps := drive(t,
		s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	res, _ := resps[0].Result.(map[string]interface{})
	si, _ := res["serverInfo"].(map[string]interface{})
	if si["name"] != mcpServerName {
		t.Errorf("serverInfo.name = %v, want %s", si["name"], mcpServerName)
	}
	if _, ok := res["capabilities"]; !ok {
		t.Error("initialize result missing capabilities")
	}
	list, _ := resps[1].Result.(map[string]interface{})
	tools, _ := list["tools"].([]interface{})
	if len(tools) != 3 {
		t.Errorf("tools/list = %d tools, want 3 (retrieve_context, run_analysis, list_symbols)", len(tools))
	}
}

func TestMCPToolsCallRetrieveContext(t *testing.T) {
	src := &mcpMockSource{rc: &RetrievedContext{
		Chunks:   []Chunk{{FilePath: "a.go", LineStart: 1, Lang: "go", Content: "package a"}},
		Symbols:  []Symbol{{Name: "Foo", Kind: "func", FilePath: "a.go"}},
		Findings: []Finding{{Rule: "R1", Severity: "high", FilePath: "a.go", Line: 2, Message: "x"}},
	}}
	s := newTestServer(t, "cc-1", src)
	resps := drive(t, s,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"retrieve_context","arguments":{"query":"foo"}}}`)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("unexpected: %+v", resps)
	}
	res, _ := resps[0].Result.(map[string]interface{})
	for _, k := range []string{"chunks", "symbols", "findings", "total_tokens"} {
		if _, ok := res[k]; !ok {
			t.Errorf("retrieve_context result missing key %q", k)
		}
	}
	if tt, _ := res["total_tokens"].(float64); tt <= 0 {
		t.Errorf("total_tokens = %v, want > 0", res["total_tokens"])
	}
}

func TestMCPDenyByDefaultUnknownClient(t *testing.T) {
	// Server trusts only "trusted-id"; the actual identity is "intruder".
	adapter := NewClaudeCodeAdapter(&mcpMockSource{rc: &RetrievedContext{}}, nil)
	_ = adapter.Install(context.Background(), AdapterConfig{WorkspaceID: "ws-1"})
	s := NewMCPServer(adapter, "trusted-id")
	s.clientID = "intruder" // simulate an untrusted client_id resolved by clawd.

	resps := drive(t, s,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"retrieve_context","arguments":{"query":"x"}}}`)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("expected denial error, got %+v", resps)
	}
	if resps[0].Error.Code != rpcAccessDenied {
		t.Errorf("error code = %d, want %d (deny-by-default)", resps[0].Error.Code, rpcAccessDenied)
	}
	if !strings.Contains(resps[0].Error.Message, "untrusted") {
		t.Errorf("error = %q, want untrusted-client message", resps[0].Error.Message)
	}
}

func TestMCPDenyUnknownTool(t *testing.T) {
	s := newTestServer(t, "cc-1", &mcpMockSource{rc: &RetrievedContext{}})
	resps := drive(t, s,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"rm_rf","arguments":{}}}`)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("expected supply-chain denial, got %+v", resps)
	}
	if resps[0].Error.Code != rpcAccessDenied {
		t.Errorf("error code = %d, want %d", resps[0].Error.Code, rpcAccessDenied)
	}
}

func TestClaudeCodeAdapterSixMethodCompliance(t *testing.T) {
	// Compile-time guarantee from var _ HostAdapter; assert behavior at runtime.
	var a HostAdapter = NewClaudeCodeAdapter(&mcpMockSource{rc: &RetrievedContext{}, healthy: nil}, nil)
	if a.Name() != ccHostName {
		t.Errorf("Name() = %q, want %q", a.Name(), ccHostName)
	}
	ctx := context.Background()
	if err := a.Install(ctx, AdapterConfig{}); err != nil {
		t.Errorf("Install: %v", err)
	}
	if _, err := a.SessionStart(ctx, HookEvent{SessionID: "s1", WorkspaceID: "ws"}); err != nil {
		t.Errorf("SessionStart: %v", err)
	}
	if _, err := a.SessionEnd(ctx, HookEvent{SessionID: "s1"}); err != nil {
		t.Errorf("SessionEnd: %v", err)
	}
	if err := a.HealthCheck(ctx); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}
	if err := a.Uninstall(ctx); err != nil {
		t.Errorf("Uninstall: %v", err)
	}
}

// captureAdapter wraps a logger to assert the audit line shape.
func TestMCPAuditLine(t *testing.T) {
	var buf strings.Builder
	logger := newJSONLogger(&buf)
	adapter := NewClaudeCodeAdapter(&mcpMockSource{rc: &RetrievedContext{}}, logger)
	_ = adapter.Install(context.Background(), AdapterConfig{WorkspaceID: "ws-9"})
	if _, err := adapter.SessionStart(context.Background(), HookEvent{SessionID: "sess-7", WorkspaceID: "ws-9"}); err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	out := buf.String()
	for _, field := range []string{`"host":"claude-code"`, `"sessionID":"sess-7"`, `"workspaceID":"ws-9"`, `"enriched":true`, `"latencyMs"`, `"ts"`, `"hook":"session_start"`} {
		if !strings.Contains(out, field) {
			t.Errorf("audit line missing %s\nlog: %s", field, out)
		}
	}
}

// TestMCPStdioOnly_NoNetListen asserts the adapter source opens no network
// listener: neither mcp_server.go nor main.go may reference net.Listen.
func TestMCPStdioOnly_NoNetListen(t *testing.T) {
	for _, path := range []string{"mcp_server.go", "../../cmd/mcp-server/main.go"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// Strip line comments so prose like "never net.Listen" doesn't trip the
		// scan — we only flag a real "net" import or an actual listener call.
		var code strings.Builder
		for _, ln := range strings.Split(string(b), "\n") {
			if i := strings.Index(ln, "//"); i >= 0 {
				ln = ln[:i]
			}
			code.WriteString(ln)
			code.WriteString("\n")
		}
		src := code.String()
		for _, banned := range []string{"net.Listen", "ListenAndServe", `"net"`} {
			if strings.Contains(src, banned) {
				t.Errorf("%s references %q — MCP transport must be stdio only", path, banned)
			}
		}
	}
}
