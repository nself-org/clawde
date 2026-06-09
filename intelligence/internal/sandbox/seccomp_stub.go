//go:build !linux || !seccomp

// seccomp_stub.go — portable seccomp executor stub.
//
// Purpose: Allow the package to compile and tests to run on any platform
//          (macOS CI, Windows CI) and on Linux without the seccomp build tag.
//          The stub performs NO kernel-level filtering — it simply runs the
//          command via exec.Cmd. On production Linux builds, replace this with
//          the real seccomp implementation by building with:
//
//            go build -tags seccomp
//
//          or via the Makefile target `make build-seccomp`.
//
//          The stub still enforces timeout and process-group kill from
//          exec_common.go, so it is safe for use in dev/test environments.
//
// Constraints: File ≤500 lines.
// SPORT: REGISTRY-SERVICES.md → seccomp sandbox runtime (stub).
package sandbox

import (
	"context"
	"os/exec"
)

// seccompExecutor is the portable stub implementation of SandboxExecutor.
// On Linux + seccomp builds, seccomp_linux.go replaces this file.
type seccompExecutor struct{}

func newSeccompExecutor() SandboxExecutor {
	return &seccompExecutor{}
}

// Execute runs the command without kernel-level seccomp filtering.
//
// Purpose: Stub implementation for non-Linux or non-seccomp builds.
//          Provides the same interface/timeout behaviour as the real executor.
// Inputs:  ctx — caller context; cmd — the sandbox command.
// Outputs: SandboxResult; error on launch failure.
// Constraints: No syscall filtering; development/test use only.
func (e *seccompExecutor) Execute(ctx context.Context, sc SandboxCommand) (SandboxResult, error) {
	cmd := exec.CommandContext(ctx, sc.Cmd, sc.Args...)
	return runWithTimeout(ctx, cmd, sc)
}
