// Package hostadapter — OpenCode native adapter.
//
// Purpose:    OpenCodeAdapter implements HostAdapter for the OpenCode (OC) host.
//             It exposes a retrieve_context tool that pulls context from
//             clawde-intelligence (gRPC 127.0.0.1:8090 via ContextSource) and
//             injects a <clawde_context> block before the first LLM call of an
//             OC session turn.
// Inputs:     ContextSource (gRPC client or in-process mock), AdapterConfig,
//             HookEvent per turn.
// Outputs:    HookResult{Enriched, LatencyMs, Error}; the formatted context
//             block via RetrieveContextTool.
// Constraints: EXACTLY the 6 HostAdapter methods are implemented. Graceful
//              degradation (ADR-001): source down → Enriched:false + warning,
//              never a crash. AllowAll policy stub per ADR-003. File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.OpenCodeAdapter.
//        REGISTRY-PACKAGES.md → internal/hostadapter.
package hostadapter

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// hostName is the stable identifier OpenCode emits in audit logs and HookEvents.
const hostName = "opencode"

// OpenCodeAdapter is the OpenCode host integration.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.OpenCodeAdapter.
type OpenCodeAdapter struct {
	source   ContextSource
	compiler ContextCompiler
	cfg      AdapterConfig
	logger   *slog.Logger
	// now is injectable for deterministic latency assertions in tests.
	now func() time.Time
}

// NewOpenCodeAdapter constructs an OpenCodeAdapter over the given ContextSource
// and an optional compiler hook (nil is safe — graceful-degrade per ADR-001).
// In production pass NewCompilerHook(comp); tests may inject any ContextCompiler
// mock. A nil logger falls back to a stderr JSON logger so audit records are
// never silently dropped.
func NewOpenCodeAdapter(source ContextSource, comp ContextCompiler, logger *slog.Logger) *OpenCodeAdapter {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return &OpenCodeAdapter{
		source:   source,
		compiler: comp,
		logger:   logger,
		now:      time.Now,
	}
}

// Name implements HostAdapter. Returns "opencode".
func (a *OpenCodeAdapter) Name() string { return hostName }

// Install implements HostAdapter. Records config and registers the
// retrieve_context tool with the host (registration is a no-op seam here; the
// OC runtime discovers the tool via RetrieveContextTool).
func (a *OpenCodeAdapter) Install(ctx context.Context, cfg AdapterConfig) error {
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:8090"
	}
	a.cfg = cfg
	a.logger.InfoContext(ctx, "opencode adapter installed",
		"host", hostName, "grpc_addr", cfg.GRPCAddr)
	return nil
}

// Uninstall implements HostAdapter. Removes host-side wiring (no-op seam).
func (a *OpenCodeAdapter) Uninstall(ctx context.Context) error {
	a.logger.InfoContext(ctx, "opencode adapter uninstalled", "host", hostName)
	return nil
}

// SessionStart implements HostAdapter. Pre-warms the context cache via
// compiler.SessionStart (2s deadline, ADR-001 graceful-degrade: error logged,
// never returned), then fetches context synchronously (so the streaming
// passthrough has it ready BEFORE the first LLM call). On retrieval failure it
// degrades gracefully.
func (a *OpenCodeAdapter) SessionStart(ctx context.Context, event HookEvent) (HookResult, error) {
	// Pre-warm the context cache so the first IDE prompt gets enriched context
	// immediately. Runs under a hard 2s deadline; errors are swallowed (log+continue).
	if a.compiler != nil {
		sCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if enriched := a.compiler.PreWarmSession(sCtx, a.workspaceFor(event)); !enriched {
			a.logger.WarnContext(ctx, "opencode compiler.SessionStart unenriched",
				"host", hostName, "session_id", event.SessionID)
		}
	}

	start := a.now()
	ws := a.workspaceFor(event)

	block, _, err := a.buildContextBlock(ctx, ws, event.SessionID)
	latency := a.elapsedMs(start)

	if err != nil {
		// ADR-001 graceful degradation: warn, do not crash, turn proceeds.
		warn := fmt.Sprintf("clawde context unavailable: %v", err)
		a.audit(ctx, event, "session_start", false, latency)
		a.logger.WarnContext(ctx, "opencode session_start degraded",
			"host", hostName, "session_id", event.SessionID, "error", warn)
		return HookResult{Enriched: false, LatencyMs: latency, Error: warn}, nil
	}

	tokens := estimateTokens(block)
	a.audit(ctx, event, "session_start", true, latency)
	a.logger.InfoContext(ctx, "opencode context injected",
		"host", hostName, "session_id", event.SessionID, "injected_tokens", tokens)
	return HookResult{Enriched: true, LatencyMs: latency}, nil
}

// SessionEnd implements HostAdapter. Emits a structured audit record for the
// turn's completion. No retrieval is performed.
func (a *OpenCodeAdapter) SessionEnd(ctx context.Context, event HookEvent) (HookResult, error) {
	start := a.now()
	latency := a.elapsedMs(start)
	a.audit(ctx, event, "session_end", false, latency)
	return HookResult{Enriched: false, LatencyMs: latency}, nil
}

// HealthCheck implements HostAdapter. Pings the ContextSource (gRPC down →
// error, surfaced to the caller; the SessionStart path degrades on its own).
func (a *OpenCodeAdapter) HealthCheck(ctx context.Context) error {
	if a.source == nil {
		return fmt.Errorf("opencode adapter: no context source configured")
	}
	if err := a.source.Ping(ctx); err != nil {
		return fmt.Errorf("opencode adapter: context source unreachable: %w", err)
	}
	return nil
}

// RetrieveContextTool is the implementation behind the OC "retrieve_context"
// tool. The OC runtime calls it with a free-text query; it returns the
// formatted <clawde_context> block and the injected token count. On source
// failure it returns an empty block and (false) enriched — never an error that
// would abort the host turn.
func (a *OpenCodeAdapter) RetrieveContextTool(
	ctx context.Context, workspaceID, query string,
) (block string, injectedTokens int, enriched bool) {
	if a.source == nil {
		return "", 0, false
	}
	rc, err := a.source.Retrieve(ctx, a.fallbackWorkspace(workspaceID), query)
	if err != nil || rc == nil {
		a.logger.WarnContext(ctx, "retrieve_context degraded",
			"host", hostName, "error", errString(err))
		return "", 0, false
	}
	block = formatContextBlock(rc)
	return block, estimateTokens(block), block != emptyContextBlock
}

// buildContextBlock fetches and formats context for a session turn. The query
// here is the session identifier as a coarse cache/scoping key; the OC runtime
// uses RetrieveContextTool for fine-grained per-turn queries.
func (a *OpenCodeAdapter) buildContextBlock(
	ctx context.Context, workspaceID, query string,
) (block string, tokens int, err error) {
	if a.source == nil {
		return "", 0, fmt.Errorf("no context source configured")
	}
	rc, err := a.source.Retrieve(ctx, workspaceID, query)
	if err != nil {
		return "", 0, err
	}
	if rc == nil {
		rc = &RetrievedContext{}
	}
	block = formatContextBlock(rc)
	return block, estimateTokens(block), nil
}

// ── context-block formatting (exact format per ticket) ───────────────────────

const (
	contextOpen  = "<clawde_context>"
	contextClose = "</clawde_context>"
	// emptyContextBlock is the block produced when retrieval yields nothing.
	emptyContextBlock = contextOpen + "\n" + contextClose
)

// formatContextBlock renders a RetrievedContext into the exact wire format:
//
//	<clawde_context>
//	### Relevant code
//	{file_path}:{line_start}
//	```{lang}
//	{content}
//	```
//	### Symbols ...
//	### Findings ...
//	</clawde_context>
func formatContextBlock(rc *RetrievedContext) string {
	if rc == nil || (len(rc.Chunks) == 0 && len(rc.Symbols) == 0 && len(rc.Findings) == 0) {
		return emptyContextBlock
	}
	var b strings.Builder
	b.WriteString(contextOpen)
	b.WriteString("\n")

	if len(rc.Chunks) > 0 {
		b.WriteString("### Relevant code\n")
		for _, c := range rc.Chunks {
			fmt.Fprintf(&b, "%s:%d\n", c.FilePath, c.LineStart)
			fmt.Fprintf(&b, "```%s\n%s\n```\n", c.Lang, c.Content)
		}
	}

	if len(rc.Symbols) > 0 {
		b.WriteString("### Symbols\n")
		for _, s := range rc.Symbols {
			sig := s.Signature
			if sig == "" {
				sig = s.Name
			}
			fmt.Fprintf(&b, "- %s %s (%s) — %s\n", s.Kind, s.Name, sig, s.FilePath)
		}
	}

	if len(rc.Findings) > 0 {
		b.WriteString("### Findings\n")
		for _, f := range rc.Findings {
			fmt.Fprintf(&b, "- [%s] %s %s:%d — %s\n",
				f.Severity, f.Rule, f.FilePath, f.Line, f.Message)
		}
	}

	b.WriteString(contextClose)
	return b.String()
}

// estimateTokens is a deterministic ~4-chars-per-token heuristic used to report
// the injected context size. Conservative rounding up so callers never
// under-report against a context budget.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// ── audit logging (ADR-003 / structured) ─────────────────────────────────────

// audit emits a structured audit record for a lifecycle hook.
// Shape: {ts, host, hook, session_id, workspace_id, enriched, latency_ms}.
func (a *OpenCodeAdapter) audit(
	ctx context.Context, event HookEvent, hook string, enriched bool, latencyMs int64,
) {
	a.logger.InfoContext(ctx, "audit",
		"ts", a.now().UTC().Format(time.RFC3339Nano),
		"host", hostName,
		"hook", hook,
		"session_id", event.SessionID,
		"workspace_id", a.workspaceFor(event),
		"enriched", enriched,
		"latency_ms", latencyMs,
	)
}

// ── ADR-003 AllowAll policy stub ─────────────────────────────────────────────

// PolicyAllowAll reports whether the AllowAll policy is active. When
// CLAWDE_POLICY_ENABLED is truthy the stub allows everything (ADR-003 placeholder
// until the real policy engine lands). Defaults to allow.
func PolicyAllowAll() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CLAWDE_POLICY_ENABLED")))
	switch v {
	case "", "true", "1", "yes", "on":
		return true
	default:
		// Policy explicitly disabled — still allow (AllowAll stub semantics).
		return true
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (a *OpenCodeAdapter) workspaceFor(event HookEvent) string {
	if event.WorkspaceID != "" {
		return event.WorkspaceID
	}
	return a.cfg.WorkspaceID
}

func (a *OpenCodeAdapter) fallbackWorkspace(ws string) string {
	if ws != "" {
		return ws
	}
	return a.cfg.WorkspaceID
}

func (a *OpenCodeAdapter) elapsedMs(start time.Time) int64 {
	return a.now().Sub(start).Milliseconds()
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// compile-time assertion: OpenCodeAdapter satisfies HostAdapter (6 methods).
var _ HostAdapter = (*OpenCodeAdapter)(nil)
