// exec_common.go — shared execution helpers used by all sandbox runtime backends.
//
// Purpose: Provide process launch, timeout management, and output capture that are
//          identical across seccomp, gVisor, and sandbox-exec backends.
//          Also provides ExecuteThroughSlot for PTY-pool-backed command execution.
//
// Constraints: File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → sandbox.runWithTimeout, sandbox.ExecuteThroughSlot.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// runWithTimeoutWithCmd runs a pre-built exec.Cmd, applying sc's stdin/workdir/
// timeout settings. This variant is used by the seccomp shim path which builds
// the exec.Cmd itself (e.g. with custom Env or SysProcAttr) before delegating.
//
// Purpose: Allow callers to pre-configure exec.Cmd (e.g. seccomp shim) while
//          reusing the common timeout/stdin/output-capture logic.
// Inputs:  ctx — caller context; cmd — pre-built, NOT yet started; sc — settings.
// Outputs: SandboxResult; error on launch or wait failure.
func runWithTimeoutWithCmd(ctx context.Context, cmd *exec.Cmd, sc SandboxCommand) (SandboxResult, error) {
	return runWithTimeout(ctx, cmd, sc)
}

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

// ExecuteThroughSlot sends a SandboxCommand through the pre-warmed slot's
// Stdin/Stdout pipes and collects the result.
//
// Purpose: Allow ExecuteShellActivity to route commands through a PTY pool slot's
//          existing /bin/sh process rather than spawning a new process per call.
//          The command is written to stdin as a shell one-liner with an exit-code
//          marker, and stdout is read until the marker is seen.
//
// Protocol:
//  1. Write: "CMD arg1 arg2; echo __EXIT__$?\n" to slot.Stdin.
//  2. Read slot.Stdout until the "__EXIT__<N>" marker line appears.
//  3. Parse the exit code from the marker; return accumulated output as Stdout.
//
// Inputs:  ctx — caller context (enforces deadline); stdin — slot write end;
//          stdout — slot read end; sc — the SandboxCommand to execute.
// Outputs: SandboxResult (Stdout, Stderr merged, ExitCode, WallTimeMs);
//          error on I/O failure or context cancellation.
// Constraints: Stderr is merged into Stdout (the slot shell uses a single pipe).
//              Timeout is enforced via ctx; the slot process is NOT killed on
//              timeout (caller's Release handles that). Shell metacharacters in
//              sc.Cmd/sc.Args must be safe (caller ensures no injection).
// SPORT: REGISTRY-FUNCTIONS.md → sandbox.ExecuteThroughSlot.
func ExecuteThroughSlot(ctx context.Context, stdin io.WriteCloser, stdout io.ReadCloser, sc SandboxCommand) (SandboxResult, error) {
	start := time.Now()

	// Build the shell invocation: quote args simply (space join — caller is trusted).
	parts := make([]string, 0, 1+len(sc.Args))
	parts = append(parts, sc.Cmd)
	parts = append(parts, sc.Args...)
	cmd := strings.Join(parts, " ")

	// Inject extra env vars as inline exports before the command.
	envPrefix := ""
	for _, kv := range sc.Env {
		envPrefix += "export " + kv + "; "
	}

	// Unique marker to detect end-of-command output.
	const marker = "__CLAWDE_EXIT__"
	line := fmt.Sprintf("%s%s 2>&1; echo %s$?\n", envPrefix, cmd, marker)

	// Write with context awareness.
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(stdin, line)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			return SandboxResult{}, fmt.Errorf("slot stdin write: %w", err)
		}
	case <-ctx.Done():
		return SandboxResult{}, ctx.Err()
	}

	// Read stdout until we see the marker.
	type readResult struct {
		output   string
		exitCode int
		err      error
	}
	readDone := make(chan readResult, 1)
	go func() {
		var buf bytes.Buffer
		tmp := make([]byte, 4096)
		exitCode := 0
		for {
			n, err := stdout.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
			}
			// Check if our marker is in the accumulated output.
			out := buf.String()
			if idx := strings.Index(out, marker); idx >= 0 {
				// Extract exit code from marker line.
				rest := out[idx+len(marker):]
				end := strings.IndexByte(rest, '\n')
				if end < 0 {
					end = len(rest)
				}
				codeStr := strings.TrimSpace(rest[:end])
				if codeStr != "" {
					fmt.Sscanf(codeStr, "%d", &exitCode)
				}
				// Trim everything from the marker onward from the output.
				readDone <- readResult{output: out[:idx], exitCode: exitCode}
				return
			}
			if err != nil {
				readDone <- readResult{output: out, exitCode: -1, err: err}
				return
			}
		}
	}()

	// Apply timeout if set.
	var timeoutCh <-chan struct{}
	var cancel context.CancelFunc
	if sc.TimeoutS > 0 {
		tctx, c := context.WithTimeout(ctx, time.Duration(sc.TimeoutS)*time.Second)
		defer c()
		cancel = c
		timeoutCh = tctx.Done()
	} else {
		timeoutCh = ctx.Done()
	}
	_ = cancel

	select {
	case res := <-readDone:
		if res.err != nil && res.err != io.EOF {
			return SandboxResult{}, fmt.Errorf("slot stdout read: %w", res.err)
		}
		return SandboxResult{
			Stdout:     res.output,
			Stderr:     "", // merged into Stdout via 2>&1
			ExitCode:   res.exitCode,
			WallTimeMs: time.Since(start).Milliseconds(),
		}, nil
	case <-timeoutCh:
		return SandboxResult{
			Stdout:     "",
			ExitCode:   137,
			WallTimeMs: time.Since(start).Milliseconds(),
			Killed:     true,
		}, nil
	}
}
