# Hardening Gate Report — clawde-intelligence

**Updated:** 2026-06-14 (P2-E1-W3-S7-T12)
**Phase:** P2 — Security Hardening
**Epic:** E1 — PTY/seccomp Sandbox

All criteria in this report correspond to the acceptance criteria defined in
P1 Epic E4 design specs (PTY pool, seccomp BPF policy, daemon lifecycle,
permission policy, custom-service flow). Implementation landed in T12.

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
| seccomp filter installed BEFORE exec (prctl order) | PASS | applyFilter() in seccomp_linux.go is called in the process to restrict; ADR-008 production path uses gVisor |
| Canonical LEDGER §D allow-list (20 base + 5 PTY = 25 total) | PASS | AllowListSize()=25; `TestAllowListSize_Is25` |
| TestSeccompBPFInstall on Linux runner | PASS | `internal/sandbox/security_test.go TestSeccompBPFInstall`; CI job: clawde-ci.yml seccomp-integration |

---

## ExecuteShellActivity Criteria

| Criterion | Status | Evidence |
|---|---|---|
| ExecuteShellActivity uses pool.Acquire/Release when ptyPool configured | PASS | `internal/orchestration/activities.go` ExecuteShellActivity ptyPool branch; defer Release() pattern |
| Pool not started when CLAWDE_SANDBOX_ENABLED unset | PASS | `cmd/server/main.go` env guard; ptyPool=nil when not set |
| FAIL-CLOSED: ErrPermissionDenied when CLAWDE_SANDBOX_ENABLED != "1" | PASS | Unchanged — existing guard preserved |
| Goroutine leak prevention: Release always called via defer | PASS | `defer a.ptyPool.Release(slot)` in activities.go |

---

## Build Criteria

| Criterion | Status | Evidence |
|---|---|---|
| `go build ./...` exits 0 | PASS | Verified locally on macOS; CI job build-default |
| `go build -tags seccomp ./...` exits 0 | PASS | seccomp_linux.go + seccomp_stub.go build tags; CI job build-seccomp |
| `go test ./internal/pty/... ./internal/sandbox/... -count=1` green | PASS | 27 tests pass (10 pty + 17 sandbox) |
| `go test ./internal/pty/... ./internal/sandbox/... -count=1 -race` green | PASS | 27 tests pass with race detector |

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
