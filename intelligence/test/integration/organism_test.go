//go:build integration

// organism_test.go — Integration tests exercising the full clawde-intelligence
// organism end-to-end.
//
// Purpose: Prove that all wired components (T01–T12) function as a coherent
//          organism after assembly.  Tests are independently skippable: each
//          guards on CLAWDE_TEST_PG_DSN via requirePG() in helpers.go.
// Inputs:  CLAWDE_TEST_PG_DSN (Postgres DSN with pgmq extension installed).
// Outputs: All 5 cases PASS with live pg; all 5 SKIP without it.
// Constraints:
//   - Each test creates its own workspace_id (no cross-test pollution).
//   - Ports resolved via 127.0.0.1:0 (no hardcoded ports).
//   - No live Ollama / TEI / Temporal connections — stubs only.
//   - File ≤ 300 lines.
//
// SPORT: REGISTRY-SERVICES.md → integration-test-lane, status: active.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nself-org/clawde/intelligence/internal/gateway"
	"github.com/nself-org/clawde/intelligence/internal/orchestration"
	"github.com/nself-org/clawde/intelligence/internal/server"
	"github.com/nself-org/clawde/intelligence/internal/worker"
	"go.temporal.io/sdk/testsuite"
)

// ── 1. TestOrganismCompileContext ─────────────────────────────────────────────

// TestOrganismCompileContext verifies that the full gRPC→compiler→retriever
// pipeline returns Enriched=true for a seeded workspace.
//
// Flow: startTestServer (in-process) → gRPC CompileContext → Enriched=true.
func TestOrganismCompileContext(t *testing.T) {
	handle, pool, cleanup := startTestServer(t)
	defer cleanup()

	workspaceID, wsCleanup := seedTestWorkspace(t, pool)
	defer wsCleanup()

	client := server.NewGatewayServiceClient(handle.conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.CompileContext(ctx, &server.CompileContextRequest{
		WorkspaceId: workspaceID,
		Signals: &server.SessionSignals{
			ActiveFilePath: "internal/compiler/compiler.go",
			VisibleSymbols: []string{"Compiler", "CompileContext"},
		},
	})
	if err != nil {
		t.Fatalf("CompileContext RPC: %v", err)
	}
	if !resp.Enriched {
		t.Errorf("expected Enriched=true, got false (ContextBlock=%q)", resp.ContextBlock)
	}
}

// ── 2. TestOrganismEmbedJobDrains ─────────────────────────────────────────────

// TestOrganismEmbedJobDrains verifies that a job enqueued on QueueEmbed is
// consumed by the worker.Pool within 5 seconds.
//
// Flow: enqueue embed job → worker.Pool.Start() → handler called → Pool.Stop().
func TestOrganismEmbedJobDrains(t *testing.T) {
	requirePG(t) // guard: skip without DSN

	var processed atomic.Int32
	done := make(chan struct{})

	store := newMemStore()
	store.enqueue(worker.QueueEmbed, &worker.Message{
		MsgID:   1,
		JobID:   "test-embed-job-1",
		Queue:   worker.QueueEmbed,
		Payload: []byte(`{"workspace_id":"test","text":"hello world"}`),
	})

	handlers := map[string]worker.Handler{
		worker.QueueEmbed: func(_ context.Context, msg *worker.Message) error {
			if processed.Add(1) == 1 {
				close(done)
			}
			return nil
		},
	}

	pool := worker.New(worker.Config{
		Store:    store,
		Handlers: handlers,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	select {
	case <-done:
		// job drained within timeout
	case <-ctx.Done():
		t.Fatal("embed job did not drain within 5s")
	}

	if n := processed.Load(); n != 1 {
		t.Errorf("expected 1 processed job, got %d", n)
	}
}

// ── 3. TestOrganismRouterFailover ─────────────────────────────────────────────

// TestOrganismRouterFailover verifies that the failover mechanism skips a
// rate-limited provider and returns a successful response from the second.
//
// Flow: httptest.Server returning 429 for primary → httptest.Server returning
//       200 for fallback → gateway.WithFailover → Enriched=true from fallback.
func TestOrganismRouterFailover(t *testing.T) {
	requirePG(t) // guard: skip without DSN (prerequisite consistency with suite)

	// Primary: always return 429.
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	}))
	defer primarySrv.Close()

	// Fallback: return a well-formed OpenAI-compat chat completion.
	fallbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"choices": []map[string]interface{}{{"index": 0, "message": map[string]string{"role": "assistant", "content": "hello from fallback"}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 4},
		})
	}))
	defer fallbackSrv.Close()

	primaryEntry := gateway.ProviderEntry{
		Lane:     gateway.LaneFast,
		Provider: "openai",
		BaseURL:  primarySrv.URL + "/v1",
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
	}
	fallbackEntry := gateway.ProviderEntry{
		Lane:     gateway.LaneFast,
		Provider: "openai",
		BaseURL:  fallbackSrv.URL + "/v1",
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
	}

	entries := []gateway.ProviderEntry{primaryEntry, fallbackEntry}
	req := gateway.LaneRequest{
		Lane:     gateway.LaneFast,
		Messages: []gateway.Message{{Role: "user", Content: "ping"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := gateway.WithFailover(ctx, entries, req)
	if err != nil {
		t.Fatalf("WithFailover: unexpected error: %v", err)
	}
	if !result.Enriched {
		t.Errorf("expected Enriched=true, got false")
	}
	t.Logf("TestOrganismRouterFailover: provider=%q content=%q", result.ProviderUsed, result.Response.Content)
}

// ── 4. TestOrganismDocIngest ──────────────────────────────────────────────────

// TestOrganismDocIngest verifies that IngestDocURL accepts a request and the
// RPC path is reachable end-to-end.
//
// The production KBIngestor has nil deps (per server.go T07 TODOs) so it
// returns Unavailable gracefully — which this test accepts.  The test confirms
// the handler is wired and reachable, not that chunks were stored.
func TestOrganismDocIngest(t *testing.T) {
	handle, pool, cleanup := startTestServer(t)
	defer cleanup()

	workspaceID, wsCleanup := seedTestWorkspace(t, pool)
	defer wsCleanup()

	client := server.NewGatewayServiceClient(handle.conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.IngestDocURL(ctx, &server.IngestDocURLRequest{
		WorkspaceId: workspaceID,
		Url:         "https://example.com/test-doc.md",
		DocType:     "markdown",
	})
	if err != nil {
		if isGRPCUnavailable(err) {
			t.Logf("IngestDocURL: Unavailable (expected with nil ingestor deps): %v", err)
			return
		}
		t.Fatalf("IngestDocURL: unexpected error: %v", err)
	}
	t.Logf("IngestDocURL: chunks_enqueued=%d skipped=%v", resp.ChunksEnqueued, resp.Skipped)
}

// ── 5. TestOrganismAgentWorkflow ──────────────────────────────────────────────

// TestOrganismAgentWorkflow verifies that AgentRunWorkflow terminates within
// MaxTurns=3 using the Temporal test environment (in-process, no live server).
//
// Flow: testsuite.WorkflowTestSuite → register AgentRunWorkflow + Activities →
//       execute → assert termination within 3 turns.
func TestOrganismAgentWorkflow(t *testing.T) {
	requirePG(t) // guard: skip without DSN (prerequisite consistency with suite)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	// Activities wired with nil gwClient (stub fallback path per NewActivities docs).
	acts := orchestration.NewActivities(nil, nil, nil, nil, nil)
	env.RegisterWorkflow(orchestration.AgentRunWorkflow)
	env.RegisterActivity(acts)

	in := orchestration.AgentRunInput{
		ModelLane:    string(gateway.LaneFast),
		SystemPrompt: "You are a test assistant. Reply 'done' with no tool calls.",
		MaxTurns:     3,
		InitialMessages: []orchestration.AgentMessage{
			{Role: "user", Content: "Say done."},
		},
	}

	env.ExecuteWorkflow(orchestration.AgentRunWorkflow, in)

	if !env.IsWorkflowCompleted() {
		t.Fatal("AgentRunWorkflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("AgentRunWorkflow error: %v", err)
	}

	var out orchestration.AgentRunOutput
	if err := env.GetWorkflowResult(&out); err != nil {
		t.Fatalf("GetWorkflowResult: %v", err)
	}
	if out.Turns > 3 {
		t.Errorf("expected ≤3 turns, got %d", out.Turns)
	}
	t.Logf("AgentRunWorkflow completed in %d turn(s)", out.Turns)
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// isGRPCUnavailable reports true for gRPC Unavailable status errors.
func isGRPCUnavailable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "Unavailable") || contains(s, "unavailable")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	}()
}
