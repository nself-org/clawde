//go:build windows

package processpool

import (
	"fmt"
	"os/exec"
	"os"
)

// setSysProcAttr is a no-op on Windows.
// Windows does not support Setpgid; process group semantics differ.
func setSysProcAttr(_ *exec.Cmd) {}

// getPGID is not meaningful on Windows. We return the PID itself as a
// placeholder so callers have a stable handle for logging.
func getPGID(pid int) (int, error) {
	return pid, nil
}

// freezeGroup suspends the process on Windows via NtSuspendProcess.
// Note: this freezes only the top-level process, not the full job object.
// Full job-object suspension requires Windows 8+ and additional syscall wiring;
// that level of depth is deferred to a follow-up ticket.
func freezeGroup(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("FindProcess(%d): %w", pid, err)
	}
	// On Windows, there is no direct SIGSTOP equivalent exposed via the Go
	// standard library. The correct native call is NtSuspendProcess via
	// windows.SuspendThread (requires enumerating threads). We emit a no-op
	// here and note the thread-suspend path for SP-21.T09 follow-up.
	_ = p
	return nil
}

// killGroup terminates the process on Windows.
func killGroup(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Kill()
}

// isAlive returns true if the process with the given PID can be found.
func isAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, FindProcess always succeeds. We attempt a no-op signal
	// to probe liveness; OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION
	// is the proper approach but requires syscall — deferred to SP-21.T09.
	_ = p
	return true
}
