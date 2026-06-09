# ClawDE CRD Parity Adapter — Specification

**Feature:** F-CLAWDE:crd-parity-adapter
**Ticket:** P1-E2-W4-S04-T03
**ADR refs:** ADR-001 (service boundary), ADR-008 (hook contract)
**Status:** Planned (P1 E2 W4 S04)

---

## Overview

The CRD parity adapter is the component inside `clawd` (the host-local Rust daemon) that reads
nSelf PCI inbox messages from `.claude/inbox/msg-*.md`, routes them to local handlers, and
archives processed messages to `.claude/archive/inbox/`. It provides local handling for the
subset of CRD message types that can be resolved without running the full `~/.claude-relay/`
CRD chain daemon — without ever touching CRD relay state files.

This adapter is defined by ADR-001 § `clawd` responsibilities:

> `clawd` owns: ... CRD parity, inbox watcher

The inbox watcher (P1-E2-W5-S05-T01) drives the filesystem polling loop; this spec defines the
routing, state handling, and escalation behaviour that the watcher delegates to.

---

## 1. Supported Message Types

All 10 CRD message types are accounted for. Locally-handled types are processed in-process
by `clawd`; CRD-only types are escalated via a fresh PCI write to the target project's inbox.

| CRD Type | Local Handler | Notes |
|---|---|---|
| `bug` | `open-task` — create a 🔒 blocked task in `.claude/tasks/active.md` with full message body | Retries on handler failure |
| `hotfix` | `open-priority-task` — same as bug but P0 priority | Retries on handler failure |
| `question` | `notify-and-prompt` — surface notification to user; record in `.claude/inbox/deferred/` until answered | Retries on handler failure |
| `info` | `notify` — write user-visible notification; archive message immediately | No retry (informational only) |
| `resolution` | `close-related-task` — find the task referenced by `chain_id`; mark it done or unblocked | No retry (idempotent close) |
| `test-request` | `open-test-task` — create test task in `.claude/tasks/active.md` | Retries on handler failure |
| `feature` | `add-to-ideas` — append to `.claude/ideas/` with source attribution | No retry (safe append) |
| `idea` | `add-to-ideas` — append to `.claude/ideas/` | No retry |
| `enhancement` | `add-to-ideas` — append to `.claude/ideas/` | No retry |
| `deploy-request` | **CRD-only** — escalate via `pci-send` to target project inbox; never handle locally | Escalation; no local retry |

**CRD-only designation:** `deploy-request` requires the full CRD relay (credentials, server auth,
idempotency ledger). The local adapter writes a new PCI to the target project's inbox and logs
the escalation, but it does not attempt to fulfill the deploy. See § 6 — Escalation Handoff.

---

## 2. Chain Metadata Compatibility Table

The local adapter reads and writes a compatible subset of the CRD chain schema. Fields not
supported locally are silently preserved (pass-through) on archive writes.

| Field | CRD Schema Type | Local Adapter Type | Deviation | Rationale |
|---|---|---|---|---|
| `chain_id` | `string (UUID v4)` | `string` | None | Identical; used as task cross-reference key |
| `from` | `string (project-path or "crd")` | `string` | None | Preserved verbatim from inbound message |
| `to` | `string (project-path)` | `string` | None | Must equal current project's `.claude/` root to accept |
| `subject` | `string` | `string` | None | Used as task title prefix |
| `priority` | `enum: critical|high|medium|low` | `enum: critical|high|medium|low` | None | Maps directly to ATP priority (P0=critical, P1=high, P2=medium, P3=low) |
| `type` | `enum: 10 values` | `enum: 10 values` | None | See § 1 for full mapping |
| `status` | `enum: pending|open|replied|resolved|wontfix|answered|deferred|failed` | `enum: same` | None | Subset used; local adapter only writes terminal states on archive |
| `reply_to` | `string (inbox-path)` | `string` | None | Used verbatim for reply PCI routing |

**ClawDE-local-only extension fields** (not part of CRD schema; appended only to the archived
copy, never sent outbound):

| Field | Type | Purpose |
|---|---|---|
| `processing_ts` | `string (ISO 8601)` | Timestamp when the local adapter processed the message |
| `local_handler` | `string` | Name of the handler invoked (e.g. `open-task`, `notify`) |
| `local_result` | `enum: success|failed|escalated` | Outcome of local processing |
| `retry_count` | `integer` | Number of handler retries attempted (0 = first attempt) |

---

## 3. Read/Write Boundary

The CRD parity adapter enforces a strict filesystem boundary:

**Reads:**
- `.claude/inbox/msg-*.md` — inbound PCI messages (filesystem polling by inbox watcher)
- `.claude/tasks/active.md` — to locate tasks referenced by `chain_id` (for `resolution` handler)

**Writes (local):**
- `.claude/tasks/active.md` — append new tasks (bug, hotfix, test-request handlers)
- `.claude/ideas/{slug}.md` — append new ideas (feature, idea, enhancement handlers)
- `.claude/inbox/deferred/{slug}.md` — park messages awaiting user reply (question handler)
- `.claude/inbox/failed/{slug}.md` — permanently failed messages with failure reason appended
- `.claude/archive/inbox/{original-filename}` — all processed messages, with extension fields appended

**NEVER writes to:**
- `~/.claude-relay/` — this is the CRD relay daemon's exclusive state directory. The local adapter has no read or write access to it. Any attempt to write here is a hard contract violation.
- Any other project's `.claude/inbox/` **directly** — escalation goes through `pci-send`, which writes to the target project's inbox at the PPI level. The adapter itself does not construct file paths outside the current project.

The `to` field of an inbound message is validated against the current project root before
any processing begins. Messages addressed to a different project are skipped with a warning.

---

## 4. Terminal State Handling

Each CRD terminal status maps to a specific local action:

| Terminal Status | Local Action | User Notification |
|---|---|---|
| `resolved` | Archive message to `.claude/archive/inbox/`; if a related task exists (by `chain_id`), mark it ✅ Done | Yes — user-visible notification that the chain is resolved |
| `wontfix` | Archive message; if a related task exists, mark it 🚫 Canceled with reason from message body | Yes — notification with reason |
| `answered` | Archive message; if a related task exists (`question` type), mark it ✅ Done | Yes — notification with answer summary |
| `deferred` | Move to `.claude/inbox/deferred/{slug}.md`; append `deferred_until` metadata if present in message | Yes — notification with deferred-until timestamp |

**Non-terminal statuses** (`pending`, `open`, `replied`) are processing states. The local adapter
only writes terminal statuses on archive; it does not modify the `status` field of messages still
in the inbox.

---

## 5. Retry Policy

The retry policy applies to handler invocations that fail (handler throws, times out, or
returns an error). It does NOT apply to informational or idempotent handlers (`info`,
`resolution`, `feature`, `idea`, `enhancement`).

**Retryable types:** `bug`, `hotfix`, `question`, `test-request`
**Non-retryable types:** `info`, `resolution`, `feature`, `idea`, `enhancement`, `deploy-request`

**Retry decision matrix:**

| Type | Retry? | Reason |
|---|---|---|
| `bug` | Yes | Task creation failure is transient; message must not be lost |
| `hotfix` | Yes | P0 priority; must not be silently dropped |
| `question` | Yes | User notification may be asynchronous; retry ensures delivery |
| `test-request` | Yes | Task creation failure is transient |
| `info` | No | Informational; best-effort; no task created |
| `resolution` | No | Idempotent close; if task not found, log warning and archive |
| `feature` | No | Ideas append is atomic; failure means ideas file corruption (escalate, not retry) |
| `idea` | No | Same as feature |
| `enhancement` | No | Same as feature |
| `deploy-request` | No | Always escalated to CRD; escalation itself is idempotent via pci-send |

**Retry schedule:**

| Attempt | Delay before retry |
|---|---|
| 1st retry | 5 seconds |
| 2nd retry | 15 seconds |
| 3rd retry | 45 seconds |
| After 3rd retry | Permanent failure |

**Permanent failure handling:** after 3 failed retries, the message is archived to
`.claude/inbox/failed/{original-filename}` with the following fields appended to the YAML
front-matter:

```yaml
failure_reason: "<handler name>: <last error message>"
failed_at: "<ISO 8601 timestamp>"
retry_count: 3
local_result: failed
```

The user receives a notification about the permanent failure. No further automatic retry
is attempted. The failed message is preserved for manual inspection or re-queue.

---

## 6. CRD Escalation Handoff Protocol

When the local adapter cannot handle a message (either `deploy-request` type, or a retryable
handler reaches permanent failure), it escalates via the GCI inbox protocol:

**Escalation steps:**

1. Identify the target project inbox path (PPI level):
   - For `deploy-request`: use the `to` field of the message to determine the target project path
   - For permanent failure escalation: re-send to the original sender's `reply_to` path

2. Write a new PCI message using `pci-send`:
   ```bash
   pci-send <project> <slug> <priority> <type> "<subject>" <<'BODY'
   ## Context
   [Original chain_id and summary of what was attempted locally]

   ## Request
   [What CRD relay or human needs to do to resolve this]

   ## Details
   [Original message body + retry history if applicable]
   BODY
   ```

3. Archive the original message to `.claude/archive/inbox/` with `local_result: escalated`.

4. **NEVER** attempt to write to `~/.claude-relay/` at any point in this process.

**Key constraint:** The `pci-send` script writes to the PPI-level inbox of the target project
(`{project-root}/.claude/inbox/`). The adapter constructs the target path from the message's
`to` field and the known project root map — it does NOT construct arbitrary filesystem paths.

---

## 7. Integration Test Plan

To verify the CRD parity adapter without running a live CRD relay:

### Test Setup

Drop a mock CRD-format message into `.claude/inbox/`:

```bash
# Filename follows CRD convention: msg-YYYY-MM-DD-<slug>.md
cat > /path/to/project/.claude/inbox/msg-2026-06-01-test-bug-report.md <<'MSG'
---
chain_id: "test-chain-001-mock"
from: "nself (/Volumes/X9/Sites/nself)"
to: "clawde (/Volumes/X9/Sites/nself/clawde)"
subject: "Test: mock bug report for parity adapter"
priority: medium
type: bug
status: pending
reply_to: "/Volumes/X9/Sites/nself/.claude/inbox"
---

## Context
This is a mock CRD-format message used to test the parity adapter.

## Request
Verify that the adapter opens a task and archives this message.

## Details
No real bug; integration test only.
MSG
```

### Verification Checks

| Check | Expected Outcome | How to verify |
|---|---|---|
| **Routing** | Handler `open-task` invoked for `type: bug` | Task appears in `.claude/tasks/active.md` with subject as title |
| **Task content** | Task contains `chain_id: test-chain-001-mock` | `grep "test-chain-001-mock" .claude/tasks/active.md` |
| **Archive** | Original message moved to `.claude/archive/inbox/` | `ls .claude/archive/inbox/msg-2026-06-01-test-bug-report.md` |
| **Archive metadata** | `processing_ts` and `local_handler: open-task` appended | `grep "processing_ts" .claude/archive/inbox/msg-2026-06-01-test-bug-report.md` |
| **CRD state untouched** | `~/.claude-relay/` is unchanged | `ls -la ~/.claude-relay/` before and after — timestamps must not change |
| **Inbox cleared** | Original file no longer in `.claude/inbox/` | `ls .claude/inbox/msg-2026-06-01-test-bug-report.md` returns not-found |

### Negative Test

Drop a `deploy-request` type message and verify escalation:

- `type: deploy-request` message placed in inbox
- Verify: no local task created
- Verify: a new PCI written to target project inbox via `pci-send`
- Verify: original message archived to `.claude/archive/inbox/` with `local_result: escalated`
- Verify: `~/.claude-relay/` is still untouched

---

## See Also

- ADR-001 — ClawDE service boundary (clawd owns CRD parity)
- ADR-004 — Local auth model
- ADR-008 — Host adapter and hook contract
- GCI inbox protocol — `~/.claude/references/inbox-protocol.md`
- Inbox watcher spec — `P1-E2-W5-S05-T01` (drives filesystem polling that feeds this adapter)
- `~/.claude-relay/` — CRD relay state (NEVER touched by this adapter)
