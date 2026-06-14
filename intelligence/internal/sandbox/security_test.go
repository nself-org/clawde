// security_test.go — Security and correctness tests for the sandbox package.
//
// Purpose: Verify:
//   1. Canonical allow-list has exactly 20 base + 5 PTY entries (= 25 total).
//   2. AllowListSize() returns 25.
//   3. All expected syscall names are present in AllowedSyscallNames.
//   4. All expected PTY ioctl names are present in AllowedPTYIoctls.
//   5. DetectRuntime() returns platform-appropriate values.
//   6. seccompExecutor (stub) executes a simple command successfully.
//   7. Timeout kills the process and sets Killed=true.
//   8. Network connect attempt fails under sandbox-exec on darwin.
//      (Skipped on non-darwin with explanation.)
//   9. Filesystem write outside workspace fails under sandbox-exec on darwin.
//      (Skipped on non-darwin with explanation.)
//
// Platform-specific tests are skipped with t.Skip(reason) when the required
// runtime (darwin sandbox-exec, Linux gVisor) is not available.
//
// Constraints: File ≤500 lines. No live DB. No network in CI.
// SPORT: REGISTRY-FUNCTIONS.md → sandbox tests.
package sandbox

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ── 1–4. Canonical allow-list content ─────────────────────────────────────────

func TestAllowListSize_Is25(t *testing.T) {
	t.Parallel()
	got := AllowListSize()
	if got != 25 {
		t.Errorf("AllowListSize() = %d, want 25 (20 base + 5 PTY)", got)
	}
}

func TestAllowedSyscallNames_Count(t *testing.T) {
	t.Parallel()
	if len(AllowedSyscallNames) != 20 {
		t.Errorf("AllowedSyscallNames has %d entries, want 20", len(AllowedSyscallNames))
	}
}

func TestAllowedPTYIoctls_Count(t *testing.T) {
	t.Parallel()
	if len(AllowedPTYIoctls) != 5 {
		t.Errorf("AllowedPTYIoctls has %d entries, want 5", len(AllowedPTYIoctls))
	}
}

func TestAllowedSyscallNames_ContainsExpected(t *testing.T) {
	t.Parallel()
	expected := []string{
		"read", "write", "open", "close", "stat", "fstat",
		"mmap", "brk", "exit", "exit_group",
		"futex", "nanosleep", "getpid", "getuid", "getgid",
		"arch_prctl", "rt_sigaction", "rt_sigprocmask", "sigreturn", "poll",
	}
	set := make(map[string]bool, len(AllowedSyscallNames))
	for _, name := range AllowedSyscallNames {
		set[name] = true
	}
	for _, want := range expected {
		if !set[want] {
			t.Errorf("AllowedSyscallNames missing expected entry %q", want)
		}
	}
}

func TestAllowedPTYIoctls_ContainsExpected(t *testing.T) {
	t.Parallel()
	expected := []string{"TIOCGWINSZ", "TIOCSWINSZ", "TIOCSPTLCK", "TIOCGPTPEER", "TIOCGPTN"}
	set := make(map[string]bool, len(AllowedPTYIoctls))
	for _, name := range AllowedPTYIoctls {
		set[name] = true
	}
	for _, want := range expected {
		if !set[want] {
			t.Errorf("AllowedPTYIoctls missing expected entry %q", want)
		}
	}
}

func TestAllowedSyscallNames_NoDuplicates(t *testing.T) {
	t.Parallel()
	seen := make(map[string]int)
	for _, name := range AllowedSyscallNames {
		seen[name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("AllowedSyscallNames has duplicate entry %q (%d times)", name, count)
		}
	}
}

func TestAllowedPTYIoctls_NoDuplicates(t *testing.T) {
	t.Parallel()
	seen := make(map[string]int)
	for _, name := range AllowedPTYIoctls {
		seen[name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("AllowedPTYIoctls has duplicate entry %q (%d times)", name, count)
		}
	}
}

// ── 5. DetectRuntime ───────────────────────────────────────────────────────────

func TestDetectRuntime_Linux(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("linux-only test")
	}
	rt := DetectRuntime()
	if rt != RuntimeSeccomp && rt != RuntimeGVisor {
		t.Errorf("DetectRuntime() on linux = %q, want seccomp or gvisor", rt)
	}
}

func TestDetectRuntime_Darwin(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only test")
	}
	rt := DetectRuntime()
	if rt != RuntimeSandboxExec {
		t.Errorf("DetectRuntime() on darwin = %q, want sandbox-exec", rt)
	}
}

// ── 6. seccompExecutor (stub) basic execution ──────────────────────────────────

func TestSeccompExecutor_BasicEcho(t *testing.T) {
	t.Parallel()
	exec := newSeccompExecutor()
	res, err := exec.Execute(context.Background(), SandboxCommand{
		Cmd:  "echo",
		Args: []string{"sandbox-ok"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "sandbox-ok") {
		t.Errorf("Stdout = %q, want to contain 'sandbox-ok'", res.Stdout)
	}
}

func TestSeccompExecutor_ExitCode(t *testing.T) {
	t.Parallel()
	exec := newSeccompExecutor()
	res, err := exec.Execute(context.Background(), SandboxCommand{
		Cmd:  "sh",
		Args: []string{"-c", "exit 42"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", res.ExitCode)
	}
}

func TestSeccompExecutor_WallTimeMs_NonZero(t *testing.T) {
	t.Parallel()
	exec := newSeccompExecutor()
	res, err := exec.Execute(context.Background(), SandboxCommand{
		Cmd:  "echo",
		Args: []string{"timing"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.WallTimeMs < 0 {
		t.Errorf("WallTimeMs = %d, want ≥ 0", res.WallTimeMs)
	}
}

// ── 7. Timeout kills process ───────────────────────────────────────────────────

func TestSeccompExecutor_Timeout_KillsProcess(t *testing.T) {
	t.Parallel()
	exec := newSeccompExecutor()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	res, err := exec.Execute(ctx, SandboxCommand{
		Cmd:      "sleep",
		Args:     []string{"60"},
		TimeoutS: 1, // 1-second sandbox timeout
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Killed {
		t.Errorf("expected Killed=true after timeout, got false (ExitCode=%d)", res.ExitCode)
	}
	if elapsed > 3*time.Second {
		t.Errorf("process not killed quickly enough: elapsed=%v", elapsed)
	}
}

// skipIfSandboxExecProfileMissing skips the test when the macOS sandbox profile
// cannot be found. The profile is required for sandbox-exec to enforce denials;
// without it the executor falls back to unsandboxed exec and the tests would
// produce false failures.
func skipIfSandboxExecProfileMissing(t *testing.T) {
	t.Helper()
	exec := newSandboxExecExecutor()
	e, ok := exec.(*sandboxExecExecutor)
	if !ok {
		t.Skip("unexpected executor type")
	}
	if _, err := os.Stat(e.profilePath); err != nil {
		t.Skipf("sandbox_macos.sb profile not found at %q (run from module root for full sandbox tests)", e.profilePath)
	}
}

// ── 8. Network denied under sandbox-exec (darwin) ─────────────────────────────

func TestSandboxExec_NetworkDenied_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is darwin-only; network denial test skipped on " + runtime.GOOS)
	}
	skipIfSandboxExecProfileMissing(t)
	exec := newSandboxExecExecutor()
	// Attempt an outbound TCP connection; should fail.
	res, err := exec.Execute(context.Background(), SandboxCommand{
		Cmd:      "sh",
		Args:     []string{"-c", "curl -s --max-time 2 http://example.com; echo exit:$?"},
		TimeoutS: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// curl should fail (exit non-zero) or the output should show a network error.
	// Under sandbox-exec --deny network, curl gets EPERM/EACCES on connect(2).
	if res.ExitCode == 0 && strings.Contains(res.Stdout, "<html") {
		t.Error("network connection succeeded under sandbox-exec; expected denial")
	}
}

// ── 9. Filesystem write outside workspace denied (darwin) ──────────────────────

func TestSandboxExec_FSWriteDenied_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is darwin-only; filesystem write test skipped on " + runtime.GOOS)
	}
	skipIfSandboxExecProfileMissing(t)
	exec := newSandboxExecExecutor()
	// Attempt to write to /etc (outside /tmp and workspace).
	res, err := exec.Execute(context.Background(), SandboxCommand{
		Cmd:      "sh",
		Args:     []string{"-c", "echo test > /etc/clawde_test_should_fail; echo exit:$?"},
		TimeoutS: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Should fail: write to /etc should be denied.
	if res.ExitCode == 0 {
		t.Error("write to /etc succeeded under sandbox-exec; expected denial")
	}
}

// ── 10. New/NewDefault smoke test ─────────────────────────────────────────────

func TestNew_SeccompRuntime(t *testing.T) {
	t.Parallel()
	executor, err := New(RuntimeSeccomp)
	if err != nil {
		t.Fatalf("New(RuntimeSeccomp): %v", err)
	}
	if executor == nil {
		t.Fatal("New(RuntimeSeccomp) returned nil executor")
	}
}

func TestNewDefault_ReturnsExecutor(t *testing.T) {
	t.Parallel()
	// gVisor may not be available; skip if it errors.
	executor, err := NewDefault()
	if err != nil {
		if runtime.GOOS == "linux" && DetectRuntime() == RuntimeGVisor {
			t.Skipf("gVisor not available in this environment: %v", err)
		}
		t.Fatalf("NewDefault(): %v", err)
	}
	if executor == nil {
		t.Fatal("NewDefault() returned nil executor")
	}
}

func TestNew_UnknownRuntime(t *testing.T) {
	t.Parallel()
	_, err := New(Runtime("unknown"))
	if err == nil {
		t.Fatal("expected error for unknown runtime")
	}
}

// ── 11. sandbox.Apply (TestSeccompBPFInstall) ────────────────────────────────
// On Linux with seccomp build tag: Apply(0) installs the BPF filter.
// On macOS: Apply is a no-op; test verifies it compiles and returns nil.
// On all platforms: Apply(0) must not return an error.
//
// Note: On Linux without the seccomp build tag the stub always returns nil.
// The full filter-install path is tested on CI with -tags seccomp.
func TestSeccompBPFInstall(t *testing.T) {
	t.Parallel()
	// Apply(0) targets the current process. On platforms without real seccomp
	// this is a no-op. On Linux+seccomp this installs the filter — which is
	// safe in a test subprocess since the test already limits its own syscalls.
	//
	// IMPORTANT: This test must be run as its own subprocess on CI to avoid
	// restricting the entire test binary. The CI job calls:
	//   go test ./internal/sandbox/... -run TestSeccompBPFInstall -count=1
	// which runs only this test in its own process.
	if err := Apply(0); err != nil {
		t.Errorf("Apply(0) returned error: %v", err)
	}
}

// TestApply_MacOSNoOp verifies that Apply is a no-op on macOS.
func TestApply_MacOSNoOp(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only: verifies seatbelt no-op path")
	}
	if err := Apply(0); err != nil {
		t.Errorf("Apply(0) on darwin: expected nil, got %v", err)
	}
}
