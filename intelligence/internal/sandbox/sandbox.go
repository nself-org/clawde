// sandbox.go — SandboxExecutor interface and runtime selection for tool execution isolation.
//
// Purpose: Provide a platform-appropriate sandbox around arbitrary shell commands invoked
//          via ExecuteShellActivity. Three runtimes are supported:
//
//            seccomp  (Linux default) — seccomp-BPF allow-list via syscall.RawSyscall(prctl);
//                                       real implementation under //go:build linux && seccomp.
//                                       Portable stub (no cgo) used when that tag is absent.
//            gvisor   (Linux opt-in)  — docker run --runtime=runsc wrapper.
//            sandbox-exec (darwin)    — macOS sandbox-exec with config/sandbox_macos.sb.
//
// Runtime selection: CLAWDE_SANDBOX_RUNTIME env var.
//   linux  default → seccomp
//   linux  CLAWDE_SANDBOX_RUNTIME=gvisor → gvisor
//   darwin default → sandbox-exec
//
// Canonical seccomp allow-list (LEDGER §D — 20 base + 5 PTY ioctls):
//   Base 20: read, write, open, close, stat, fstat, mmap, brk, exit, exit_group,
//            futex, nanosleep, getpid, getuid, getgid, arch_prctl, rt_sigaction,
//            rt_sigprocmask, sigreturn, poll
//   PTY 5:   TIOCGWINSZ, TIOCSWINSZ, TIOCSPTLCK, TIOCGPTPEER, TIOCGPTN
//   Default action: SCMP_ACT_ERRNO(EPERM) — graceful EPERM, NOT KILL.
//
// Constraints: File ≤500 lines. Canonical 25-entry list is exported for test verification.
// SPORT: REGISTRY-FUNCTIONS.md → sandbox.SandboxExecutor; REGISTRY-SERVICES.md → sandbox runtimes.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// ── Public types ───────────────────────────────────────────────────────────────

// SandboxCommand carries the parameters for a sandboxed execution.
type SandboxCommand struct {
	// Cmd is the executable name or path.
	Cmd string
	// Args are the command arguments (not including Cmd itself).
	Args []string
	// Stdin is the optional stdin content fed to the process.
	Stdin string
	// WorkDir is the working directory; uses os.TempDir() if empty.
	WorkDir string
	// TimeoutS is the wall-clock timeout in seconds; 0 means no timeout.
	TimeoutS int
	// Env is a list of "KEY=VALUE" extra environment overrides.
	Env []string
}

// SandboxResult carries the outcome of a sandboxed execution.
type SandboxResult struct {
	// Stdout is the process standard output.
	Stdout string
	// Stderr is the process standard error.
	Stderr string
	// ExitCode is the process exit code (0 = success).
	ExitCode int
	// WallTimeMs is the elapsed wall-clock time in milliseconds.
	WallTimeMs int64
	// Killed is true when the process was terminated due to timeout.
	Killed bool
}

// SandboxExecutor is the core interface for sandboxed command execution.
//
// Purpose: Allow ExecuteShellActivity (orchestration) to delegate execution to
//          the correct sandbox runtime without knowledge of the runtime details.
// Inputs:  ctx — caller context; cmd — the command descriptor.
// Outputs: SandboxResult — execution outcome; error — setup/launch failure.
// Constraints: Implementations must kill the process group on timeout.
// SPORT: REGISTRY-FUNCTIONS.md → sandbox.SandboxExecutor.
type SandboxExecutor interface {
	Execute(ctx context.Context, cmd SandboxCommand) (SandboxResult, error)
}

// ── Canonical syscall/ioctl lists (LEDGER §D) ─────────────────────────────────

// AllowedSyscallNames is the canonical base-20 seccomp allow-list from LEDGER §D.
// These names are the Linux syscall names used when building the BPF filter.
// This list is authoritative — do NOT alter without updating ADR-008.
var AllowedSyscallNames = []string{
	"read", "write", "open", "close", "stat", "fstat",
	"mmap", "brk", "exit", "exit_group",
	"futex", "nanosleep", "getpid", "getuid", "getgid",
	"arch_prctl", "rt_sigaction", "rt_sigprocmask", "sigreturn", "poll",
}

// AllowedPTYIoctls is the canonical PTY ioctl allow-list (5 entries) from LEDGER §D.
// These constants are used by the seccomp BPF filter to permit PTY operations.
var AllowedPTYIoctls = []string{
	"TIOCGWINSZ", "TIOCSWINSZ", "TIOCSPTLCK", "TIOCGPTPEER", "TIOCGPTN",
}

// AllowListSize returns the total number of allowed entries (20 base + 5 PTY = 25).
// Used by tests to assert the canonical list is intact.
func AllowListSize() int {
	return len(AllowedSyscallNames) + len(AllowedPTYIoctls)
}

// ── Runtime selection ─────────────────────────────────────────────────────────

// Runtime represents the chosen sandbox runtime.
type Runtime string

const (
	RuntimeSeccomp     Runtime = "seccomp"
	RuntimeGVisor      Runtime = "gvisor"
	RuntimeSandboxExec Runtime = "sandbox-exec"
)

// DetectRuntime returns the sandbox runtime that should be used on the current
// platform, taking the CLAWDE_SANDBOX_RUNTIME override into account.
//
// Platform defaults:
//
//	linux  → seccomp (unless CLAWDE_SANDBOX_RUNTIME=gvisor)
//	darwin → sandbox-exec (dev-only; override unsupported)
//	other  → seccomp (portable stub; will EPERM all syscalls gracefully)
func DetectRuntime() Runtime {
	override := os.Getenv("CLAWDE_SANDBOX_RUNTIME")
	switch runtime.GOOS {
	case "linux":
		if override == "gvisor" {
			return RuntimeGVisor
		}
		return RuntimeSeccomp
	case "darwin":
		return RuntimeSandboxExec
	default:
		return RuntimeSeccomp
	}
}

// New returns the SandboxExecutor for the given runtime.
// Returns an error when gVisor is requested but Docker is unavailable.
func New(rt Runtime) (SandboxExecutor, error) {
	switch rt {
	case RuntimeGVisor:
		return newGVisorExecutor()
	case RuntimeSandboxExec:
		return newSandboxExecExecutor(), nil
	case RuntimeSeccomp:
		return newSeccompExecutor(), nil
	default:
		return nil, fmt.Errorf("sandbox: unknown runtime %q", rt)
	}
}

// NewDefault creates a SandboxExecutor using the platform-detected runtime.
func NewDefault() (SandboxExecutor, error) {
	return New(DetectRuntime())
}

// Apply installs the seccomp-BPF filter on the process identified by pid.
//
// Purpose: Allow the PTY pool (internal/pty) to apply the sandbox filter to a
//          pre-warmed slot process after fork but before the slot is used.
//          On Linux (seccomp build tag), installs the canonical LEDGER §D BPF
//          allow-list via prctl(PR_SET_SECCOMP). On macOS, seatbelt is applied
//          at spawn time via sandbox-exec, so this function is a no-op.
//          On all other platforms, returns nil (no-op).
//
// Inputs:  pid — process ID to apply the filter to (0 = current process).
// Outputs: error on filter installation failure (Linux only).
// Constraints: Must be called before exec on Linux (prctl is per-thread/process).
// SPORT: REGISTRY-FUNCTIONS.md → sandbox.Apply.
func Apply(pid int) error {
	return applyFilter(pid)
}
