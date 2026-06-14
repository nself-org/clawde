// workflows.go — Temporal Workflow definitions for durable agent execution.
//
// Purpose: Three workflows over the clawde-intelligence task queue:
//   1. RetrieveContextWorkflow — durable wrap of RetrieveContextActivity.
//      Activity StartToClose=30s, retry: InitialInterval=1s, MaxAttempts=3.
//   2. AgentRunWorkflow — multi-turn LLM agent loop via ToolRegistry.
//      Input: AgentRunInput{model_lane, system_prompt, tools, max_turns}.
//   3. EvalWorkflow — offline eval over golden pairs → metrics → clawde_eval_runs.
//
// Constraints: File ≤500 lines. No direct DB/HTTP in workflow code.
//              LLM dispatch is represented by a stub activity seam replaceable at
//              worker registration time without forking this file.
//
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.RetrieveContextWorkflow,
//        orchestration.AgentRunWorkflow, orchestration.EvalWorkflow.
package orchestration

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/nself-org/clawde/intelligence/internal/eval"
	"github.com/nself-org/clawde/intelligence/internal/retrieval"
)

// ── Shared activity options ───────────────────────────────────────────────────

// defaultActivityOptions returns activity options for most retrieval activities.
// StartToClose=30s; 3 attempts with 1s initial backoff and 2× multiplier.
func defaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
}

// fastActivityOptions are used where a single attempt is preferred.
func fastActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
}

// ── 1. RetrieveContextWorkflow ────────────────────────────────────────────────

// RetrieveContextWorkflowInput is the input for RetrieveContextWorkflow.
type RetrieveContextWorkflowInput struct {
	WorkspaceID string    `json:"workspace_id"` // UUID string
	Query       string    `json:"query"`
	QueryVec    []float32 `json:"query_vec,omitempty"`
}

// RetrieveContextWorkflowOutput carries the fused retrieval result.
type RetrieveContextWorkflowOutput struct {
	Context *retrieval.RetrievalContext `json:"context"`
}

// RetrieveContextWorkflow is a durable wrapper around RetrieveContextActivity.
//
// Purpose: Fault-tolerant retrieval — transient DB/TEI failures are retried
//          automatically by Temporal without the caller resubmitting the workflow.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.RetrieveContextWorkflow.
func RetrieveContextWorkflow(ctx workflow.Context, in RetrieveContextWorkflowInput) (RetrieveContextWorkflowOutput, error) {
	actCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions())

	var out RetrieveContextOutput
	err := workflow.ExecuteActivity(actCtx,
		"RetrieveContextActivity",
		RetrieveContextInput{
			WorkspaceID: in.WorkspaceID,
			Query:       in.Query,
			QueryVec:    in.QueryVec,
		},
	).Get(actCtx, &out)
	if err != nil {
		return RetrieveContextWorkflowOutput{}, fmt.Errorf("retrieve_context_workflow: %w", err)
	}
	return RetrieveContextWorkflowOutput{Context: out.Context}, nil
}

// ── 2. AgentRunWorkflow ───────────────────────────────────────────────────────

// ToolCall is one tool invocation from the LLM.
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// AgentMessage is one turn in the multi-turn conversation.
type AgentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	ToolID  string `json:"tool_id,omitempty"`
}

// AgentRunInput is the input for AgentRunWorkflow.
type AgentRunInput struct {
	ModelLane       string         `json:"model_lane"`
	SystemPrompt    string         `json:"system_prompt"`
	Tools           []string       `json:"tools,omitempty"`
	MaxTurns        int            `json:"max_turns"`
	InitialMessages []AgentMessage `json:"initial_messages"`
}

// AgentRunOutput is returned when the workflow completes.
type AgentRunOutput struct {
	Messages []AgentMessage `json:"messages"`
	Turns    int            `json:"turns"`
}

// AgentRunWorkflow implements a durable multi-turn LLM agent loop.
//
// Each turn:
//  1. LLM call via Activities.LLMCallActivity (real gateway-backed; stub fallback when nil client).
//  2. If no tool call in response → stop.
//  3. Dispatch tool call via Activities.ToolDispatchActivity (real ToolRegistry dispatcher).
//  4. Append tool result and continue.
//
// Bounded by MaxTurns (default 10).
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.AgentRunWorkflow.
func AgentRunWorkflow(ctx workflow.Context, in AgentRunInput) (AgentRunOutput, error) {
	maxTurns := in.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	messages := make([]AgentMessage, len(in.InitialMessages))
	copy(messages, in.InitialMessages)
	actCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions())

	for turn := 0; turn < maxTurns; turn++ {
		var assistantMsg AgentMessage
		err := workflow.ExecuteActivity(actCtx, "LLMCallActivity",
			StubLLMInput{
				ModelLane:    in.ModelLane,
				SystemPrompt: in.SystemPrompt,
				Tools:        in.Tools,
				Messages:     messages,
			},
		).Get(actCtx, &assistantMsg)
		if err != nil {
			return AgentRunOutput{}, fmt.Errorf("agent_run: llm turn %d: %w", turn+1, err)
		}
		messages = append(messages, assistantMsg)

		tc := parseToolCall(assistantMsg.Content)
		if tc == nil {
			return AgentRunOutput{Messages: messages, Turns: turn + 1}, nil
		}

		var toolResult string
		dispatchErr := workflow.ExecuteActivity(actCtx, "ToolDispatchActivity",
			StubToolDispatchInput{ToolName: tc.Name, Input: tc.Input},
		).Get(actCtx, &toolResult)
		if dispatchErr != nil {
			toolResult = fmt.Sprintf("error: %v", dispatchErr)
		}
		messages = append(messages, AgentMessage{Role: "tool", Content: toolResult, ToolID: tc.ID})
	}

	return AgentRunOutput{Messages: messages, Turns: maxTurns}, nil
}

// parseToolCall extracts a ToolCall from an assistant message.
// Sentinel format "TOOL_CALL:<name>" enables deterministic test loop control.
// A real implementation would unmarshal structured JSON from the LLM response.
func parseToolCall(content string) *ToolCall {
	const prefix = "TOOL_CALL:"
	if len(content) > len(prefix) && content[:len(prefix)] == prefix {
		return &ToolCall{ID: "tc_1", Name: content[len(prefix):], Input: map[string]any{}}
	}
	return nil
}

// ── LLM + tool-dispatch stub activities (seam for deterministic workflow testing) ─

// StubLLMInput is the input for the stub LLM activity.
type StubLLMInput struct {
	ModelLane    string         `json:"model_lane"`
	SystemPrompt string         `json:"system_prompt"`
	Tools        []string       `json:"tools"`
	Messages     []AgentMessage `json:"messages"`
}

// stubLLMActivity returns "done" without calling any LLM.
// The Worker replaces this with the real gateway activity at registration time.
func stubLLMActivity(_ context.Context, in StubLLMInput) (AgentMessage, error) {
	_ = in
	return AgentMessage{Role: "assistant", Content: "done"}, nil
}

// StubToolDispatchInput is the input for the stub tool-dispatch activity.
type StubToolDispatchInput struct {
	ToolName string         `json:"tool_name"`
	Input    map[string]any `json:"input"`
}

// stubToolDispatchActivity returns a canned response.
// The Worker replaces this with the real ToolRegistry-backed dispatcher.
func stubToolDispatchActivity(_ context.Context, in StubToolDispatchInput) (string, error) {
	return fmt.Sprintf("tool %q executed", in.ToolName), nil
}

// ── 3. EvalWorkflow ───────────────────────────────────────────────────────────

// EvalPair is one golden query + ground-truth chunk ID set.
type EvalPair struct {
	Query       string    `json:"query"`
	QueryVec    []float32 `json:"query_vec,omitempty"`
	RelevantIDs []string  `json:"relevant_ids"`
}

// EvalWorkflowInput is the input for EvalWorkflow.
type EvalWorkflowInput struct {
	WorkspaceID string     `json:"workspace_id"`
	Provider    string     `json:"provider"`
	Dataset     string     `json:"dataset"`
	Pairs       []EvalPair `json:"pairs"`
}

// EvalWorkflowOutput carries the computed metrics.
type EvalWorkflowOutput struct {
	Result eval.EvalResult `json:"result"`
}

// EvalWorkflow runs offline evaluation over golden query pairs.
//
// For each pair:
//  1. Child RetrieveContextWorkflow → ranked chunks.
//  2. Extract chunk IDs for recall/MRR computation.
// After all pairs: aggregate metrics → InsertEvalRunActivity → clawde_eval_runs.
//
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.EvalWorkflow.
func EvalWorkflow(ctx workflow.Context, in EvalWorkflowInput) (EvalWorkflowOutput, error) {
	if len(in.Pairs) == 0 {
		return EvalWorkflowOutput{}, fmt.Errorf("eval_workflow: no pairs provided")
	}

	retrieved := make([][]string, 0, len(in.Pairs))
	relevant := make([][]string, 0, len(in.Pairs))

	childOpts := workflow.ChildWorkflowOptions{
		TaskQueue:                TaskQueue,
		WorkflowExecutionTimeout: 60 * time.Second,
	}

	for _, pair := range in.Pairs {
		childCtx := workflow.WithChildOptions(ctx, childOpts)
		var childOut RetrieveContextWorkflowOutput
		err := workflow.ExecuteChildWorkflow(childCtx, RetrieveContextWorkflow,
			RetrieveContextWorkflowInput{
				WorkspaceID: in.WorkspaceID,
				Query:       pair.Query,
				QueryVec:    pair.QueryVec,
			},
		).Get(childCtx, &childOut)
		if err != nil {
			// Non-fatal: record a miss for this pair and continue.
			retrieved = append(retrieved, nil)
			relevant = append(relevant, pair.RelevantIDs)
			continue
		}
		ids := make([]string, len(childOut.Context.Chunks))
		for i, c := range childOut.Context.Chunks {
			ids[i] = c.ID.String()
		}
		retrieved = append(retrieved, ids)
		relevant = append(relevant, pair.RelevantIDs)
	}

	result := eval.EvalResult{
		Provider:    in.Provider,
		Dataset:     in.Dataset,
		RecallAt5:   eval.RecallAtK(retrieved, relevant, 5),
		RecallAt10:  eval.RecallAtK(retrieved, relevant, 10),
		MRRAt10:     eval.MRRAtK(retrieved, relevant, 10),
		SampleCount: len(in.Pairs),
		// P50Ms / P95Ms: not measured here (async child workflows).
	}

	// Persist to clawde_eval_runs — non-fatal on failure.
	actCtx := workflow.WithActivityOptions(ctx, fastActivityOptions())
	insertErr := workflow.ExecuteActivity(actCtx,
		"InsertEvalRunActivity",
		InsertEvalRunInput{WorkspaceID: in.WorkspaceID, Result: result},
	).Get(actCtx, nil)
	if insertErr != nil {
		workflow.GetLogger(ctx).Error("eval_workflow: insert eval run failed", "error", insertErr)
	}

	return EvalWorkflowOutput{Result: result}, nil
}

// InsertEvalRunInput is the input for InsertEvalRunActivity.
type InsertEvalRunInput struct {
	WorkspaceID string          `json:"workspace_id"`
	Result      eval.EvalResult `json:"result"`
}
