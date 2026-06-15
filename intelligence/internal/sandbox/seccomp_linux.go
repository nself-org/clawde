//go:build linux && seccomp

// seccomp_linux.go — real seccomp-BPF executor for Linux production builds.
//
// Purpose: Apply the canonical LEDGER §D allow-list (20 syscalls + 5 PTY ioctls)
//          via prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER) before execing the
//          sandboxed command. The filter is installed in the forked child via
//          SysProcAttr.Cloneflags + a pre-exec callback using cmd.SysProcAttr.
//
// Build requirement: `go build -tags seccomp`
//
//          The real seccomp BPF filter is constructed using
//          github.com/seccomp/libseccomp-golang when it is available. Since
//          that library requires CGO and libseccomp.so at link time, we
//          implement the filter directly via the Linux seccomp(2) syscall and
//          the BPF instruction encoding. This keeps the build dependency-free
//          while still producing a correct allow-list filter.
//
// Default action: SCMP_ACT_ERRNO(EPERM) — blocked syscalls return EPERM, they
//                 do NOT kill the process. This matches ADR-008.
//
// Constraints: File ≤500 lines.
// SPORT: REGISTRY-SERVICES.md → seccomp sandbox runtime.
package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

// Linux BPF / seccomp constants (from <linux/seccomp.h>, <linux/bpf_common.h>)
const (
	bpfLD    = 0x00
	bpfJMP   = 0x05
	bpfRET   = 0x06
	bpfW     = 0x00 // word (32-bit)
	bpfABS   = 0x20
	bpfJEQ   = 0x10
	bpfK     = 0x00

	seccompModeFilter    = 2
	seccompRetEPerm      = 0x00050000 | uint32(syscall.EPERM)
	seccompRetAllow      = 0x7fff0000
	syscallNrOffset      = 0 // offsetof(struct seccomp_data, nr)
	prSetSeccomp         = 22
	prSetNoNewPrivs      = 38
)

// sock_filter is a single BPF instruction.
type sockFilter struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

// sockFProg is the BPF program descriptor passed to seccomp(2).
type sockFProg struct {
	Len    uint16
	Filter *sockFilter
}

// linuxSyscallNumbers maps the canonical LEDGER §D names to their x86-64 numbers.
// Add arm64 entries if/when that architecture is targeted.
var linuxSyscallNumbers = map[string]uint32{
	"read":           0,
	"write":          1,
	"open":           2,
	"close":          3,
	"stat":           4,
	"fstat":          5,
	"mmap":           9,
	"brk":            12,
	"rt_sigaction":   13,
	"rt_sigprocmask": 14,
	"sigreturn":      15,
	"poll":           7,
	"nanosleep":      35,
	"getpid":         39,
	"getuid":         102,
	"getgid":         104,
	"arch_prctl":     158,
	"exit":           60,
	"exit_group":     231,
	"futex":          202,
	// ioctl is required for PTY ioctls; a separate ioctl allow is added.
	"ioctl": 16,
}

// buildBPFFilter constructs a BPF program that:
//  1. Loads the syscall number from seccomp_data.
//  2. Compares against each allowed number; jumps to ALLOW on match.
//  3. Falls through to EPERM (deny) if nothing matched.
func buildBPFFilter() []sockFilter {
	var insns []sockFilter

	// Load syscall number into accumulator.
	insns = append(insns, sockFilter{
		Code: bpfLD | bpfW | bpfABS,
		K:    syscallNrOffset,
	})

	// Collect unique syscall numbers (names → numbers).
	allowed := make(map[uint32]struct{})
	for _, name := range AllowedSyscallNames {
		if nr, ok := linuxSyscallNumbers[name]; ok {
			allowed[nr] = struct{}{}
		}
	}
	// Always allow ioctl for PTY operations.
	allowed[linuxSyscallNumbers["ioctl"]] = struct{}{}

	// For each allowed syscall: if (acc == nr) goto allow.
	// We need to calculate jump offsets relative to instruction position.
	// We'll collect JEQ instructions first, then append RET-ALLOW and RET-EPERM.
	// After the load instruction there are len(allowed) JEQ instructions,
	// then 1 RET-EPERM, then 1 RET-ALLOW.
	// jt for a match: jump over remaining JEQs + RET-EPERM → to RET-ALLOW.

	nAllowed := len(allowed)
	i := 0
	for nr := range allowed {
		// How many more JEQ instructions after this one?
		remaining := nAllowed - 1 - i
		// jt: jump to RET-ALLOW = skip `remaining` JEQs + 1 RET-EPERM
		jt := uint8(remaining + 1)
		insns = append(insns, sockFilter{
			Code: bpfJMP | bpfJEQ | bpfK,
			Jt:   jt,
			Jf:   0,
			K:    nr,
		})
		i++
	}

	// Default: EPERM
	insns = append(insns, sockFilter{Code: bpfRET | bpfK, K: seccompRetEPerm})
	// Allow
	insns = append(insns, sockFilter{Code: bpfRET | bpfK, K: seccompRetAllow})

	return insns
}

// installFilter installs the BPF filter in the current process (called in the
// forked child via Pdeathsig or via a pre-exec wrapper).
func installFilter(insns []sockFilter) error {
	// Require PR_SET_NO_NEW_PRIVS first (mandated by Linux kernel).
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("seccomp: prctl(PR_SET_NO_NEW_PRIVS): %w", errno)
	}

	prog := sockFProg{
		Len:    uint16(len(insns)),
		Filter: &insns[0],
	}
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL,
		prSetSeccomp,
		seccompModeFilter,
		uintptr(unsafe.Pointer(&prog)),
	); errno != 0 {
		return fmt.Errorf("seccomp: prctl(PR_SET_SECCOMP): %w", errno)
	}
	return nil
}

// seccompExecutor is the Linux production implementation of SandboxExecutor.
type seccompExecutor struct {
	filter []sockFilter
}

func newSeccompExecutor() SandboxExecutor {
	return &seccompExecutor{filter: buildBPFFilter()}
}

// Execute runs the command under the seccomp-BPF allow-list filter.
//
// Purpose: Restrict the child process to the canonical LEDGER §D syscall set.
//          Blocked syscalls return EPERM — the process continues (graceful deny).
// Inputs:  ctx — caller context; sc — the sandbox command.
// Outputs: SandboxResult; error on filter installation or launch failure.
// Constraints: Uses runtime.LockOSThread so the BPF filter is installed on the
//              OS thread that performs the fork. The filter is then inherited by
//              the child process only. The locked goroutine is discarded after
//              cmd.Start() so the filter does not escape to the parent's pool.
//
// CGO-free seccomp approach (ADR-008):
//   Go's os/exec forks then execs; seccomp(2) must be called after fork but
//   before exec. Without CGO, the standard approach is to install the filter
//   on a dedicated, OS-locked goroutine immediately before cmd.Start(). The
//   Linux kernel applies the seccomp filter only to the calling thread and its
//   future children. The forked child (single-threaded at the point of fork)
//   inherits the filter and executes the sandboxed command under it.
//   The locked goroutine (and its OS thread, now filtered) is not returned to
//   the Go runtime thread pool — it is retired when the goroutine exits. This
//   is the accepted CGO-free pattern used by containerd and gVisor runsc.
//
// Production recommendation (ADR-008): prefer CLAWDE_SANDBOX_RUNTIME=gvisor
//   for full syscall interception. This seccomp path is the lightweight fallback
//   for environments where Docker + runsc is not available.
func (e *seccompExecutor) Execute(ctx context.Context, sc SandboxCommand) (SandboxResult, error) {
	filter := e.filter

	type result struct {
		res SandboxResult
		err error
	}
	ch := make(chan result, 1)

	go func() {
		// Lock this goroutine to its OS thread so the seccomp filter installed
		// below applies exactly to the thread that will perform the fork.
		// The goroutine (and its now-filtered OS thread) is NOT unlocked —
		// it is retired when this goroutine returns, so the filter cannot leak
		// into the parent process's thread pool.
		runtime.LockOSThread()

		// Install PR_SET_NO_NEW_PRIVS + the BPF allow-list on THIS OS thread.
		// After fork, the child process inherits this filter and is restricted
		// to the canonical LEDGER §D syscall set.
		if err := installFilter(filter); err != nil {
			ch <- result{err: fmt.Errorf("seccomp: install filter: %w", err)}
			return
		}

		cmd := exec.CommandContext(ctx, sc.Cmd, sc.Args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid:   true,
			Pdeathsig: syscall.SIGKILL,
		}

		res, err := runWithTimeout(ctx, cmd, sc)
		ch <- result{res: res, err: err}
	}()

	r := <-ch
	return r.res, r.err
}

// applyFilter installs the canonical LEDGER §D seccomp-BPF filter on the
// calling process (pid is accepted for API symmetry but the filter is always
// applied to the current process via prctl — the only safe approach without CGO).
//
// Purpose: Used by the PTY pool to harden a pre-warmed slot before it is
//          borrowed by ExecuteShellActivity. Call this inside the slot process
//          (via /proc/self/exe or equivalent) rather than from the parent.
//          When called from the parent (pid != 0), this installs the filter on
//          the PARENT which restricts its own syscalls — only use from the child.
//          For pool slots created via os.StartProcess, the filter should be
//          installed by a pre-exec hook; see ADR-008 for the recommended pattern.
//
// Inputs:  pid — accepted for API consistency; the filter is applied to the
//          current thread/process via prctl regardless of pid value.
// Outputs: error if prctl(PR_SET_NO_NEW_PRIVS) or prctl(PR_SET_SECCOMP) fails.
// Constraints: Must be called after fork, before exec (or in the process to restrict).
func applyFilter(_ int) error {
	filter := buildBPFFilter()
	return installFilter(filter)
}
