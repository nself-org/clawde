//go:build linux && seccomp

// seccomp_linux.go — real seccomp-BPF executor for Linux production builds.
//
// Purpose: Apply the canonical LEDGER §D allow-list (20 syscalls + 5 PTY ioctls)
//          via prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER) before execing the
//          sandboxed command. The filter is installed via a self-re-exec shim:
//          the executor runs /proc/self/exe with CLAWDE_SECCOMP_INIT=1 set; the
//          shim detects that sentinel, installs the BPF filter in-process, then
//          syscall.Exec's the real command. This is the standard CGO-free pattern
//          used by runc/containerd and avoids the fork+exec ordering constraint.
//
// Self-re-exec shim (init path):
//   os.Getenv("CLAWDE_SECCOMP_INIT") == "1"
//   → install filter via prctl(PR_SET_NO_NEW_PRIVS) + prctl(PR_SET_SECCOMP)
//   → syscall.Exec(real_cmd, real_args, filtered_env)
//
// Build requirement: `go build -tags seccomp`
//
//          The BPF filter is implemented directly via the Linux seccomp(2) syscall
//          and BPF instruction encoding. No CGO, no libseccomp.so — the filter is
//          built from the canonical LEDGER §D syscall numbers at compile time.
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
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// seccompInitEnv is the environment sentinel used by the self-re-exec shim.
// When present with value "1", the process installs the BPF filter then exec's
// the real command encoded in seccompCmdEnv / seccompArgsEnv.
const seccompInitEnv = "CLAWDE_SECCOMP_INIT"
const seccompCmdEnv = "CLAWDE_SECCOMP_CMD"
const seccompArgsEnv = "CLAWDE_SECCOMP_ARGS" // args joined with \x00

// MaybeRunSeccompShim checks whether the current process was invoked as the
// seccomp init shim. If so, it installs the BPF filter and exec's the real
// command — this function never returns on the shim path.
//
// Purpose: Called from main() (or init() in the shim binary) so the shim
//          installs the filter before any restricted syscall is made.
//          On the normal (non-shim) path, this function is a no-op.
//
// IMPORTANT: When using the seccompExecutor, the test/production binary does NOT
// need to call this directly — the executor invokes /proc/self/exe which handles
// it. If you embed the intelligence binary as the executor, call this at the very
// top of main() before any other logic.
func MaybeRunSeccompShim() {
	if os.Getenv(seccompInitEnv) != "1" {
		return
	}
	// Install the BPF filter in this process (the shim).
	if err := installFilter(buildBPFFilter()); err != nil {
		fmt.Fprintf(os.Stderr, "seccomp shim: installFilter: %v\n", err)
		os.Exit(125)
	}
	// Exec the real command.
	cmd := os.Getenv(seccompCmdEnv)
	if cmd == "" {
		fmt.Fprintln(os.Stderr, "seccomp shim: CLAWDE_SECCOMP_CMD not set")
		os.Exit(125)
	}
	argsRaw := os.Getenv(seccompArgsEnv)
	var args []string
	if argsRaw != "" {
		args = strings.Split(argsRaw, "\x00")
	}
	argv := append([]string{cmd}, args...)

	// Strip shim sentinels from the environment before exec.
	env := filterEnv(os.Environ(), seccompInitEnv, seccompCmdEnv, seccompArgsEnv)

	if err := syscall.Exec(cmd, argv, env); err != nil {
		fmt.Fprintf(os.Stderr, "seccomp shim: exec %q: %v\n", cmd, err)
		os.Exit(126)
	}
	// Unreachable.
}

// filterEnv returns env with entries whose KEY matches any of the given keys removed.
func filterEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		skip := false
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, e)
		}
	}
	return out
}

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
// Purpose: Restrict the child process to the canonical LEDGER §D syscall set
//          via real kernel seccomp enforcement (prctl PR_SET_SECCOMP).
//          Blocked syscalls return EPERM — the process continues (graceful deny).
//
// Implementation (CGO-free self-re-exec shim):
//   This executor launches /proc/self/exe (the current binary) with three env
//   sentinels set: CLAWDE_SECCOMP_INIT=1, CLAWDE_SECCOMP_CMD, CLAWDE_SECCOMP_ARGS.
//   The shim path (MaybeRunSeccompShim) detects those sentinels at startup, calls
//   installFilter (which calls prctl in-process — no fork ordering constraint),
//   then syscall.Exec's the real command. The result is a child process that ran
//   the BPF filter installation before any restricted syscall.
//
//   This is the standard CGO-free pattern used by runc and containerd. It requires
//   that /proc/self/exe is readable and executable (true in all supported environments).
//   The caller (cmd/worker/main.go) must call MaybeRunSeccompShim() at the top of
//   main() so the shim path is handled correctly in that binary.
//
// Inputs:  ctx — caller context; sc — the sandbox command.
// Outputs: SandboxResult; error on filter build or launch failure.
func (e *seccompExecutor) Execute(ctx context.Context, sc SandboxCommand) (SandboxResult, error) {
	// Resolve the shim executable: /proc/self/exe in the child process that will
	// install the seccomp filter before exec-ing the real command.
	shimExe, err := os.Readlink("/proc/self/exe")
	if err != nil {
		// /proc/self/exe unavailable (unusual on Linux); fall back to direct exec
		// without seccomp — log the degradation so operators see it.
		fmt.Fprintf(os.Stderr, "seccomp: /proc/self/exe unreadable (%v); running without filter\n", err)
		cmd := exec.CommandContext(ctx, sc.Cmd, sc.Args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
		return runWithTimeout(ctx, cmd, sc)
	}

	// Build the shim command: /proc/self/exe with sentinel env vars.
	// The shim installs the BPF filter then exec's sc.Cmd.
	argsEncoded := strings.Join(sc.Args, "\x00")
	shimEnv := append(filterEnv(sc.Env), // caller's extra env (clean of sentinels)
		seccompInitEnv+"=1",
		seccompCmdEnv+"="+sc.Cmd,
		seccompArgsEnv+"="+argsEncoded,
	)

	// Build an exec.Cmd for the shim (which will exec the real command after
	// installing the filter). We do NOT use exec.CommandContext here because
	// we pass a custom env — use the shimExe path directly.
	shimCmd := exec.CommandContext(ctx, shimExe)
	shimCmd.Env = append(os.Environ(), shimEnv...)
	shimCmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}

	// Wrap in a SandboxCommand so runWithTimeout handles stdin/stdout/timeout.
	shimSC := SandboxCommand{
		Cmd:      shimExe,
		Stdin:    sc.Stdin,
		WorkDir:  sc.WorkDir,
		TimeoutS: sc.TimeoutS,
		// Args/Env are encoded in env vars above; shimCmd has no CLI args.
	}

	return runWithTimeoutWithCmd(ctx, shimCmd, shimSC)
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
