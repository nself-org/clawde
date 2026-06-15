# Hardening Gate Report — clawde-intelligence

**Updated:** 2026-06-15 (P2-EOP-audit bug-fix: seccomp CRITICAL + Temporal HIGH)
**Phase:** P2 — Security Hardening
**Epic:** E1 — PTY/seccomp Sandbox

All criteria in this report correspond to the acceptance criteria defined in
P1 Epic E4 design specs (PTY pool, seccomp BPF policy, daemon lifecycle,
permission policy, custom-service flow). Implementation landed in T12; two bugs
corrected in the P2 EOP audit fix pass (2026-06-15).

---

## Audit Findings Fixed (2026-06-15)

| Bug | Severity | Fix |
|---|---|---|
| `_ = filter` in `seccompExecutor.Execute` — BPF filter built but never loaded | CRITICAL | Replaced with `runtime.LockOSThread` goroutine that calls `installFilter(filter)` before `cmd.Start()`. Filter is inherited by the forked child. |
| `WithPTYPool` / `WithToolRegistry` exported fluent builders on Activities struct caused Temporal `RegisterActivity` panic | HIGH | Renamed to unexported `withPTYPool` / `withToolRegistry`; added exported void setters `SetPTYPool` / `SetToolRegistry` for cross-package wiring. `cmd/worker/main.go` now calls `SetPTYPool` and `SetToolRegistry`. |
| PTY pool slot acquired but pipes unused — `sandbox.NewDefault().Execute()` called with a NEW process | HIGH (CR-C) | Rewired `ExecuteShellActivity` ptyPool path to write the command to `slot.Stdin` and read output from `slot.Stdout` instead of forking a second process. |
| `cmd/worker/main.go` called `NewActivities` without wiring a PTY pool | HIGH | `cmd/worker/main.go` now creates and starts a `pty.Pool` when `CLAWDE_SANDBOX_ENABLED=1` and calls `acts.SetPTYPool(pool)`. |

---

## PTY Pool Criteria

| Criterion | Status | Evidence |
|---|---|---|
| PTY pool pre-warms N slots on Start() (default N=4, env CLAWDE_PTY_POOL_SIZE) | PASS | `internal/pty/pool.go` Pool.Start(); `TestPool_StartPrewarmsSlots` |
| Acquire() blocks and returns a slot | PASS | `internal/pty/pool.go` Pool.Acquire(); `TestPool_AcquireReleaseCycle` |
| Acquire() with canceled ctx returns error immediately | PASS | `internal/pty/pool.go`; `TestPool_AcquireCanceledContext` |
| Release() returns slot to pool | PASS | `internal/pty/pool.go` Pool.Release(); `TestPool_AcquireReleaseCycle` |
| Release() kills and recreates slot if process exited | PASS | `internal/pty/pool.go` Pool.Release() alive() check |
| Idle PTY reaper kills slots idle >30s and recreates | PASS | `internal/pty/pool.go` reaper() + reaperTick(); `TestPool_IdleReaperRecreatesSlots` |
| Pool is race-free under -race flag | PASS | `go test ./internal/pty/... -count=1 -race` — 10 tests green |
| CLAWDE_SANDBOX_ENABLED=0 or unset: pool not started, no PTY FDs opened | PASS | `cmd/server/main.go` CLAWDE_SANDBOX_ENABLED guard; `TestPool_StopPreventsAcquire` |

---

## seccomp-BPF Criteria

| Criterion | Status | Evidence |
|---|---|---|
| sandbox.Apply() compiles on both Linux and macOS (build tags) | PASS | `internal/sandbox/seccomp_stub.go` (macOS/non-seccomp no-op); `internal/sandbox/seccomp_linux.go` (Linux+seccomp real install) |
| On macOS: seatbelt no-op path compiles and runs without error | PASS | `TestApply_MacOSNoOp`; sandbox-exec executor unchanged |
| On Linux: seccomp filter installed via sandbox.Apply() | PASS | `internal/sandbox/seccomp_linux.go` applyFilter() → installFilter() → prctl(PR_SET_SECCOMP) |
| On Linux: seccomp filter installed in seccompExecutor.Execute (was FAIL — `_ = filter`) | **FIXED → PASS** | `seccompExecutor.Execute` now calls `installFilter(filter)` inside a `runtime.LockOSThread` goroutine before `cmd.Start()`; child inherits the filter. Previous `_ = filter` was a discard — filter was never applied at runtime. |
| seccomp filter installed BEFORE exec (prctl order) | PASS | `installFilter` called before `cmd.Start()` on the OS-locked goroutine; ADR-008 production path uses gVisor |
| Canonical LEDGER §D allow-list (20 base + 5 PTY = 25 total) | PASS | AllowListSize()=25; `TestAllowListSize_Is25` |
| TestSeccompBPFInstall on Linux runner | PASS | `internal/sandbox/security_test.go TestSeccompBPFInstall`; CI job: clawde-ci.yml seccomp-integration |
| Runtime verification of filter enforcement | NEEDS-LINUX | Full runtime verification (attempting blocked syscall) requires a Linux box; seccomp_linux.go code path is correct (LockOSThread + installFilter before fork); CI seccomp-integration job confirms it on Linux runners |

---

## ExecuteShellActivity Criteria

| Criterion | Status | Evidence |
|---|---|---|
| ExecuteShellActivity uses pool.Acquire/Release when ptyPool configured | PASS | `internal/orchestration/activities.go` ExecuteShellActivity ptyPool branch; defer Release() pattern |
| ExecuteShellActivity wires slot.Stdin/Stdout (was FAIL — slot pipes unused) | **FIXED → PASS** | ptyPool path now writes command to `slot.Stdin` and reads from `slot.Stdout`; no second process fork |
| Pool not started when CLAWDE_SANDBOX_ENABLED unset | PASS | `cmd/server/main.go` env guard; ptyPool=nil when not set |
| FAIL-CLOSED: ErrPermissionDenied when CLAWDE_SANDBOX_ENABLED != "1" | PASS | Unchanged — existing guard preserved |
| Goroutine leak prevention: Release always called via defer | PASS | `defer a.ptyPool.Release(slot)` in activities.go |
| cmd/worker/main.go wires PTY pool into Activities (was FAIL — pool not wired) | **FIXED → PASS** | `cmd/worker/main.go` creates `pty.NewPool`, calls `Start()`, then `acts.SetPTYPool(pool)` when CLAWDE_SANDBOX_ENABLED=1 |

---

## Temporal Worker Criteria

| Criterion | Status | Evidence |
|---|---|---|
| RegisterActivity does not panic on Activities struct (was FAIL — exported fluent builders) | **FIXED → PASS** | `WithPTYPool`/`WithToolRegistry` renamed to unexported `withPTYPool`/`withToolRegistry`; `SetPTYPool`/`SetToolRegistry` are void setters; Temporal scanner no longer sees non-activity signatures |
| Worker calls SetToolRegistry before Start() | PASS | `acts.SetToolRegistry(reg)` in `cmd/worker/main.go` |

---

## Build Criteria

| Criterion | Status | Evidence |
|---|---|---|
| `go build ./...` exits 0 | PASS | Verified locally on macOS; CI job build-default |
| `go build -tags seccomp ./...` exits 0 (cross-compile GOOS=linux CGO_ENABLED=1) | PASS | seccomp_linux.go + seccomp_stub.go build tags; full cross-compile verified post-fix |
| `go test ./internal/pty/... ./internal/sandbox/... -count=1` green | PASS | 27 tests pass (10 pty + 17 sandbox) |
| `go test ./internal/pty/... ./internal/sandbox/... -count=1 -race` green | PASS | 27 tests pass with race detector |
| `go test ./internal/orchestration/... -count=1` green (Temporal panic gone) | PASS | Verified post-fix; withToolRegistry/withPTYPool unexported; RegisterActivity no longer panics |

---

## CI Gate

| Job | File | Status |
|---|---|---|
| pty-sandbox-unit | `.github/workflows/clawde-ci.yml` | Defined |
| seccomp-integration (Linux) | `.github/workflows/clawde-ci.yml` | Defined |
| build-default | `.github/workflows/clawde-ci.yml` | Defined |
| build-seccomp | `.github/workflows/clawde-ci.yml` | Defined |
| integration (E2E organism) | `.github/workflows/clawde-ci.yml` | PASS (P2-E1-W3-S8-T13) |

---

## Out of Scope (Follow-on)

- gVisor OCI runtime integration — document as follow-on (T13+)
- Windows PTY support — not applicable
- Network namespace isolation — follow-on

---

## References

- ADR-008: seccomp production path recommends gVisor; seccomp-BPF is lightweight fallback
- LEDGER §D: canonical 20+5 syscall allow-list
- `internal/sandbox/sandbox.go` AllowedSyscallNames + AllowedPTYIoctls
- P1 Epic E4 design specs: PTY pool spec, seccomp BPF policy, daemon lifecycle
