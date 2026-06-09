// exec_common.go — shared execution helpers used by all sandbox runtime backends.
//
// Purpose: Provide process launch, timeout management, and output capture that are
//          identical across seccomp, gVisor, and sandbox-exec backends.
//
// Constraints: File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → sandbox.runWithTimeout.
package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// runWithTimeout launches cmd, enforces SandboxCommand.TimeoutS, kills the
// entire process GROUP on expiry, and returns a SandboxResult.
//
// Purpose: Centralise process lifecycle so each runtime backend only builds
//          the exec.Cmd (setting SysProcAttr for Setpgid) and delegates here.
// Inputs:  ctx — caller context (cancellation); cmd — the prepared exec.Cmd;
//          sc — the original SandboxCommand (for timeout + stdin).
// Outputs: SandboxResult with Stdout, Stderr, ExitCode, WallTimeMs, Killed.
// Constraints: Always kills the process group, not just the process, to reap
//              any child processes spawned inside the sandbox.
func runWithTimeout(ctx context.Context, cmd *exec.Cmd, sc SandboxCommand) (SandboxResult, error) {
	// Set stdin.
	if sc.Stdin != "" {
		cmd.Stdin = strings.NewReader(sc.Stdin)
	}

	// Capture stdout/stderr.
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// Work dir.
	if sc.WorkDir != "" {
		cmd.Dir = sc.WorkDir
	} else {
		cmd.Dir = os.TempDir()
	}

	// Merge extra env over inherited environment.
	if len(sc.Env) > 0 {
		cmd.Env = append(os.Environ(), sc.Env...)
	}

	// Ensure the child gets its own process group so we can kill the whole tree.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	start := time.Now()

	if err := cmd.Start(); err != nil {
		return SandboxResult{}, err
	}

	// Set up timeout killer.
	killed := false
	var timeoutCancel context.CancelFunc
	runCtx := ctx
	if sc.TimeoutS > 0 {
		runCtx, timeoutCancel = context.WithTimeout(ctx, time.Duration(sc.TimeoutS)*time.Second)
		defer timeoutCancel()
	}

	// Wait for process or timeout.
	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-doneCh:
		// process finished normally
	case <-runCtx.Done():
		// Timeout or caller cancellation — kill the process group.
		killed = true
		pgid := cmd.Process.Pid // Setpgid=true → pgid == pid
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-doneCh // drain
	}

	wallMs := time.Since(start).Milliseconds()

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				exitCode = 137 // killed by signal
			}
		} else if !killed {
			return SandboxResult{}, waitErr
		}
	}

	return SandboxResult{
		Stdout:     outBuf.String(),
		Stderr:     errBuf.String(),
		ExitCode:   exitCode,
		WallTimeMs: wallMs,
		Killed:     killed,
	}, nil
}
