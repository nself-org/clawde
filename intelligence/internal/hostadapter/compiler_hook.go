// compiler_hook.go — production ContextCompiler bridge over *compiler.Compiler.
//
// Purpose:    Wraps *compiler.Compiler so it satisfies the ContextCompiler
//             interface declared in adapter.go. This is the ONLY file in the
//             hostadapter package that imports the compiler package, keeping
//             the compiler's CGo-heavy transitive dependency out of the test
//             binary for all tests that don't need it.
// Inputs:     *compiler.Compiler (may be nil — NewCompilerHook returns nil in
//             that case so the adapter's nil-guard fires naturally).
// Outputs:    ContextCompiler (or nil).
// Constraints: Single responsibility: bridge only. No business logic. ≤60 lines.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.CompilerHook.
package hostadapter

import (
	"context"
	"log/slog"
	"os"

	"github.com/nself-org/clawde/intelligence/internal/compiler"
)

// compilerHook wraps *compiler.Compiler, satisfying ContextCompiler.
type compilerHook struct {
	c      *compiler.Compiler
	logger *slog.Logger
}

// NewCompilerHook returns a ContextCompiler backed by comp. If comp is nil,
// NewCompilerHook returns nil so the adapter's nil-guard fires naturally (the
// adapter never calls PreWarmSession on a nil ContextCompiler).
func NewCompilerHook(comp *compiler.Compiler) ContextCompiler {
	if comp == nil {
		return nil
	}
	return &compilerHook{
		c:      comp,
		logger: slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// PreWarmSession implements ContextCompiler. It calls compiler.SessionStart
// under the caller-supplied context (must be ≤2s — callers use
// context.WithTimeout). Errors from the compiler are silently swallowed here
// (they are already logged inside compiler.SessionStart).
func (h *compilerHook) PreWarmSession(ctx context.Context, workspaceID string) bool {
	req := compiler.CompileContextRequest{WorkspaceID: workspaceID}
	resp := h.c.SessionStart(ctx, req, h.logger)
	return resp.Enriched
}
