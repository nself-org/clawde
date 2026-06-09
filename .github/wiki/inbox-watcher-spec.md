# Inbox Watcher and Routing Rules

ClawDE's inbox watcher monitors `.claude/inbox/msg-*.md` files dropped into any watched
project's inbox directory and routes each message to the correct handler based on CRD message type.

---

## Watch Path and Strategy

### Watched Path Pattern

```
~/{project}/.claude/inbox/msg-*.md
```

The watcher monitors all paths listed in `~/.config/clawde/inbox-routes.toml`. Each entry
specifies a project name and the absolute inbox path. Paths outside this list are explicitly
rejected as scope violations (see Scope Check Gate below).

### Native Event Strategy (per platform)

| Platform | Primary mechanism | Fallback |
|---|---|---|
| macOS | `kqueue` via the `notify` crate | Polling at 2s interval |
| Linux | `inotify` via the `notify` crate | Polling at 2s interval |
| Windows | `ReadDirectoryChangesW` via the `notify` crate | Polling at 2s interval |

**Default polling interval:** 2 seconds. Configurable in `~/.config/clawde/inbox-routes.toml`
via the `poll_interval_ms` field (minimum: 500ms; maximum: 30000ms).

The `notify` crate is used for all platforms to abstract native event backends. If the native
backend fails to initialise (e.g., inotify fd limit exhausted), the watcher automatically
falls back to polling and emits a `WARN` log entry.

### File Stabilization Guard (Step 1 — fires before debounce)

On receiving a filesystem event for a candidate `msg-*.md` file, the watcher first applies
a **50ms stabilization guard**:

- The file must have `size > 0` (non-empty)
- The file must not have been modified in the last 50ms (measured from the last FS event for
  that path)
- Only after both conditions are satisfied does the watcher start the 500ms debounce timer

This guard prevents reading a partially-written file during an in-progress editor write flush.

**Order is mandatory (LEDGER §G ORDER):** stabilization guard fires FIRST; the 500ms debounce
timer starts ONLY after the stabilization guard passes. These are sequential, not concurrent.

### Debounce (Step 2 — starts after stabilization guard passes)

- **Window:** 500ms minimum from first event that passed the stabilization guard, for a given path
- **Same-file deduplication:** If multiple `CREATE`/`MODIFY` events arrive for the same file
  during the 500ms debounce window, the debounce timer is reset; only one processing pass triggers
- **Same-file re-creation:** A file-replace (delete + re-create at same path) is treated as a
  new event; the debounce timer is reset from scratch

### `chain_id` Deduplication (Hold-Until-Terminal)

Once a file passes stabilization + debounce and is dispatched to a handler, the watcher records
the `chain_id` parsed from the message YAML front-matter. The dedup rule spans across debounce
windows (not limited to a single window):

- If a second file arrives with the same `chain_id` while the first is in-flight (being handled),
  the second message is held in the queue until the first handler reaches a terminal state
  (`done` or `failed`)
- After the first handler terminates, the held message is released from the queue for normal dispatch
- This is not a collapse (both messages are processed); it is a sequencing guarantee for the same
  logical chain

---

## Routing Rules Table

All 10 CRD message types are routed as follows:

| Type | Handler | Priority | Max concurrency |
|---|---|---|---|
| `bug` | `BugHandler` | P1 (highest) | 1 |
| `hotfix` | `HotfixHandler` | P1 (highest) | 1 |
| `test-request` | `TestRequestHandler` | P2 | 2 |
| `resolution` | `ResolutionHandler` | P2 | 2 |
| `question` | `QuestionHandler` | P3 | 5 |
| `deploy-request` | `DeployRequestHandler` | P1 (highest) | 1 |
| `feature` | `FeatureHandler` | P3 | 5 |
| `idea` | `IdeaHandler` | P4 (lowest) | 5 |
| `enhancement` | `EnhancementHandler` | P3 | 5 |
| `info` | `InfoHandler` | P4 (lowest) | 5 |

**Priority levels:**
- **P1 (highest):** dispatched immediately, bypasses queue
- **P2:** normal dispatch, enters queue ahead of P3/P4
- **P3:** normal dispatch
- **P4 (lowest):** dispatched only when no P1–P3 messages are pending

**Handler interface contract:**

```rust
/// Purpose: Process a single CRD inbox message
/// Inputs: parsed MessageEnvelope (type, chain_id, priority, body, source path)
/// Outputs: HandlerResult { status, archive_path, error_details }
/// Constraints: must complete within handler_timeout_ms (default: 30s)
/// SPORT: F-CLAWDE:inbox-watcher
pub trait InboxHandler: Send + Sync {
    fn message_type(&self) -> &'static str;
    async fn handle(&self, envelope: MessageEnvelope) -> HandlerResult;
}
```

**Concurrency model:** The dispatcher maintains a per-type goroutine pool bounded by the
`max_concurrency` value above. Messages are dispatched asynchronously; excess messages queue
in the backpressure buffer (see below).

---

## Backpressure

| Parameter | Value | Notes |
|---|---|---|
| Max in-flight per project | 5 | Across all message types for that watched project |
| Queue size limit | 100 messages | In-memory FIFO; bounded per watched project |
| Overflow behaviour | Drop oldest + log + user notification | When queue > 100, oldest message is dropped and a `WARN` is emitted; a desktop notification is pushed via the notification subsystem |

**Queue depth metric:** The current queue depth is exposed as an observable metric:
`clawde_inbox_queue_depth{project="<name>"}` (Prometheus-compatible format). The ClawDE
daemon Doctor check `inbox_queue` reads this metric.

---

## Failure Recovery

Handler crashes are isolated per message:

1. If a handler panics or returns an error, the error is caught at the dispatch boundary
2. The message is moved to `.claude/inbox/failed/` with error metadata appended
3. The watcher continues processing the next message — a single failure does not stop the watcher

**Failed message schema** (appended as YAML front-matter at the end of the original message):

```yaml
## clawde_failure_metadata
failed_at: "2026-06-01T12:00:00Z"
error_type: "HandlerPanic"
error_detail: "thread 'bug-handler' panicked at 'unwrap on None value'"
retry_count: 0
max_retries: 3
permanent_failure: false
```

**Retry policy:**
- Max 3 retries with exponential backoff (5s / 15s / 45s)
- After 3 failed retries, `permanent_failure: true` is set and the file remains in `failed/`
- Permanent failures are surfaced in the ClawDE daemon Doctor output

---

## Archive Rule

After a message is successfully processed, it is moved to `.claude/archive/inbox/`:

1. The original message content is preserved verbatim
2. A processing timestamp is appended as YAML front-matter:

```yaml
## clawde_archive_metadata
archived_at: "2026-06-01T12:01:05Z"
processed_by: "clawd-inbox-watcher"
handler: "BugHandler"
duration_ms: 312
```

**Atomicity guarantee:** The archive move uses `rename(2)` (atomic on POSIX when source and
destination are on the same filesystem). On Windows, `MoveFileExW` with
`MOVEFILE_REPLACE_EXISTING`. If source and destination are on different filesystems (rare
edge case: inbox on one mount, archive on another), a copy-then-delete is performed and the
move is logged as non-atomic.

---

## Scope Check Gate

Before dispatching any message, the watcher verifies the source inbox path is in the allowed
project scope list.

**Config file:** `~/.config/clawde/inbox-routes.toml`

```toml
[projects.nself]
inbox_path = "/Volumes/X9/Sites/nself/.claude/inbox"
archive_path = "/Volumes/X9/Sites/nself/.claude/archive/inbox"

[projects.unity]
inbox_path = "/Volumes/X9/Sites/unyeco/.claude/inbox"
archive_path = "/Volumes/X9/Sites/unyeco/.claude/archive/inbox"
```

**Enforcement:**

- A file event from a path NOT in `inbox-routes.toml` is a **hard error**, not a warning
- The error is logged at `ERROR` level with the offending path
- No message processing occurs
- The watcher does NOT attempt to guess the project or fall through to a default handler
- The event is counted in the `clawde_inbox_scope_violations_total` metric

---

## Integration Test

To verify the full routing, archive, scope guard, deduplication, and failure isolation:

```bash
# 1. Normal routing: drop a test bug message into an allowed project inbox
cat > /tmp/test-inbox-msg.md << 'EOF'
---
type: bug
chain_id: test-001
priority: high
---
Test bug message
EOF
cp /tmp/test-inbox-msg.md ~/Sites/nself/.claude/inbox/msg-2026-06-01-test-001.md
# Expected within 3s: daemon log "dispatched to BugHandler chain_id=test-001"
# Expected: msg-2026-06-01-test-001.md present in archive (.claude/archive/inbox/)
#   with clawde_archive_metadata appended

# 2. chain_id deduplication: rapid-drop two messages with same chain_id
cat > /tmp/test-dedup-a.md << 'EOF'
---
type: bug
chain_id: dedup-chain-001
priority: high
---
First message for dedup chain
EOF
cat > /tmp/test-dedup-b.md << 'EOF'
---
type: bug
chain_id: dedup-chain-001
priority: high
---
Second message (duplicate chain_id)
EOF
cp /tmp/test-dedup-a.md ~/Sites/nself/.claude/inbox/msg-2026-06-01-dedup-a.md
cp /tmp/test-dedup-b.md ~/Sites/nself/.claude/inbox/msg-2026-06-01-dedup-b.md
# Expected: first message dispatched immediately
# Expected: second message held in queue until first completes or fails
#   (hold-until-terminal dedup behavior; NOT a debounce-window collapse)

# 3. Scope guard: drop a file outside allowed paths
cp /tmp/test-inbox-msg.md /tmp/inbox-scope-violation.md
# Inject a fake watch event from an out-of-scope path — watcher must log ERROR, not dispatch
# Expected: clawde_inbox_scope_violations_total increments by 1; no BugHandler invocation

# 4. Handler failure isolation: kill handler mid-execution, confirm watcher continues
# Simulate by dropping a message whose handler panics (use a synthetic test handler in dev mode)
cat > /tmp/test-panic-msg.md << 'EOF'
---
type: bug
chain_id: panic-test-001
priority: high
x-test-inject-panic: true
---
Message designed to trigger handler panic in test mode
EOF
cp /tmp/test-panic-msg.md ~/Sites/nself/.claude/inbox/msg-2026-06-01-panic-test.md
# Expected: handler panic is isolated; message moved to .claude/inbox/failed/
# Expected: watcher continues and processes the next queued message without interruption
# Expected: daemon log contains handler error entry with retry_count=0
```

---

## Configuration Reference

`~/.config/clawde/inbox-routes.toml` full schema:

```toml
poll_interval_ms = 2000           # fallback polling interval (ms); min 500
debounce_window_ms = 500          # debounce window (ms); minimum enforced
queue_size_per_project = 100      # max in-memory queue depth per project
max_inflight_per_project = 5      # max concurrent in-flight handlers
handler_timeout_ms = 30000        # per-handler timeout; default 30s

[projects.<name>]
inbox_path = "/abs/path/.claude/inbox"
archive_path = "/abs/path/.claude/archive/inbox"
```

---

## SPORT Reference

- Feature ID: `F-CLAWDE:inbox-watcher`
- Epic: E2 · Wave: W5 · Sprint: S05
- Ticket: P1-E2-W5-S05-T01
- Status: 🔲 Planned (spec complete; implementation in W6+)

**See also:**
- [Architecture.md](Architecture.md) — Inbox Watcher section for system context
- [daemon-spec.md](daemon-spec.md) — daemon lifecycle and IPC
- [mcp-server-spec.md](mcp-server-spec.md) — MCP tool surface
