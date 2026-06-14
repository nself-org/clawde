// Package orchestration — Temporal worker + tool registry for durable agent execution.
//
// Purpose: Central registry for all Temporal Activities exposed as "tools" to the
//          AgentRunWorkflow. Supports built-in tools (retrieve_context, run_analysis,
//          list_symbols, get_file_content, execute_shell) and custom extension tools
//          registered by callers.
//
// Inputs:  tool name string → Activity function.
// Outputs: Activity function looked up by name; error when tool is unknown.
// Constraints: File ≤500 lines. Registry is not safe for concurrent mutation after
//              the worker starts — register all tools before calling worker.Start().
//
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.ToolRegistry, orchestration.RegisterTool,
//        orchestration.GetActivity.
package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
)

// BuiltInTool constants are the canonical names for built-in activities.
const (
	ToolRetrieveContext = "retrieve_context"
	ToolRunAnalysis     = "run_analysis"
	ToolListSymbols     = "list_symbols"
	ToolGetFileContent  = "get_file_content"
	ToolExecuteShell    = "execute_shell"
)

// ActivityFunc is the type signature for a Temporal activity function.
// Using any here keeps the registry decoupled from the concrete activity structs
// (Temporal's RegisterActivity accepts any function via reflection).
type ActivityFunc = any

// ToolDispatchFn is the invocation signature used by ToolDispatchActivity.
// It receives the raw tool input as a JSON-round-trip-compatible map and returns
// a string result suitable for inclusion in the agent's conversation history.
//
// Purpose: Decouple ToolDispatchActivity from the concrete input types of each
//          tool without reflection. Every built-in tool registered via
//          NewToolRegistry gets a corresponding ToolDispatchFn that marshals the
//          map[string]any input → typed struct → calls the real activity method.
type ToolDispatchFn func(ctx context.Context, input map[string]any) (string, error)

// ToolRegistry maps tool names to Temporal activity functions and dispatch handlers.
//
// Purpose: Single lookup table so AgentRunWorkflow can dispatch tool calls by name
//          without hard-coding each tool. Supports custom extension tools added at
//          startup via RegisterTool.
// Invariants: all built-in tools are pre-registered by NewToolRegistry.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.ToolRegistry.
type ToolRegistry struct {
	tools    map[string]ActivityFunc
	handlers map[string]ToolDispatchFn
}

// NewToolRegistry creates a ToolRegistry pre-loaded with all built-in activities.
// Pass an Activities instance so the built-ins are bound to their dependencies.
//
// Each built-in tool registers both:
//   - An ActivityFunc for Temporal worker registration (w.RegisterActivityWithOptions).
//   - A ToolDispatchFn for ToolDispatchActivity — converts map[string]any input to
//     the typed struct, calls the real method, and returns the string result.
func NewToolRegistry(acts *Activities) *ToolRegistry {
	r := &ToolRegistry{
		tools:    make(map[string]ActivityFunc),
		handlers: make(map[string]ToolDispatchFn),
	}

	// ── retrieve_context ─────────────────────────────────────────────────────
	r.tools[ToolRetrieveContext] = acts.RetrieveContextActivity
	r.handlers[ToolRetrieveContext] = func(ctx context.Context, input map[string]any) (string, error) {
		var in RetrieveContextInput
		if err := mapToStruct(input, &in); err != nil {
			return "", fmt.Errorf("retrieve_context: decode input: %w", err)
		}
		out, err := acts.RetrieveContextActivity(ctx, in)
		if err != nil {
			return "", fmt.Errorf("retrieve_context: %w", err)
		}
		return structToString(out)
	}

	// ── run_analysis ─────────────────────────────────────────────────────────
	r.tools[ToolRunAnalysis] = acts.RunAnalysisActivity
	r.handlers[ToolRunAnalysis] = func(ctx context.Context, input map[string]any) (string, error) {
		var in RunAnalysisInput
		if err := mapToStruct(input, &in); err != nil {
			return "", fmt.Errorf("run_analysis: decode input: %w", err)
		}
		if err := acts.RunAnalysisActivity(ctx, in); err != nil {
			return "", fmt.Errorf("run_analysis: %w", err)
		}
		return "analysis complete", nil
	}

	// ── list_symbols ─────────────────────────────────────────────────────────
	r.tools[ToolListSymbols] = acts.ListSymbolsActivity
	r.handlers[ToolListSymbols] = func(ctx context.Context, input map[string]any) (string, error) {
		var in ListSymbolsInput
		if err := mapToStruct(input, &in); err != nil {
			return "", fmt.Errorf("list_symbols: decode input: %w", err)
		}
		out, err := acts.ListSymbolsActivity(ctx, in)
		if err != nil {
			return "", fmt.Errorf("list_symbols: %w", err)
		}
		return structToString(out)
	}

	// ── get_file_content ─────────────────────────────────────────────────────
	r.tools[ToolGetFileContent] = acts.GetFileContentActivity
	r.handlers[ToolGetFileContent] = func(ctx context.Context, input map[string]any) (string, error) {
		var in GetFileContentInput
		if err := mapToStruct(input, &in); err != nil {
			return "", fmt.Errorf("get_file_content: decode input: %w", err)
		}
		out, err := acts.GetFileContentActivity(ctx, in)
		if err != nil {
			return "", fmt.Errorf("get_file_content: %w", err)
		}
		return out.Content, nil
	}

	// ── execute_shell ─────────────────────────────────────────────────────────
	r.tools[ToolExecuteShell] = acts.ExecuteShellActivity
	r.handlers[ToolExecuteShell] = func(ctx context.Context, input map[string]any) (string, error) {
		var in ExecuteShellInput
		if err := mapToStruct(input, &in); err != nil {
			return "", fmt.Errorf("execute_shell: decode input: %w", err)
		}
		out, err := acts.ExecuteShellActivity(ctx, in)
		if err != nil {
			return "", fmt.Errorf("execute_shell: %w", err)
		}
		return structToString(out)
	}

	return r
}

// RegisterTool adds or replaces a tool in the registry.
//
// Purpose: Extension point so callers can add custom activities (e.g., a project-
//          specific linter, a custom formatter) without forking this package.
// Inputs:  name (must be non-empty), fn (any valid Temporal activity function).
//          handler (optional): if non-nil, also registers a dispatch handler for
//          ToolDispatchActivity. Pass nil to register only for Temporal (no
//          agent-loop dispatch support).
// Outputs: error if name is empty.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.RegisterTool.
func (r *ToolRegistry) RegisterTool(name string, fn ActivityFunc, handler ...ToolDispatchFn) error {
	if name == "" {
		return fmt.Errorf("orchestration: tool name must not be empty")
	}
	r.tools[name] = fn
	if len(handler) > 0 && handler[0] != nil {
		r.handlers[name] = handler[0]
	}
	return nil
}

// GetActivity looks up a tool by name.
//
// Purpose: Called by AgentRunWorkflow to resolve a tool name from an LLM tool-call
//          into the activity function that Temporal should execute.
// Inputs:  tool name string.
// Outputs: ActivityFunc, nil on success; nil, error when tool is unknown.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.GetActivity.
func (r *ToolRegistry) GetActivity(name string) (ActivityFunc, error) {
	fn, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("orchestration: unknown tool %q", name)
	}
	return fn, nil
}

// GetDispatchHandler looks up the ToolDispatchFn for a tool.
//
// Purpose: Called by ToolDispatchActivity to resolve a tool name into its
//          dispatch-capable handler. Only tools registered with a handler
//          (all built-ins + custom tools registered with handler arg) are
//          reachable via agent-loop dispatch.
// Inputs:  tool name string.
// Outputs: ToolDispatchFn, nil on success; nil, error when tool is unknown or
//          has no dispatch handler.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.GetDispatchHandler.
func (r *ToolRegistry) GetDispatchHandler(name string) (ToolDispatchFn, error) {
	h, ok := r.handlers[name]
	if !ok {
		return nil, fmt.Errorf("orchestration: unknown tool %q (no dispatch handler registered)", name)
	}
	return h, nil
}

// ── JSON bridge helpers ───────────────────────────────────────────────────────

// mapToStruct converts a map[string]any to a typed struct via JSON round-trip.
// This bridges the untyped tool input from StubToolDispatchInput.Input to
// the typed input structs expected by each activity.
func mapToStruct(m map[string]any, dst any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal input: %w", err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("unmarshal input: %w", err)
	}
	return nil
}

// structToString serialises any value to a compact JSON string.
// Used by ToolDispatchFns to produce a human/LLM-readable result string.
func structToString(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(b), nil
}

// RegisteredTools returns a sorted list of all registered tool names.
// Used by AgentRunWorkflow to populate the tool description sent to the LLM.
func (r *ToolRegistry) RegisteredTools() []string {
	names := make([]string, 0, len(r.tools))
	for k := range r.tools {
		names = append(names, k)
	}
	// deterministic order for prompt generation
	sortStrings(names)
	return names
}

// sortStrings sorts a string slice in-place (insertion sort — small N, no import).
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}
