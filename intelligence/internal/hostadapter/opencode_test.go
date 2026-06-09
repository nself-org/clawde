// Package hostadapter — OpenCodeAdapter integration + compliance tests.
//
// Covers: context-block format, enriched=true on 3 chunks+1 symbol+1 finding,
//         graceful degradation (source down → enriched=false, no panic),
//         6-method HostAdapter compliance, audit-log emission, AllowAll stub.
package hostadapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ── mock gRPC server (KernelSeam) ────────────────────────────────────────────

type mockKernel struct {
	rc      *RetrievedContext
	retErr  error
	pingErr error
}

func (m *mockKernel) Retrieve(_ context.Context, _, _ string) (*RetrievedContext, error) {
	if m.retErr != nil {
		return nil, m.retErr
	}
	return m.rc, nil
}
func (m *mockKernel) Healthy(_ context.Context) error { return m.pingErr }

func threeChunkContext() *RetrievedContext {
	return &RetrievedContext{
		Chunks: []Chunk{
			{FilePath: "internal/auth/login.go", LineStart: 42, Lang: "go", Content: "func Login() {}"},
			{FilePath: "internal/auth/token.go", LineStart: 7, Lang: "go", Content: "func Mint() {}"},
			{FilePath: "internal/db/conn.go", LineStart: 100, Lang: "go", Content: "var Pool *pgxpool.Pool"},
		},
		Symbols:  []Symbol{{Name: "Login", Kind: "function", Signature: "func Login() error", FilePath: "internal/auth/login.go"}},
		Findings: []Finding{{Rule: "sql-injection", Severity: "HIGH", FilePath: "internal/db/conn.go", Line: 100, Message: "untrusted input"}},
	}
}

// ── 6-method compliance ──────────────────────────────────────────────────────

func TestHostAdapter_ExactlySixMethods(t *testing.T) {
	typ := reflect.TypeOf((*HostAdapter)(nil)).Elem()
	if got := typ.NumMethod(); got != 6 {
		t.Fatalf("HostAdapter must have EXACTLY 6 methods, got %d", got)
	}
	want := []string{"HealthCheck", "Install", "Name", "SessionEnd", "SessionStart", "Uninstall"}
	for _, name := range want {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("HostAdapter missing required method %q", name)
		}
	}
	// Ensure InvalidateContextCache is NOT present.
	if _, ok := typ.MethodByName("InvalidateContextCache"); ok {
		t.Error("HostAdapter must NOT have InvalidateContextCache")
	}
}

func TestOpenCodeAdapter_ImplementsHostAdapter(t *testing.T) {
	var _ HostAdapter = NewOpenCodeAdapter(NewGRPCSource(&mockKernel{}), nil)
}

// ── context block + enriched=true ────────────────────────────────────────────

func TestSessionStart_Enriched(t *testing.T) {
	src := NewGRPCSource(&mockKernel{rc: threeChunkContext()})
	a := NewOpenCodeAdapter(src, nil)
	if err := a.Install(context.Background(), AdapterConfig{WorkspaceID: "ws-1"}); err != nil {
		t.Fatalf("install: %v", err)
	}
	res, err := a.SessionStart(context.Background(), HookEvent{
		TS: time.Now(), Host: "opencode", Hook: "session_start",
		SessionID: "s-1", WorkspaceID: "ws-1",
	})
	if err != nil {
		t.Fatalf("session_start: %v", err)
	}
	if !res.Enriched {
		t.Fatal("expected Enriched=true")
	}
	if res.Error != "" {
		t.Fatalf("expected no error, got %q", res.Error)
	}
}

func TestFormatContextBlock_ExactShape(t *testing.T) {
	block := formatContextBlock(threeChunkContext())
	if !strings.HasPrefix(block, "<clawde_context>\n") {
		t.Errorf("missing open tag: %q", block[:30])
	}
	if !strings.HasSuffix(block, "</clawde_context>") {
		t.Error("missing close tag")
	}
	for _, want := range []string{
		"### Relevant code\n",
		"internal/auth/login.go:42\n",
		"```go\nfunc Login() {}\n```\n",
		"### Symbols\n",
		"### Findings\n",
		"[HIGH] sql-injection internal/db/conn.go:100 — untrusted input",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q\n---\n%s", want, block)
		}
	}
}

func TestRetrieveContextTool_TokenCount(t *testing.T) {
	a := NewOpenCodeAdapter(NewGRPCSource(&mockKernel{rc: threeChunkContext()}), nil)
	block, tokens, enriched := a.RetrieveContextTool(context.Background(), "ws-1", "auth")
	if !enriched {
		t.Fatal("expected enriched=true")
	}
	if tokens <= 0 || tokens != estimateTokens(block) {
		t.Fatalf("token count mismatch: got %d, block estimate %d", tokens, estimateTokens(block))
	}
}

// ── graceful degradation (ADR-001) ───────────────────────────────────────────

func TestSessionStart_GracefulDegradation_ServerDown(t *testing.T) {
	src := NewGRPCSource(&mockKernel{retErr: context.DeadlineExceeded})
	a := NewOpenCodeAdapter(src, nil)
	_ = a.Install(context.Background(), AdapterConfig{WorkspaceID: "ws-1"})

	var res HookResult
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		// must not panic
		res, err = a.SessionStart(context.Background(), HookEvent{SessionID: "s-2", WorkspaceID: "ws-1"})
	}()
	<-done

	if err != nil {
		t.Fatalf("degradation must not return error, got %v", err)
	}
	if res.Enriched {
		t.Fatal("expected Enriched=false when source is down")
	}
	if res.Error == "" {
		t.Fatal("expected a warning message on degradation")
	}
}

func TestRetrieveContextTool_DegradesNoPanic(t *testing.T) {
	a := NewOpenCodeAdapter(NewGRPCSource(&mockKernel{retErr: context.Canceled}), nil)
	block, tokens, enriched := a.RetrieveContextTool(context.Background(), "ws", "q")
	if enriched || block != "" || tokens != 0 {
		t.Fatalf("expected empty/false on degradation, got block=%q tokens=%d enriched=%v", block, tokens, enriched)
	}
}

func TestHealthCheck(t *testing.T) {
	ok := NewOpenCodeAdapter(NewGRPCSource(&mockKernel{}), nil)
	if err := ok.HealthCheck(context.Background()); err != nil {
		t.Fatalf("expected healthy, got %v", err)
	}
	down := NewOpenCodeAdapter(NewGRPCSource(&mockKernel{pingErr: context.DeadlineExceeded}), nil)
	if err := down.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error when source unreachable")
	}
}

// ── audit log ────────────────────────────────────────────────────────────────

func newJSONLogger(w *strings.Builder) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func TestAuditLogEmitted(t *testing.T) {
	var buf strings.Builder
	a := NewOpenCodeAdapter(NewGRPCSource(&mockKernel{rc: threeChunkContext()}), newJSONLogger(&buf))
	_ = a.Install(context.Background(), AdapterConfig{WorkspaceID: "ws-1"})
	_, _ = a.SessionStart(context.Background(), HookEvent{SessionID: "s-3", WorkspaceID: "ws-1"})
	_, _ = a.SessionEnd(context.Background(), HookEvent{SessionID: "s-3", WorkspaceID: "ws-1"})

	var sawStart, sawEnd bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m["msg"] != "audit" {
			continue
		}
		for _, k := range []string{"ts", "host", "hook", "session_id", "workspace_id", "enriched", "latency_ms"} {
			if _, ok := m[k]; !ok {
				t.Errorf("audit record missing key %q: %v", k, m)
			}
		}
		if m["host"] != "opencode" {
			t.Errorf("audit host = %v, want opencode", m["host"])
		}
		switch m["hook"] {
		case "session_start":
			sawStart = true
		case "session_end":
			sawEnd = true
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing audit records: start=%v end=%v", sawStart, sawEnd)
	}
}

func TestPolicyAllowAll(t *testing.T) {
	t.Setenv("CLAWDE_POLICY_ENABLED", "true")
	if !PolicyAllowAll() {
		t.Error("expected allow when enabled")
	}
	t.Setenv("CLAWDE_POLICY_ENABLED", "")
	if !PolicyAllowAll() {
		t.Error("AllowAll stub must default to allow")
	}
}

func TestName(t *testing.T) {
	if NewOpenCodeAdapter(nil, nil).Name() != "opencode" {
		t.Fatal("Name() must be opencode")
	}
}
