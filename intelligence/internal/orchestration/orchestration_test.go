// orchestration_test.go — unit tests for the orchestration package.
//
// Purpose: Verify:
//   1. ToolRegistry register/get/registered-tools behaviour.
//   2. ExecuteShellActivity fail-closed when CLAWDE_SANDBOX_ENABLED is not "1".
//   3. ExecuteShellActivity success when CLAWDE_SANDBOX_ENABLED=1.
//   4. AgentRunWorkflow terminates after one turn (no tool call).
//   5. AgentRunWorkflow executes a tool-call turn via sentinel content.
//   6. EvalWorkflow computes correct metrics and calls InsertEvalRunActivity.
//   7. RetrieveContextWorkflow propagates activity output.
//   8. InsertEvalRunActivity unit tests (recorder called / nil recorder no-op).
//
// All workflow tests use go.temporal.io/sdk/testsuite (in-process).
// Constraints: File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration tests.
package orchestration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/eval"
	"github.com/nself-org/clawde/intelligence/internal/retrieval"
)

// ── Stubs ─────────────────────────────────────────────────────────────────────

type stubKernel struct {
	result *retrieval.RetrievalContext
	err    error
}

func (s *stubKernel) RetrieveContext(_ context.Context, _ uuid.UUID, _ string, _ []float32) (*retrieval.RetrievalContext, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	return &retrieval.RetrievalContext{}, nil
}

type stubRunner struct{ err error }

func (s *stubRunner) Handle(_ context.Context, _ []byte) error { return s.err }

type stubRecorder struct {
	recorded []eval.EvalResult
	err      error
}

func (s *stubRecorder) Record(_ context.Context, _ uuid.UUID, result eval.EvalResult) error {
	if s.err != nil {
		return s.err
	}
	s.recorded = append(s.recorded, result)
	return nil
}

func newTestActivities(kernel HybridKerneler, recorder EvalRecorder) *Activities {
	return NewActivities(kernel, &stubRunner{}, nil, recorder)
}

// newEnv creates a TestWorkflowEnvironment with all orchestration
// workflows and activities registered.
func newEnv(acts *Activities) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(RetrieveContextWorkflow)
	env.RegisterWorkflow(AgentRunWorkflow)
	env.RegisterWorkflow(EvalWorkflow)
	env.RegisterActivity(acts)
	env.RegisterActivity(stubLLMActivity)
	env.RegisterActivity(stubToolDispatchActivity)
	return env
}

// ── 1. ToolRegistry tests ─────────────────────────────────────────────────────

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()
	acts := newTestActivities(&stubKernel{}, nil)
	reg := NewToolRegistry(acts)

	for _, name := range []string{
		ToolRetrieveContext, ToolRunAnalysis, ToolListSymbols,
		ToolGetFileContent, ToolExecuteShell,
	} {
		fn, err := reg.GetActivity(name)
		if err != nil {
			t.Errorf("built-in %q not found: %v", name, err)
		}
		if fn == nil {
			t.Errorf("built-in %q returned nil", name)
		}
	}
}

func TestToolRegistry_RegisterCustomTool(t *testing.T) {
	t.Parallel()
	acts := newTestActivities(&stubKernel{}, nil)
	reg := NewToolRegistry(acts)

	if err := reg.RegisterTool("custom", func() string { return "ok" }); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	fn, err := reg.GetActivity("custom")
	if err != nil || fn == nil {
		t.Fatalf("GetActivity(custom): err=%v fn=%v", err, fn)
	}
}

func TestToolRegistry_RegisterEmptyName(t *testing.T) {
	t.Parallel()
	acts := newTestActivities(&stubKernel{}, nil)
	reg := NewToolRegistry(acts)
	if err := reg.RegisterTool("", func() {}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestToolRegistry_GetUnknown(t *testing.T) {
	t.Parallel()
	acts := newTestActivities(&stubKernel{}, nil)
	reg := NewToolRegistry(acts)
	_, err := reg.GetActivity("no_such_tool")
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestToolRegistry_RegisteredTools_Sorted(t *testing.T) {
	t.Parallel()
	acts := newTestActivities(&stubKernel{}, nil)
	reg := NewToolRegistry(acts)
	names := reg.RegisteredTools()
	if len(names) < 5 {
		t.Fatalf("expected ≥5 tools, got %d", len(names))
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("not sorted at index %d: %q < %q", i, names[i], names[i-1])
		}
	}
}

// ── 2. ExecuteShellActivity fail-closed ───────────────────────────────────────

// Note: ExecuteShell tests use t.Setenv so they cannot use t.Parallel.
func TestExecuteShellActivity_FailClosed_NoSandbox(t *testing.T) {
	t.Setenv("CLAWDE_SANDBOX_ENABLED", "")
	acts := newTestActivities(&stubKernel{}, nil)
	_, err := acts.ExecuteShellActivity(context.Background(), ExecuteShellInput{Command: "echo"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got: %v", err)
	}
}

func TestExecuteShellActivity_FailClosed_WrongValue(t *testing.T) {
	t.Setenv("CLAWDE_SANDBOX_ENABLED", "yes") // only "1" is accepted
	acts := newTestActivities(&stubKernel{}, nil)
	_, err := acts.ExecuteShellActivity(context.Background(), ExecuteShellInput{Command: "echo"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for 'yes', got: %v", err)
	}
}

func TestExecuteShellActivity_Sandbox_Enabled(t *testing.T) {
	t.Setenv("CLAWDE_SANDBOX_ENABLED", "1")
	acts := newTestActivities(&stubKernel{}, nil)
	out, err := acts.ExecuteShellActivity(context.Background(), ExecuteShellInput{
		Command: "echo",
		Args:    []string{"clawde"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	if out.Stdout != "clawde\n" {
		t.Fatalf("expected stdout %q, got %q", "clawde\n", out.Stdout)
	}
}

func TestExecuteShellActivity_EmptyCommand(t *testing.T) {
	t.Setenv("CLAWDE_SANDBOX_ENABLED", "1")
	acts := newTestActivities(&stubKernel{}, nil)
	_, err := acts.ExecuteShellActivity(context.Background(), ExecuteShellInput{Command: ""})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

// ── 3. InsertEvalRunActivity unit tests ───────────────────────────────────────

func TestInsertEvalRunActivity_RecorderCalled(t *testing.T) {
	t.Parallel()
	rec := &stubRecorder{}
	acts := newTestActivities(&stubKernel{}, rec)
	err := acts.InsertEvalRunActivity(context.Background(), InsertEvalRunInput{
		WorkspaceID: uuid.New().String(),
		Result:      eval.EvalResult{Provider: "bge-m3", Dataset: "golden", RecallAt10: 0.85},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(rec.recorded) != 1 || rec.recorded[0].RecallAt10 != 0.85 {
		t.Fatalf("unexpected recorded results: %v", rec.recorded)
	}
}

func TestInsertEvalRunActivity_NilRecorder(t *testing.T) {
	t.Parallel()
	acts := newTestActivities(&stubKernel{}, nil)
	err := acts.InsertEvalRunActivity(context.Background(), InsertEvalRunInput{
		WorkspaceID: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("expected no error with nil recorder, got: %v", err)
	}
}

func TestInsertEvalRunActivity_InvalidUUID(t *testing.T) {
	t.Parallel()
	rec := &stubRecorder{}
	acts := newTestActivities(&stubKernel{}, rec)
	err := acts.InsertEvalRunActivity(context.Background(), InsertEvalRunInput{
		WorkspaceID: "not-a-uuid",
	})
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

// ── 4. Workflow tests using Temporal testsuite ────────────────────────────────

func TestRetrieveContextWorkflow_Success(t *testing.T) {
	acts := newTestActivities(&stubKernel{}, nil)
	env := newEnv(acts)

	chunkID := uuid.New()
	mockRetrieve := func(ctx context.Context, in RetrieveContextInput) (RetrieveContextOutput, error) {
		return RetrieveContextOutput{
			Context: &retrieval.RetrievalContext{
				Chunks: []retrieval.ScoredChunk{{ID: chunkID, Score: 0.9, Content: "hello"}},
			},
		}, nil
	}
	env.OnActivity("RetrieveContextActivity", mock.Anything, mock.Anything).Return(mockRetrieve)

	env.ExecuteWorkflow(RetrieveContextWorkflow, RetrieveContextWorkflowInput{
		WorkspaceID: uuid.New().String(),
		Query:       "how auth works",
	})

	var out RetrieveContextWorkflowOutput
	if err := env.GetWorkflowResult(&out); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if out.Context == nil || len(out.Context.Chunks) != 1 {
		t.Fatalf("expected 1 chunk")
	}
}

func TestRetrieveContextWorkflow_ActivityError(t *testing.T) {
	acts := newTestActivities(&stubKernel{}, nil)
	env := newEnv(acts)

	mockFail := func(ctx context.Context, in RetrieveContextInput) (RetrieveContextOutput, error) {
		return RetrieveContextOutput{}, fmt.Errorf("db error")
	}
	env.OnActivity("RetrieveContextActivity", mock.Anything, mock.Anything).Return(mockFail)

	env.ExecuteWorkflow(RetrieveContextWorkflow, RetrieveContextWorkflowInput{
		WorkspaceID: uuid.New().String(),
		Query:       "test",
	})
	var out RetrieveContextWorkflowOutput
	if err := env.GetWorkflowResult(&out); err == nil {
		t.Fatal("expected error")
	}
}

func TestAgentRunWorkflow_NoToolCall_StopsAfterOneTurn(t *testing.T) {
	acts := newTestActivities(&stubKernel{}, nil)
	env := newEnv(acts)

	mockLLM := func(ctx context.Context, in StubLLMInput) (AgentMessage, error) {
		return AgentMessage{Role: "assistant", Content: "done"}, nil
	}
	env.OnActivity(stubLLMActivity, mock.Anything, mock.Anything).Return(mockLLM)

	env.ExecuteWorkflow(AgentRunWorkflow, AgentRunInput{
		ModelLane:       "sonnet",
		MaxTurns:        5,
		InitialMessages: []AgentMessage{{Role: "user", Content: "hello"}},
	})

	var out AgentRunOutput
	if err := env.GetWorkflowResult(&out); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if out.Turns != 1 {
		t.Fatalf("expected 1 turn, got %d", out.Turns)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out.Messages))
	}
}

func TestAgentRunWorkflow_ToolCallTurn(t *testing.T) {
	acts := newTestActivities(&stubKernel{}, nil)
	env := newEnv(acts)

	// First call returns a tool-call sentinel; second returns "done".
	callCount := 0
	mockLLM := func(ctx context.Context, in StubLLMInput) (AgentMessage, error) {
		callCount++
		if callCount == 1 {
			return AgentMessage{Role: "assistant", Content: "TOOL_CALL:retrieve_context"}, nil
		}
		return AgentMessage{Role: "assistant", Content: "done"}, nil
	}
	env.OnActivity(stubLLMActivity, mock.Anything, mock.Anything).Return(mockLLM)

	mockDispatch := func(ctx context.Context, in StubToolDispatchInput) (string, error) {
		return fmt.Sprintf("result for %s", in.ToolName), nil
	}
	env.OnActivity(stubToolDispatchActivity, mock.Anything, mock.Anything).Return(mockDispatch)

	env.ExecuteWorkflow(AgentRunWorkflow, AgentRunInput{
		ModelLane:       "sonnet",
		MaxTurns:        5,
		InitialMessages: []AgentMessage{{Role: "user", Content: "retrieve context"}},
	})

	var out AgentRunOutput
	if err := env.GetWorkflowResult(&out); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if out.Turns != 2 {
		t.Fatalf("expected 2 turns, got %d", out.Turns)
	}
	// user + assistant(tool_call) + tool_result + assistant(done)
	if len(out.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(out.Messages), out.Messages)
	}
}

func TestEvalWorkflow_MetricsAndInsert(t *testing.T) {
	acts := newTestActivities(&stubKernel{}, nil)
	env := newEnv(acts)

	chunkID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	mockChild := func(ctx workflow.Context, in RetrieveContextWorkflowInput) (RetrieveContextWorkflowOutput, error) {
		return RetrieveContextWorkflowOutput{
			Context: &retrieval.RetrievalContext{
				Chunks: []retrieval.ScoredChunk{{ID: chunkID, Score: 1.0}},
			},
		}, nil
	}
	env.OnWorkflow(RetrieveContextWorkflow, mock.Anything, mock.Anything).Return(mockChild)

	insertCalled := false
	mockInsert := func(ctx context.Context, in InsertEvalRunInput) error {
		insertCalled = true
		if in.Result.RecallAt10 != 1.0 {
			return fmt.Errorf("expected RecallAt10=1.0, got %f", in.Result.RecallAt10)
		}
		return nil
	}
	env.OnActivity("InsertEvalRunActivity", mock.Anything, mock.Anything).Return(mockInsert)

	env.ExecuteWorkflow(EvalWorkflow, EvalWorkflowInput{
		WorkspaceID: uuid.New().String(),
		Provider:    "bge-m3",
		Dataset:     "test",
		Pairs: []EvalPair{
			{Query: "how auth works", RelevantIDs: []string{chunkID.String()}},
		},
	})

	var out EvalWorkflowOutput
	if err := env.GetWorkflowResult(&out); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if out.Result.RecallAt10 != 1.0 {
		t.Fatalf("expected RecallAt10=1.0, got %f", out.Result.RecallAt10)
	}
	if !insertCalled {
		t.Fatal("InsertEvalRunActivity was not called")
	}
}

func TestEvalWorkflow_NoPairs(t *testing.T) {
	acts := newTestActivities(&stubKernel{}, nil)
	env := newEnv(acts)

	env.ExecuteWorkflow(EvalWorkflow, EvalWorkflowInput{
		WorkspaceID: uuid.New().String(),
		Provider:    "bge-m3",
		Dataset:     "empty",
	})
	var out EvalWorkflowOutput
	if err := env.GetWorkflowResult(&out); err == nil {
		t.Fatal("expected error for no pairs")
	}
}
