// session_hook.go — clawd SessionStart wiring for the auto-context compiler.
//
// Purpose: Drive CompileContext from the clawd SessionStart hook with a hard 2s
//          deadline. On timeout or any error the hook exits cleanly (exit 0
//          semantics: enriched=false, no panic) and emits a structured audit
//          line so the degradation is observable. CompileContext itself routes
//          through the ADR-003 dispatch chain (PolicyEngine) before retrieval.
// Inputs:  *Compiler, CompileContextRequest, slog.Logger.
// Outputs: CompileContextResponse (enriched=false on timeout/error).
// Constraints: File ≤500 lines. Never returns a non-nil error to the host — the
//              turn must always proceed (graceful degradation, ADR-001).
// SPORT: REGISTRY-FUNCTIONS.md → compiler.SessionStart.
package compiler

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// sessionStartDeadline is the hard budget for context compilation at the
// SessionStart hook. Exceeding it means the host turn proceeds unenriched.
const sessionStartDeadline = 2 * time.Second

// SessionStart runs CompileContext under a 2s deadline for the clawd SessionStart
// hook. It NEVER returns an error: on timeout or compile failure it returns an
// unenriched response and emits an audit line. The host turn always continues.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.SessionStart.
func (c *Compiler) SessionStart(
	ctx context.Context, req CompileContextRequest, logger *slog.Logger,
) CompileContextResponse {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	start := c.now()

	dctx, cancel := context.WithTimeout(ctx, sessionStartDeadline)
	defer cancel()

	type result struct {
		resp CompileContextResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := c.CompileContext(dctx, req)
		ch <- result{resp, err}
	}()

	select {
	case <-dctx.Done():
		// Deadline hit (or parent cancelled): graceful exit, turn proceeds.
		c.auditHook(logger, req, "session_start", false, c.elapsedMs(start), dctx.Err())
		return CompileContextResponse{Enriched: false}
	case r := <-ch:
		if r.err != nil {
			c.auditHook(logger, req, "session_start", false, c.elapsedMs(start), r.err)
			return CompileContextResponse{Enriched: false}
		}
		c.auditHook(logger, req, "session_start", r.resp.Enriched, c.elapsedMs(start), nil)
		return r.resp
	}
}

// elapsedMs is the wall-clock ms since start (injectable now() for tests).
func (c *Compiler) elapsedMs(start time.Time) int64 {
	return c.now().Sub(start).Milliseconds()
}

// auditHook emits a structured audit record for a hook invocation.
func (c *Compiler) auditHook(
	logger *slog.Logger, req CompileContextRequest, hook string, enriched bool, latencyMs int64, err error,
) {
	attrs := []any{
		"ts", c.now().UTC().Format(time.RFC3339Nano),
		"component", "auto_context_compiler",
		"hook", hook,
		"workspace_id", req.WorkspaceID,
		"enriched", enriched,
		"latency_ms", latencyMs,
	}
	if err != nil {
		attrs = append(attrs, "degraded", err.Error())
	}
	logger.Info("audit", attrs...)
}
