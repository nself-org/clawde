// worker.go — Temporal Worker factory for the clawde-intelligence task queue.
//
// Purpose: Construct and configure a Temporal Worker that registers all workflows
//          and activities so they are discoverable from any Temporal client.
//
// The task queue name is "clawde-intelligence" per canonical spec.
// TEMPORAL_HOST_URL defaults to localhost:7233.
// TEMPORAL_NAMESPACE defaults to "clawde".
//
// Constraints: File ≤500 lines. No side effects at package init — call NewWorker
//              explicitly.
//
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.NewWorker, orchestration.TaskQueue.
//        REGISTRY-SERVICES.md → Temporal CS_N.
package orchestration

import (
	"fmt"
	"os"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// TaskQueue is the canonical Temporal task queue name for clawde-intelligence.
const TaskQueue = "clawde-intelligence"

// DefaultTemporalHost is the default Temporal frontend address.
const DefaultTemporalHost = "localhost:7233"

// DefaultNamespace is the default Temporal namespace for clawde.
const DefaultNamespace = "clawde"

// NewTemporalClient creates a Temporal client from environment variables.
//
//   - TEMPORAL_HOST_URL — host:port; defaults to localhost:7233.
//   - TEMPORAL_NAMESPACE — defaults to "clawde".
//
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.NewTemporalClient.
func NewTemporalClient() (client.Client, error) {
	host := os.Getenv("TEMPORAL_HOST_URL")
	if host == "" {
		host = DefaultTemporalHost
	}
	namespace := os.Getenv("TEMPORAL_NAMESPACE")
	if namespace == "" {
		namespace = DefaultNamespace
	}

	c, err := client.Dial(client.Options{
		HostPort:  host,
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("orchestration: dial temporal at %s (namespace=%s): %w", host, namespace, err)
	}
	return c, nil
}

// NewWorker creates a Temporal Worker bound to the clawde-intelligence task queue.
//
// Purpose: Single factory that registers ALL workflows and activities so callers
//          do not need to import sub-packages or enumerate registrations manually.
//
// Inputs:
//   - c:       Temporal client (from NewTemporalClient or injected in tests).
//   - acts:    Activities bundle carrying all activity implementations.
//   - reg:     ToolRegistry for resolving tool names in AgentRunWorkflow.
//   - opts:    optional worker.Options; zero value uses Temporal defaults.
//
// Outputs: worker.Worker ready to call Start() on.
// SPORT: REGISTRY-FUNCTIONS.md → orchestration.NewWorker.
func NewWorker(c client.Client, acts *Activities, reg *ToolRegistry, opts worker.Options) worker.Worker {
	w := worker.New(c, TaskQueue, opts)

	// ── Register Workflows ────────────────────────────────────────────────────
	w.RegisterWorkflow(RetrieveContextWorkflow)
	w.RegisterWorkflow(AgentRunWorkflow)
	w.RegisterWorkflow(EvalWorkflow)

	// ── Register built-in Activities ─────────────────────────────────────────
	// RegisterActivity accepts a struct pointer — all exported methods on *Activities
	// are registered as activities under their Go method name (e.g. "RetrieveContextActivity",
	// "LLMCallActivity", "ToolDispatchActivity").
	w.RegisterActivity(acts)

	// ── Register all ToolRegistry activities ─────────────────────────────────
	// Each tool in the registry is also registered as a named activity so
	// external callers can dispatch individual tools without going through
	// the AgentRunWorkflow multi-turn loop.
	for _, name := range reg.RegisteredTools() {
		fn, err := reg.GetActivity(name)
		if err != nil {
			continue // should not happen; registry was just built
		}
		w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}

	return w
}
