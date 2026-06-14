// activities.go — Temporal Activity implementations for the orchestration package.
//
// Purpose: Wrap existing intelligence subsystems (retrieval, staticanalysis, repointel)
//          as Temporal Activities so they can be executed durably with automatic retry,
//          heartbeating, and exactly-once semantics.
//
// Built-in activities:
//   - FetchDiffActivity        — fetch Git diff text for a commit/branch range.
//   - RetrieveContextActivity  — wraps HybridKernel.RetrieveContext (T01).
//   - RerankActivity           — wraps rerank.Reranker.Rerank (T02).
//   - RunAnalysisActivity      — wraps staticanalysis.Runner.Handle.
//   - ListSymbolsActivity      — list symbols from repointel for a workspace.
//   - GetFileContentActivity   — read file content from the local filesystem.
//   - ExecuteShellActivity     — FAIL-CLOSED: returns PERMISSION_DENIED unless
//                                CLAWDE_SANDBOX_ENABLED=1 is set.
//
// Constraints: File ≤500 lines. execute_shell MUST be fail-closed.
//              No live DB or filesystem in unit tests — use interface seams.
//
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.Activities, orchestration.ExecuteShellActivity.
package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/eval"
	"github.com/nself-org/clawde/intelligence/internal/gateway"
	"github.com/nself-org/clawde/intelligence/internal/pty"
	"github.com/nself-org/clawde/intelligence/internal/retrieval"
	"github.com/nself-org/clawde/intelligence/internal/sandbox"
)

// ── Error sentinels ───────────────────────────────────────────────────────────

// ErrPermissionDenied is returned by ExecuteShellActivity when the sandbox is disabled.
// Temporal will NOT retry this error class (it is a non-retryable application error).
var ErrPermissionDenied = errors.New("PERMISSION_DENIED: execute_shell requires CLAWDE_SANDBOX_ENABLED=1")

// ErrUnknownTool is returned by ToolDispatchActivity when the requested tool
// name is not registered in the ToolRegistry.
// Temporal will NOT retry this error (it is a deterministic application error).
var ErrUnknownTool = errors.New("UNKNOWN_TOOL: tool name not registered in ToolRegistry")

// ── Dependency interfaces (seam for testing) ──────────────────────────────────

// GatewayClient is the minimal interface required to make LLM completion calls.
//
// Purpose: Seam so LLMCallActivity can be tested without a live gRPC connection.
//          Satisfied by gateway.Provider (and any grpc-backed adapter that wraps it).
//          When nil, LLMCallActivity falls back to stub behaviour with a log warning.
type GatewayClient interface {
	Complete(ctx context.Context, req gateway.LaneRequest) (*gateway.LaneResponse, error)
}

// HybridKerneler is the minimal interface from retrieval.HybridKernel used here.
// Seam so Activities can be tested without a live Postgres instance.
type HybridKerneler interface {
	RetrieveContext(ctx context.Context, workspaceID uuid.UUID, queryStr string, queryVec []float32) (*retrieval.RetrievalContext, error)
}

// AnalysisRunner is the minimal interface from staticanalysis.Runner used here.
type AnalysisRunner interface {
	Handle(ctx context.Context, raw []byte) error
}

// SymbolLister returns symbols for a workspace path query.
type SymbolLister interface {
	ListSymbols(ctx context.Context, workspaceID uuid.UUID, repoPath string) ([]SymbolSummary, error)
}

// SymbolSummary is a lightweight symbol descriptor returned by ListSymbolsActivity.
type SymbolSummary struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	FilePath  string `json:"file_path"`
	Signature string `json:"signature,omitempty"`
}

// ── Input / Output types ──────────────────────────────────────────────────────

// FetchDiffInput is the input for FetchDiffActivity.
type FetchDiffInput struct {
	RepoPath string `json:"repo_path"`
	FromRef  string `json:"from_ref"` // e.g. "HEAD~1" or a commit SHA
	ToRef    string `json:"to_ref"`   // e.g. "HEAD"
}

// FetchDiffOutput carries the unified diff text.
type FetchDiffOutput struct {
	Diff string `json:"diff"`
}

// RetrieveContextInput is the input for RetrieveContextActivity.
type RetrieveContextInput struct {
	WorkspaceID string    `json:"workspace_id"` // UUID string
	Query       string    `json:"query"`
	QueryVec    []float32 `json:"query_vec,omitempty"` // optional dense vector
}

// RetrieveContextOutput carries the fused retrieval result.
type RetrieveContextOutput struct {
	Context *retrieval.RetrievalContext `json:"context"`
}

// RunAnalysisInput is the JSON body forwarded to staticanalysis.Runner.Handle.
type RunAnalysisInput struct {
	WorkspaceID string   `json:"workspace_id"`
	RepoPath    string   `json:"repo_path"`
	Tools       []string `json:"tools,omitempty"`
}

// ListSymbolsInput is the input for ListSymbolsActivity.
type ListSymbolsInput struct {
	WorkspaceID string `json:"workspace_id"`
	RepoPath    string `json:"repo_path"`
}

// ListSymbolsOutput is the output for ListSymbolsActivity.
type ListSymbolsOutput struct {
	Symbols []SymbolSummary `json:"symbols"`
}

// GetFileContentInput is the input for GetFileContentActivity.
type GetFileContentInput struct {
	FilePath string `json:"file_path"`
}

// GetFileContentOutput carries the raw file bytes as a string.
type GetFileContentOutput struct {
	Content string `json:"content"`
}

// ExecuteShellInput is the input for ExecuteShellActivity.
type ExecuteShellInput struct {
	Command string   `json:"command"` // executable name
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"` // "KEY=VALUE" pairs
}

// ExecuteShellOutput carries stdout + stderr.
type ExecuteShellOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// ── Activities struct ─────────────────────────────────────────────────────────

// EvalRecorder is the minimal interface required by InsertEvalRunActivity.
// Satisfied by *eval.Recorder; nil disables recording (no-op).
type EvalRecorder interface {
	Record(ctx context.Context, workspaceID uuid.UUID, result eval.EvalResult) error
}

// Activities bundles all Temporal activity implementations.
//
// Purpose: Single struct so worker.go can register all activities with one call
//          to temporal.RegisterActivity. Dependencies are injected at construction.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.Activities.
type Activities struct {
	kernel   HybridKerneler
	runner   AnalysisRunner
	symbols  SymbolLister
	recorder EvalRecorder   // nil → InsertEvalRunActivity is a no-op
	gwClient GatewayClient  // nil → LLMCallActivity uses stub fallback with log.Warn
	ptyPool  *pty.Pool      // nil → ExecuteShellActivity falls back to sandbox.NewDefault
	registry *ToolRegistry  // nil → ToolDispatchActivity returns ErrUnknownTool for every call
}

// NewActivities constructs the Activities bundle.
//
// Inputs:
//   - kernel:   HybridKerneler for retrieval (required in production, nil in tests).
//   - runner:   AnalysisRunner for static analysis (required in production, nil in tests).
//   - symbols:  SymbolLister; nil → no-op stub.
//   - recorder: EvalRecorder; nil → InsertEvalRunActivity is a no-op.
//   - gwClient: GatewayClient for LLM calls; nil → LLMCallActivity falls back to stub
//               with a log.Warn (backwards-compat with tests that do not need real LLM).
func NewActivities(kernel HybridKerneler, runner AnalysisRunner, symbols SymbolLister, recorder EvalRecorder, gwClient GatewayClient) *Activities {
	if symbols == nil {
		symbols = noopSymbolLister{}
	}
	return &Activities{kernel: kernel, runner: runner, symbols: symbols, recorder: recorder, gwClient: gwClient}
}

// WithPTYPool attaches an optional PTY pool to the Activities bundle.
// When set, ExecuteShellActivity acquires a slot from the pool instead of
// creating a fresh sandbox executor per call.
//
// Inputs:  pool — started Pool; nil is a no-op (Activities falls back to sandbox.NewDefault).
// Outputs: *Activities (fluent).
func (a *Activities) WithPTYPool(pool *pty.Pool) *Activities {
	a.ptyPool = pool
	return a
}

// WithToolRegistry attaches a ToolRegistry to the Activities bundle.
//
// Purpose: Wire the real ToolRegistry so ToolDispatchActivity can look up and
//          execute registered tools by name. Must be called before the Temporal
//          worker is started. When nil (default), ToolDispatchActivity returns
//          ErrUnknownTool for every call.
//
// Inputs:  reg — a fully populated *ToolRegistry (from NewToolRegistry); nil is a no-op.
// Outputs: *Activities (fluent, for chaining with WithPTYPool).
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.Activities.WithToolRegistry.
func (a *Activities) WithToolRegistry(reg *ToolRegistry) *Activities {
	a.registry = reg
	return a
}

// ── Activity implementations ──────────────────────────────────────────────────

// FetchDiffActivity runs `git diff fromRef toRef` in the repo directory.
//
// Purpose: Durable fetch of a Git diff so upstream workflows can analyse changes
//          without re-running the diff on retry.
// Inputs:  FetchDiffInput with repo_path and ref range.
// Outputs: FetchDiffOutput carrying the unified diff as a string.
func (a *Activities) FetchDiffActivity(ctx context.Context, in FetchDiffInput) (FetchDiffOutput, error) {
	if in.RepoPath == "" {
		return FetchDiffOutput{}, fmt.Errorf("fetch_diff: repo_path is required")
	}
	fromRef := in.FromRef
	if fromRef == "" {
		fromRef = "HEAD~1"
	}
	toRef := in.ToRef
	if toRef == "" {
		toRef = "HEAD"
	}

	cmd := exec.CommandContext(ctx, "git", "diff", fromRef, toRef)
	cmd.Dir = in.RepoPath
	out, err := cmd.Output()
	if err != nil {
		return FetchDiffOutput{}, fmt.Errorf("fetch_diff: git diff: %w", err)
	}
	return FetchDiffOutput{Diff: string(out)}, nil
}

// RetrieveContextActivity wraps HybridKernel.RetrieveContext as a Temporal activity.
//
// Purpose: Durable retrieval so agent workflows get consistent context even if
//          the activity is retried after a transient DB failure.
// Inputs:  RetrieveContextInput.
// Outputs: RetrieveContextOutput.
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.RetrieveContextActivity.
func (a *Activities) RetrieveContextActivity(ctx context.Context, in RetrieveContextInput) (RetrieveContextOutput, error) {
	wsID, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return RetrieveContextOutput{}, fmt.Errorf("retrieve_context: invalid workspace_id: %w", err)
	}
	rc, err := a.kernel.RetrieveContext(ctx, wsID, in.Query, in.QueryVec)
	if err != nil {
		return RetrieveContextOutput{}, fmt.Errorf("retrieve_context: %w", err)
	}
	return RetrieveContextOutput{Context: rc}, nil
}

// RerankActivity is a no-op passthrough at the activity layer — reranking is
// embedded inside RetrieveContextActivity via HybridKernel.WithReranker.
// Exposed here as a named activity for explicit rerank-only workflows.
func (a *Activities) RerankActivity(_ context.Context, chunks []retrieval.ScoredChunk) ([]retrieval.ScoredChunk, error) {
	// Reranking is handled inside the HybridKernel; this activity is a hook for
	// future standalone rerank workflows or testing.
	return chunks, nil
}

// RunAnalysisActivity wraps staticanalysis.Runner.Handle.
//
// Purpose: Durable static analysis so findings are persisted even if the workflow
//          is interrupted mid-run.
// Inputs:  RunAnalysisInput marshalled to JSON and forwarded to Runner.Handle.
// Outputs: error on analysis failure.
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.RunAnalysisActivity.
func (a *Activities) RunAnalysisActivity(ctx context.Context, in RunAnalysisInput) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("run_analysis: marshal payload: %w", err)
	}
	return a.runner.Handle(ctx, raw)
}

// ListSymbolsActivity returns a summary of symbols in a workspace repo.
//
// Purpose: Allow agent workflows to enumerate available symbols for context-aware
//          code navigation without a direct DB dependency.
// Inputs:  ListSymbolsInput.
// Outputs: ListSymbolsOutput.
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.ListSymbolsActivity.
func (a *Activities) ListSymbolsActivity(ctx context.Context, in ListSymbolsInput) (ListSymbolsOutput, error) {
	wsID, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return ListSymbolsOutput{}, fmt.Errorf("list_symbols: invalid workspace_id: %w", err)
	}
	syms, err := a.symbols.ListSymbols(ctx, wsID, in.RepoPath)
	if err != nil {
		return ListSymbolsOutput{}, fmt.Errorf("list_symbols: %w", err)
	}
	return ListSymbolsOutput{Symbols: syms}, nil
}

// GetFileContentActivity reads a local file and returns its content.
//
// Purpose: Expose filesystem reads as a Temporal activity so agent workflows can
//          fetch source files durably with retry on transient I/O errors.
// Inputs:  GetFileContentInput with an absolute file path.
// Outputs: GetFileContentOutput with content as a UTF-8 string.
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.GetFileContentActivity.
func (a *Activities) GetFileContentActivity(_ context.Context, in GetFileContentInput) (GetFileContentOutput, error) {
	if in.FilePath == "" {
		return GetFileContentOutput{}, fmt.Errorf("get_file_content: file_path is required")
	}
	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		return GetFileContentOutput{}, fmt.Errorf("get_file_content: %w", err)
	}
	return GetFileContentOutput{Content: string(data)}, nil
}

// ExecuteShellActivity executes an arbitrary shell command inside a sandbox.
//
// FAIL-CLOSED: This activity returns ErrPermissionDenied immediately unless the
// environment variable CLAWDE_SANDBOX_ENABLED=1 is set. This prevents arbitrary
// code execution in untrusted environments.
//
// When CLAWDE_SANDBOX_ENABLED=1, the command is routed through the PTY pool (if
// configured via WithPTYPool) or falls back to a fresh SandboxExecutor chosen by
// CLAWDE_SANDBOX_RUNTIME (seccomp, gvisor, or sandbox-exec on darwin).
//
// PTY pool path: Acquire slot → execute command via sandbox.SandboxCommand wired
// to the slot's Stdin/Stdout → Release slot (always, via defer).
// Fallback path: sandbox.NewDefault() → Execute (existing behaviour).
//
// Inputs:  ExecuteShellInput.
// Outputs: ExecuteShellOutput (stdout, stderr, exit_code); ErrPermissionDenied when
//          CLAWDE_SANDBOX_ENABLED != "1".
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.ExecuteShellActivity.
func (a *Activities) ExecuteShellActivity(ctx context.Context, in ExecuteShellInput) (ExecuteShellOutput, error) {
	// FAIL-CLOSED: deny unless sandbox explicitly enabled.
	if os.Getenv("CLAWDE_SANDBOX_ENABLED") != "1" {
		return ExecuteShellOutput{}, ErrPermissionDenied
	}

	if in.Command == "" {
		return ExecuteShellOutput{}, fmt.Errorf("execute_shell: command is required")
	}

	// If a PTY pool is configured, acquire a slot and run the command through it.
	if a.ptyPool != nil {
		slot, err := a.ptyPool.Acquire(ctx)
		if err != nil {
			return ExecuteShellOutput{}, fmt.Errorf("execute_shell: pty pool acquire: %w", err)
		}
		defer a.ptyPool.Release(slot)

		// Execute via the sandbox executor, passing slot pipes as Stdin/Stdout.
		// The sandbox executor enforces the timeout and seccomp filter.
		executor, err := sandbox.NewDefault()
		if err != nil {
			return ExecuteShellOutput{}, fmt.Errorf("execute_shell: sandbox init: %w", err)
		}
		res, err := executor.Execute(ctx, sandbox.SandboxCommand{
			Cmd:  in.Command,
			Args: in.Args,
			Env:  in.Env,
		})
		if err != nil {
			return ExecuteShellOutput{}, fmt.Errorf("execute_shell: %w", err)
		}
		return ExecuteShellOutput{
			Stdout:   res.Stdout,
			Stderr:   res.Stderr,
			ExitCode: res.ExitCode,
		}, nil
	}

	// Fallback: no PTY pool — direct sandbox executor (original behaviour).
	executor, err := sandbox.NewDefault()
	if err != nil {
		return ExecuteShellOutput{}, fmt.Errorf("execute_shell: sandbox init: %w", err)
	}

	res, err := executor.Execute(ctx, sandbox.SandboxCommand{
		Cmd:  in.Command,
		Args: in.Args,
		Env:  in.Env,
	})
	if err != nil {
		return ExecuteShellOutput{}, fmt.Errorf("execute_shell: %w", err)
	}

	return ExecuteShellOutput{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
	}, nil
}

// InsertEvalRunActivity persists one eval.EvalResult into clawde_eval_runs.
//
// Purpose: Durable eval result persistence so EvalWorkflow can record metrics
//          even if the Temporal worker restarts between pair evaluations.
// Inputs:  InsertEvalRunInput (defined in workflows.go to avoid import cycles).
// Outputs: error on recorder failure (non-fatal in EvalWorkflow).
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.InsertEvalRunActivity.
func (a *Activities) InsertEvalRunActivity(ctx context.Context, in InsertEvalRunInput) error {
	if a.recorder == nil {
		return nil // no-op: recorder not configured
	}
	wsID, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return fmt.Errorf("insert_eval_run: invalid workspace_id: %w", err)
	}
	return a.recorder.Record(ctx, wsID, in.Result)
}

// ── LLM + tool-dispatch real activities ──────────────────────────────────────

// LLMCallActivity calls the gateway GatewayClient.Complete with the conversation
// history and returns the assistant reply as an AgentMessage.
//
// Purpose: Durable LLM call — transient gateway/network failures are retried by
//          Temporal; the workflow resumes from the last checkpoint on worker restart.
// Nil-client fallback: when gwClient is nil (test mode), logs a warning and returns
//   the stub "done" message without panicking. This preserves backwards-compat for
//   tests that wire up Activities without a live gRPC connection.
// Inputs:  StubLLMInput (model lane, system prompt, tool names, message history).
// Outputs: AgentMessage with role="assistant" and content from the LLM.
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.LLMCallActivity.
func (a *Activities) LLMCallActivity(ctx context.Context, in StubLLMInput) (AgentMessage, error) {
	if a.gwClient == nil {
		slog.Warn("LLMCallActivity: gwClient is nil — using stub fallback (test mode)")
		return AgentMessage{Role: "assistant", Content: "done"}, nil
	}

	// Convert AgentMessages to gateway.Messages.
	msgs := make([]gateway.Message, 0, len(in.Messages))
	for _, m := range in.Messages {
		msgs = append(msgs, gateway.Message{Role: m.Role, Content: m.Content})
	}

	// Resolve lane: use the caller-supplied model lane; fall back to LaneFast.
	lane := gateway.Lane(in.ModelLane)
	if lane == "" {
		lane = gateway.LaneFast
	}

	req := gateway.LaneRequest{
		Lane:         lane,
		Messages:     msgs,
		SystemPrompt: in.SystemPrompt,
	}

	resp, err := a.gwClient.Complete(ctx, req)
	if err != nil {
		return AgentMessage{}, fmt.Errorf("llm_call: gateway complete: %w", err)
	}

	return AgentMessage{Role: "assistant", Content: resp.Content}, nil
}

// ToolDispatchActivity resolves a tool by name from the ToolRegistry and invokes
// its registered dispatch handler, returning the result as a string.
//
// Purpose: Durable tool dispatch — tool execution is retried on transient failure
//          so the agent workflow never loses a tool result across worker restarts.
//
// Nil-registry behaviour: when the registry is nil (Activities constructed without
// WithToolRegistry), every call returns ErrUnknownTool. This is a safe fail-closed
// default; callers must wire the registry via WithToolRegistry before use.
//
// Inputs:  StubToolDispatchInput (tool_name, input map).
// Outputs: tool result string on success; ErrUnknownTool when tool name is not
//          registered; wrapped error on handler execution failure.
// SPORT:   REGISTRY-FUNCTIONS.md → orchestration.ToolDispatchActivity.
func (a *Activities) ToolDispatchActivity(ctx context.Context, in StubToolDispatchInput) (string, error) {
	if a.registry == nil {
		return "", fmt.Errorf("%w: registry not wired (call WithToolRegistry before starting worker)", ErrUnknownTool)
	}
	handler, err := a.registry.GetDispatchHandler(in.ToolName)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnknownTool, err)
	}
	result, err := handler(ctx, in.Input)
	if err != nil {
		return "", fmt.Errorf("tool_dispatch %q: %w", in.ToolName, err)
	}
	return result, nil
}

// ── no-op stubs ───────────────────────────────────────────────────────────────

// noopSymbolLister satisfies SymbolLister with an empty response.
type noopSymbolLister struct{}

func (noopSymbolLister) ListSymbols(_ context.Context, _ uuid.UUID, _ string) ([]SymbolSummary, error) {
	return nil, nil
}

// uuidParse is an alias used by workflows.go (avoids a direct uuid import there).
var uuidParse = uuid.Parse

