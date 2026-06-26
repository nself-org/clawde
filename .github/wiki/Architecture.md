# Architecture

## Overview

ClawDE is built around a single principle: **the daemon is the source of truth, not the UI.**

```
┌─────────────────────────────────────────────────────┐
│                     User machine                    │
│                                                     │
│  ┌─────────────┐   JSON-RPC 2.0    ┌─────────────┐  │
│  │ Desktop app │◄──── WebSocket ───►│    clawd    │  │
│  │ (Tauri 2 +  │   ws://127.0.0.1  │  (Rust/    │  │
│  │  React 19)  │       :4300       │   Tokio)    │  │
│  └─────────────┘                   │             │  │
│                                    │  SQLite DB  │  │
│  ┌─────────────┐                   │  Git2       │  │
│  │ Mobile app  │◄── relay (mTLS) ──│  AI runners │  │
│  │ (RN + Expo) │   api.clawde.io   │             │  │
│  └─────────────┘                   └─────────────┘  │
└─────────────────────────────────────────────────────┘
```

## Components

### `clawd` — Rust daemon

The daemon runs as a background process on the user's machine. It:

- Manages **AI sessions** — creates, pauses, resumes, and closes them
- **Spawns AI providers** as subprocesses (`claude`, `codex`, `cursor`, etc.) and streams their output
- Maintains a **SQLite database** (WAL mode) of all sessions, messages, tool calls, and settings
- Watches the **filesystem** for changes via `notify` and tracks git state via `git2`
- Serves a **JSON-RPC 2.0 WebSocket server** on `ws://127.0.0.1:4300`
- Pushes **server events** to connected clients (new messages, tool calls, status changes)

### Desktop app — Tauri 2 + React 19

The desktop app is a **thin client** — it contains UI and desktop-platform code only. All state lives in the daemon. Migrated from Flutter to Tauri 2 + React 19 in P3-E4 (2026-06-16).

- Multi-pane layout: session list → chat → code editor
- Native OS menus (macOS menu bar, Windows title bar) via Tauri 2 native menu APIs
- Keyboard shortcuts optimized for developers
- Code editor powered by CodeMirror 6 via WebView
- Platform runners for macOS, Windows, and Linux
- `@nself/tauri-bridge`, `@nself/types`, `@nself/errors` packages for shared TS types + IPC bridge

### Mobile app — React Native + Expo

A companion app for monitoring and responding to sessions from a phone. Migrated from Flutter to React Native + Expo SDK 53 (RN 0.79.7, React 19) in P3-E4 (2026-06-16).

- Session list with status indicators
- Full chat view with tool-call approval
- Bottom-navigation shell optimized for touch
- Platform runners for iOS and Android
- `@nself/native-bridge`, `@nself/errors` packages for shared TS types + JSI bridge

### Shared TypeScript packages

| Package | Purpose |
| --- | --- |
| `@nself/types` | Shared TypeScript types mirroring the JSON-RPC protocol (`Session`, `Message`, `ToolCall`, etc.) |
| `@nself/tauri-bridge` | Typed Tauri IPC bridge for desktop app |
| `@nself/native-bridge` | Typed JSI bridge for mobile app |
| `@nself/errors` | Shared error types used by both apps |

> **Note:** The legacy Flutter/Dart packages (`clawd_proto`, `clawd_client`, `clawd_core`, `clawd_ui`) are archived in `apps/packages-flutter-archive/` (DEPRECATED, P3-E4). Do not use.

### `clawde-intelligence` — Go service (P1)

A separate Go service (`clawde/intelligence/`) that handles all LLM routing, semantic search, and evaluation for ClawDE. Communicates with `clawd` via gRPC on port 8090 (REST on 8091).

**Module:** `github.com/nself-org/clawde/intelligence`

#### `internal/gateway` — Unified LLM gateway

Provider abstraction and lane-based routing layer. All LLM calls go through this package — never direct API calls from other packages.

| Component | Description |
| --- | --- |
| `Provider` interface | `Complete`, `Stream`, `Embed(ctx, text, expectedDim)`, `Rerank`, `HealthCheck`, `Name` |
| `AnthropicProvider` | anthropic-sdk-go v1.46.0+ adapter |
| `OpenAICompatProvider` | Covers Gemini (Vertex), Ollama (:11434), vLLM (:8093, 127.0.0.1 only), TEI-embed (:8080), TEI-rerank (:8092) |
| `Registry` | Parsed `model_registry.yaml`; maps Lane → ordered []ProviderEntry with fallback chain |
| `RegistryWatcher` | fsnotify hot-reload; atomic pointer swap under RWMutex; target <500ms |
| `LaneResolve` | Return ordered provider entries for a lane |
| `BuildProvider` | Construct a concrete Provider from a ProviderEntry |

**7 routing lanes:**

| Lane | Primary | Fallback |
| --- | --- | --- |
| `fast` | Anthropic Haiku | OpenAI gpt-4o-mini |
| `deep` | Anthropic Opus | Gemini 1.5-pro |
| `multimodal` | Anthropic Opus | Gemini Flash |
| `embedding` | TEI sidecar :8080 (BGE-M3 1024-dim) | Gemini text-embedding-004 |
| `rerank` | TEI sidecar :8092 (bge-reranker-v2-m3) | — |
| `live` | Anthropic Sonnet | — |
| `local` | vLLM :8093 (127.0.0.1) | Ollama :11434 |

**Key constraints:** model names are YAML-only (never in .go files); `api_key_ref` strings only (keys resolved from vault.env at load); `gemini` entries require `project_id` (quota is per GCP project, not per API key); vLLM must bind `127.0.0.1` per ADR-001.

#### `internal/server` — gRPC + REST server (W10-T04)

Hosts `GatewayService` over gRPC on `127.0.0.1:8090` and a plain-JSON REST mux on `127.0.0.1:8091`. Both listeners are loopback-only (never `0.0.0.0`).

**Auth: HMAC-SHA256** — every RPC except Health requires:
- `X-ClawDE-Timestamp` (Unix seconds)
- `X-ClawDE-Signature: "HMAC-SHA256 " + hex(HMAC(secret, ts + "." + hex(SHA256(body))))`
- Timestamp window: ±30 s. Secret from `CLAWDE_GATEWAY_HMAC_SECRET` — never logged.

**gRPC RPCs (service `gateway.v1.GatewayService`):**

| RPC | Type | REST | Auth |
|---|---|---|---|
| Complete | Unary | POST /v1/gateway/complete | HMAC |
| StreamComplete | Server-stream | POST /v1/gateway/stream | HMAC |
| Embed | Unary | POST /v1/gateway/embed | HMAC |
| Rerank | Unary | POST /v1/gateway/rerank | HMAC |
| Health | Unary | GET /v1/gateway/health | none |

**Proto:** `clawde/intelligence/proto/gateway.proto`. Stubs hand-written in `internal/server/gateway_grpc_gen.go` (protoc-gen-go/grpc-gateway not installed; run `make proto` once they are).

**Reflection:** enabled unless `CLAWDE_ENV=production`.

**Health handler:** polls each provider HealthCheck with a 3 s timeout in parallel; returns `{status:"ok|degraded", providers:[...]}`.

**Spec:** `clawde/.claude/docs/llm-gateway-spec.md`
**SPORT:** `nself/.claude/docs/sport/REGISTRY-SERVICES.md` → clawde-intelligence gateway + server

## Daemon Lifecycle

### Background Process Model

`clawd` runs as a persistent background daemon managed by the OS launch mechanism on each platform. It is not a server that the Flutter app starts and stops; it runs independently and the apps connect to it.

**Lifecycle states:** `stopped → starting → running → stopping → crashed → upgrading`

The startup sequence is strictly ordered (ADR-004):
1. Generate and write workspace token to `~/.claude/hooks/.clawde-token-<workspace>` (mode 0600)
2. Bind MCP socket at `/tmp/clawd.<workspace>.sock` (Unix) or `127.0.0.1:7430` (Windows fallback)
3. Write PID file at `~/.local/share/clawde/daemon.pid` using `O_CREAT|O_EXCL` (atomic, race-safe)
4. Report running state to structured log

> **Client connection note:** Tauri desktop and React Native mobile both connect to the daemon via the shared TypeScript daemon-client module (`apps/packages/`). Never use raw WebSocket from app code; always go through the typed client.

**Platform launch mechanisms:**

| Platform | Mechanism | Agent file |
|---|---|---|
| macOS | launchd (user agent) | `~/Library/LaunchAgents/io.nself.clawde.plist` |
| Linux | systemd (user unit) | `~/.config/systemd/user/clawde.service` |
| Windows | NSSM or sc.exe | Windows Service `ClawDE` |

**Restart policy:** Max 5 restarts per hour. Backoff: 5s / 10s / 30s. Ceiling triggers OS notification and halts auto-restart.

**Graceful shutdown (SIGTERM):**
1. Stop accepting new MCP connections
2. Drain in-flight tool calls (max 10 seconds; ADR-008 hook timeout of 2s is per-call, the 10s window is the aggregate drain)
3. Force SIGKILL after 10s drain window
4. Close MCP socket, flush logs, remove PID file
5. Exit 0

**Full specification:** [daemon-spec.md](daemon-spec.md)

---

## IPC protocol

All communication between apps and daemon uses **JSON-RPC 2.0 over WebSocket**.

- **17 RPC methods** — `session.create`, `session.list`, `message.list`, `tool_call.approve`, etc.
- **7 push events** — `session.created`, `message.appended`, `tool_call.pending`, etc.
- Push events flow daemon → client as `{"jsonrpc":"2.0","method":"event.name","params":{...}}`

## Data flow (sending a message)

```
User types → ChatScreen / ChatInput component
  → useDaemonClient().sendMessage(text)
    → daemonClient.call('session.sendMessage', {...})   // TypeScript daemon client
      → clawd daemon receives JSON-RPC request
        → spawns / resumes AI provider subprocess
          → streams output back
            → daemon pushes message.appended events
              → useDaemonClient listener appends to local state
                → message bubble renders new message
```

## MCP Server

`clawd` exposes a local MCP (Model Context Protocol) server that provides tool surfaces to Claude Code (CC), OpenCode (OC), and the nClaw mobile companion. This is the primary IPC boundary between host AI agents and the `clawd` Rust daemon.

**Full specification:** [mcp-server-spec.md](mcp-server-spec.md)

### Transport

Three transport modes:
- **stdio** (default) — zero port conflict; launched by host via hook script injection of `CLAWDE_MCP_SOCKET_PATH` (ADR-008).
- **Unix socket** (opt-in) — persistent connections at `/tmp/clawd.<workspace>.sock`; peer credentials validated via `SO_PEERCRED`.
- **TCP loopback `127.0.0.1:7430`** (Windows / IDE plugins fallback) — Bearer token auth.

Binding to `0.0.0.0` is explicitly forbidden and rejected at config validation (unit-tested).

### Tool Surface

Five initial tools, each with JSON input/output schema, permission hint, and SandboxGuard constraint:

| Tool | Permission | Sandbox constraint |
|---|---|---|
| `bash` | `execute` | Working dir pinned to workspace root |
| `read_file` | `read` | Path within workspace root |
| `write_file` | `write` | Destination within workspace root |
| `list_dir` | `read` | Directory within workspace root |
| `search` | `read` | Search root within workspace root |

### Dispatch Chain

Every tool call passes a 5-step chain (per ADR-003). Any step failure short-circuits with deny + audit row:

```
auth → trust_registry.lookup → PolicyEngine.evaluate (AllowAll stub P1) → SupplyChainPolicy.check → SandboxGuard.check → handler.dispatch
```

The `PolicyEngine.evaluate()` AllowAll stub ships W4–W13. The real deny-by-default implementation replaces it in **W14-T02**.

### Auth

- **CC/OC:** Unix socket `SO_PEERCRED` check (macOS/Linux) or Bearer workspace token from `~/.claude/hooks/.clawde-token-<workspace>` (Windows/TCP).
- **nClaw mobile:** QR code one-time pairing → device-scoped API key, trust level 1 (read-only tools).

### Health Endpoint

`GET /health` → `{"status","pid","uptime_s","mcp_version","active_tools","last_policy_eval_ms"}` · Latency SLO: < 5ms p99.

### Config

`~/.config/clawde/mcp-server.toml` — fields: `transport`, `socket_path`, `port`, `tool_timeout_ms` (default 2000ms per ADR-008), `policy_stub_mode` (true in P1), `token_path`.

---

## Notification System

**Spec:** `clawde/.github/wiki/notifications-spec.md` (P1-E2-W5-S05-T03)
**Status:** Planned

The ClawDE notification system surfaces daemon lifecycle, task, and security events to the user via native OS notifications and an in-app history panel.

### Delivery Architecture

```
clawd daemon
  │
  ├── Event emitted (daemon-started / task-failed / security-warning / ...)
  │
  └── NotificationDispatcher
        ├── QuietHoursFilter  ─── queues non-critical if in quiet window
        ├── DNDFilter         ─── queues non-critical if DND active
        └── OSAdapter
              ├── macOS:   UNUserNotificationCenter (12+) / NSUserNotification (<12)
              ├── Linux:   libnotify / notify-send (D-Bus)
              └── Windows: WinRT ToastNotification
```

**Privacy invariant:** no notification payload leaves the device. The `NotificationDispatcher` makes zero outbound network calls.

### Severity Levels

| Severity | Color | Persistence | Quiet Hours / DND |
|---|---|---|---|
| `info` | sky-500 `#0ea5e9` | Auto-dismiss 5s | Queued |
| `warning` | amber-500 `#f59e0b` | Persistent until acknowledged | Queued |
| `error` | red-500 `#ef4444` | Persistent, action required | Queued |
| `critical` | red-700 `#b91c1c` modal | Blocks UX until resolved | **Always delivered immediately** |

### Notification Events (Locked — 10 events)

`daemon-started` · `daemon-crashed` · `daemon-stopped` · `inbox-message-received` · `task-completed` · `task-failed` · `task-blocked` · `quota-paused` · `permission-prompt-required` · `security-warning`

### Suppression

- Per-notification snooze: 1h / 8h / 24h / forever (right-click context menu)
- Global DND: `clawde notifications pause [duration]`
- Suppressed notifications always appear in the in-app notification history panel

### History Panel

Stored in clawd SQLite (`clawd_notification_history` table). Max 500 entries. Filterable by severity, event type, and date range.

## Local Task Queue

`clawd` maintains a SQLite-backed task queue for workstation jobs dispatched from the inbox
watcher. The queue owns the full lifecycle from dispatch through completion or cancellation.

Full spec: `clawde/.github/wiki/task-queue-spec.md`
Feature: F-CLAWDE:task-queue (P1-E2-W5-S05-T02)
ADR refs: ADR-001 (SQLite storage), ADR-008 (hook contract)

### 7-State Machine (summary)

`claimed` → `active` → `done` (success path)
`active` → `retry` → `active` (transient failure, up to 3 retries)
`active` / `retry` → `cancelled` (max retries or user cancel)
`active` → `paused` → `active` (user pause/resume)
`active` / `blocked` → `cancelled` (user cancel)

Terminal states: `done`, `cancelled`. Slot release on: `done`, `cancelled`.

### Task File Location

```
.claude/tasks/queue/{task-id}.yaml
```

### Checkpoint Protocol

Every `active` task writes `.claude/tasks/checkpoints/{task-id}.json` every 5 minutes.
On daemon restart, `clawd` reads the checkpoint and resumes from `next_action`.

### Concurrency

`max_concurrent_tasks = 3` (default, configurable in `~/.config/clawde/task-queue.toml`).
Priority bands: `critical` > `high` > `medium` > `low`. FIFO within same band.

---

## Inbox Watcher

`clawd` includes a built-in inbox watcher that monitors `.claude/inbox/msg-*.md` files in
allowed project directories and routes each CRD message to the correct handler.

### Watched Paths

Projects to watch are declared in `~/.config/clawde/inbox-routes.toml`. A file event from
any path NOT in this list is a hard error (scope violation) — the watcher never dispatches
from an unknown path.

```
~/{project}/.claude/inbox/msg-*.md
```

Default polling interval (fallback): **2 seconds**.
Native events: `kqueue` (macOS), `inotify` (Linux), `ReadDirectoryChangesW` (Windows) via the
`notify` crate.

### Routing

All 10 CRD message types are routed to dedicated handlers with configured priority and
max-concurrency. Handler dispatch is asynchronous via a per-type goroutine pool.

| Type | Priority | Max concurrency |
|---|---|---|
| `bug`, `hotfix`, `deploy-request` | P1 (highest) | 1–3 |
| `test-request`, `resolution` | P2 | 2 |
| `question`, `feature`, `enhancement` | P3 | 5 |
| `idea`, `info` | P4 (lowest) | 5 |

### Debounce + Deduplication

- **500ms debounce window** before processing any new file event
- Rapid file drops sharing the same `chain_id` within the window are collapsed to one dispatch
- Partial-write safety: file must be stable for the full debounce window before reading

### Backpressure

- Max **5 in-flight messages per watched project** at a time
- Overflow queues in memory (FIFO, cap: **100 messages**)
- Queue overflow: drop oldest + `WARN` log + desktop notification

### Failure Recovery

Handler crashes are isolated to the single message:
1. Failed message moved to `.claude/inbox/failed/` with error metadata appended
2. Watcher continues — one crash does not stop processing
3. Up to 3 retries with exponential backoff; permanent failures surfaced in `clawd doctor`

### Archive

Processed messages are atomically moved to `.claude/archive/inbox/` with a processing
timestamp appended. Original content is preserved.

**Full specification:** [inbox-watcher-spec.md](inbox-watcher-spec.md)
