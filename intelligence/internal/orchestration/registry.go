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

// ToolRegistry maps tool names to Temporal activity functions.
//
// Purpose: Single lookup table so AgentRunWorkflow can dispatch tool calls by name
//          without hard-coding each tool. Supports custom extension tools added at
//          startup via RegisterTool.
// Invariants: all built-in tools are pre-registered by NewToolRegistry.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.ToolRegistry.
type ToolRegistry struct {
	tools map[string]ActivityFunc
}

// NewToolRegistry creates a ToolRegistry pre-loaded with all built-in activities.
// Pass an Activities instance so the built-ins are bound to their dependencies.
func NewToolRegistry(acts *Activities) *ToolRegistry {
	r := &ToolRegistry{tools: make(map[string]ActivityFunc)}
	r.tools[ToolRetrieveContext] = acts.RetrieveContextActivity
	r.tools[ToolRunAnalysis] = acts.RunAnalysisActivity
	r.tools[ToolListSymbols] = acts.ListSymbolsActivity
	r.tools[ToolGetFileContent] = acts.GetFileContentActivity
	r.tools[ToolExecuteShell] = acts.ExecuteShellActivity
	return r
}

// RegisterTool adds or replaces a tool in the registry.
//
// Purpose: Extension point so callers can add custom activities (e.g., a project-
//          specific linter, a custom formatter) without forking this package.
// Inputs:  name (must be non-empty), fn (any valid Temporal activity function).
// Outputs: error if name is empty.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.RegisterTool.
func (r *ToolRegistry) RegisterTool(name string, fn ActivityFunc) error {
	if name == "" {
		return fmt.Errorf("orchestration: tool name must not be empty")
	}
	r.tools[name] = fn
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
