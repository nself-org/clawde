// Package hostadapter — shared HostAdapter contract for clawde-intelligence
// host integrations (OpenCode, Claude Code, ...).
//
// Purpose:    Define the canonical HostAdapter interface (ADR-008 / LEDGER §H)
//             plus the HookEvent / HookResult / AdapterConfig value types and
//             the ContextSource retrieval seam. W14-T02 (Claude Code adapter)
//             and W14-T03/T04 reuse these declarations verbatim.
// Inputs:     none (type + interface declarations only).
// Outputs:    HostAdapter, HookEvent, HookResult, AdapterConfig, ContextSource,
//             RetrievedContext, Chunk, Symbol, Finding.
// Constraints: HostAdapter has EXACTLY 6 methods (no InvalidateContextCache).
//              File ≤500 lines. Stdlib + context only.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.HostAdapter.
//        REGISTRY-PACKAGES.md → internal/hostadapter.
package hostadapter

import (
	"context"
	"time"
)

// HostAdapter is the canonical contract every host integration implements.
//
// Pinned VERBATIM per ADR-008 / LEDGER §H. EXACTLY six methods — do not add
// InvalidateContextCache or any other method without an ADR amendment.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.HostAdapter.
type HostAdapter interface {
	// Name returns the stable host identifier, e.g. "opencode".
	Name() string

	// Install wires the adapter into its host (registers tools/hooks).
	Install(ctx context.Context, cfg AdapterConfig) error

	// Uninstall removes any host-side wiring this adapter created.
	Uninstall(ctx context.Context) error

	// SessionStart is invoked at the beginning of a host session turn. It
	// fetches and injects clawde context before the first LLM call.
	SessionStart(ctx context.Context, event HookEvent) (HookResult, error)

	// SessionEnd is invoked when a host session turn completes.
	SessionEnd(ctx context.Context, event HookEvent) (HookResult, error)

	// HealthCheck verifies the adapter's downstream dependency (the
	// clawde-intelligence retrieval service) is reachable.
	HealthCheck(ctx context.Context) error
}

// HookEvent is the payload a host emits when a lifecycle hook fires.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.HookEvent.
type HookEvent struct {
	// TS is the host-supplied event timestamp.
	TS time.Time
	// Host is the host identifier, e.g. "opencode".
	Host string
	// Hook is the lifecycle hook name, e.g. "session_start".
	Hook string
	// SessionID is the host session identifier.
	SessionID string
	// WorkspaceID scopes retrieval to one workspace's index.
	WorkspaceID string
}

// HookResult is what an adapter returns from a lifecycle hook.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.HookResult.
type HookResult struct {
	// Enriched is true when context was successfully injected.
	Enriched bool
	// LatencyMs is the wall-clock time spent handling the hook.
	LatencyMs int64
	// Error carries a non-fatal warning message; empty on success. A
	// populated Error with Enriched=false is the graceful-degradation path
	// (ADR-001): the host turn proceeds without enrichment, never crashes.
	Error string
}

// AdapterConfig is the install-time configuration for an adapter.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.AdapterConfig.
type AdapterConfig struct {
	// GRPCAddr is the clawde-intelligence gRPC endpoint, default
	// "127.0.0.1:8090".
	GRPCAddr string
	// WorkspaceID is the default workspace when a HookEvent omits one.
	WorkspaceID string
	// MaxContextTokens caps the injected context block size. 0 = no cap.
	MaxContextTokens int
}

// ContextCompiler is the seam over compiler.Compiler used in adapters. It
// satisfies *compiler.Compiler in production (via compilerHook in
// compiler_hook.go); tests inject a lightweight mock without pulling in
// compiler's CGo transitive dependency chain.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.ContextCompiler.
type ContextCompiler interface {
	// PreWarmSession pre-warms the context cache for the given workspaceID
	// synchronously under a caller-supplied context (2s deadline). It MUST
	// never block longer than that — graceful-degrade only.
	// Returns true when the compile produced enriched context.
	PreWarmSession(ctx context.Context, workspaceID string) bool
}

// ContextSource is the retrieval seam an adapter depends on. The production
// implementation is a gRPC client to 127.0.0.1:8090; tests inject an
// in-process mock. This indirection keeps the adapter unit-testable without a
// live server (ADR-001 graceful-degradation is expressed as a returned error).
type ContextSource interface {
	// Retrieve fetches relevant context for a query within a workspace.
	// A non-nil error signals the source is unavailable; the caller MUST
	// degrade gracefully and never panic.
	Retrieve(ctx context.Context, workspaceID, query string) (*RetrievedContext, error)
	// Ping verifies the source is reachable (drives HealthCheck).
	Ping(ctx context.Context) error
}

// RetrievedContext is the host-agnostic result the ContextSource returns. It
// mirrors retrieval.RetrievalContext plus static-analysis findings surfaced by
// the gRPC layer.
type RetrievedContext struct {
	Chunks   []Chunk
	Symbols  []Symbol
	Findings []Finding
}

// Chunk is a single retrieved code chunk.
type Chunk struct {
	FilePath  string
	LineStart int
	Lang      string
	Content   string
}

// Symbol is a matched symbol definition.
type Symbol struct {
	Name      string
	Kind      string
	Signature string
	FilePath  string
}

// Finding is a static-analysis result relevant to the query.
type Finding struct {
	Rule     string
	Severity string
	FilePath string
	Line     int
	Message  string
}
