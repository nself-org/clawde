//go:build unix

package processpool

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setSysProcAttr configures the command to start a new process group.
// This lets us freeze/kill the whole group (parent + children) atomically
// by signalling the negative PGID.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// getPGID returns the process group ID for the given PID.
// On Linux/macOS, Getpgid(pid) is the canonical call.
func getPGID(pid int) (int, error) {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return 0, fmt.Errorf("Getpgid(%d): %w", pid, err)
	}
	return pgid, nil
}

// freezeGroup sends SIGSTOP to the entire process group identified by pgid.
// Using negative pgid targets the group, not just the process — this freezes
// all children spawned by the CLI worker as well.
func freezeGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGSTOP); err != nil {
		return fmt.Errorf("kill(-PGID=%d, SIGSTOP): %w", pgid, err)
	}
	return nil
}

// killGroup sends SIGKILL to the process group.
func killGroup(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// isAlive returns true if the process with the given PID exists and we have
// permission to signal it. kill(pid, 0) is the POSIX liveness probe.
func isAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}
