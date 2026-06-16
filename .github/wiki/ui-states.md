# UI States — 7-State AsyncScreen Contract

All data-fetching panels in the ClawDE desktop app use the `AsyncScreen` component from `@nself/ui`. Every panel supports exactly 7 states. None can be silently omitted.

## The 7 States

| # | State | Trigger | UI |
|---|-------|---------|-----|
| 1 | **loading** | Data fetch in flight | Skeleton matching panel layout |
| 2 | **populated** | `Ok(data)`, non-empty | Normal content |
| 3 | **empty** | `Ok([])` + `emptyCheck` true | Zero-state CTA |
| 4 | **error** | `Err(internal)` | Error card + retry button |
| 5 | **offline** | `useDaemonStatus().isConnected === false` | "ClawDE daemon offline — run `nself start`" + Reconnect button |
| 6 | **permission-denied** | `useDaemonStatus().licensed === false` | "ClawDE bundle required" + upgrade CTA → cloud.nself.org |
| 7 | **rate-limited** | `Err(rate_limited)` | "Rate limit reached" + countdown |

## Daemon Offline Detection

`useDaemonStatus` polls `GET http://127.0.0.1:7432/health` every 5 seconds.

- Two consecutive failures → `isConnected = false` (prevents flapping on single timeout)
- On success → `isConnected = true`, failure counter resets
- `/health` response with `{ licensed: false }` → `licensed = false`

The offline state in ClawDE means **daemon disconnected**, not network offline. The reconnect button calls `retry()` which fires an immediate health check.

## Permission-Denied Semantics

Permission-denied distinguishes free vs paid features:

- **Desktop app features are always free** (all sessions, chat, files, git, packs, settings, dashboard)
- **Bundle features** (mobile, multi-account OAuth pool, server mode, team) require the ClawDE bundle ($0.99/mo)
- The upgrade CTA links to `https://cloud.nself.org`

Never show permission-denied for features that are free on desktop.

## Panel Coverage (8 panels)

| Panel | Component | Empty CTA |
|-------|-----------|-----------|
| Session List (sidebar) | `ChatScreen → SessionSidebar` | "Create your first project" |
| Agent Chat | `ChatScreen → AgentChatArea` | "Start a conversation with your AI pair programmer" |
| File Tree | `FilesScreen` | "Open a folder or create a new project" |
| Sessions | `SessionsScreen` | "Start a conversation" |
| Dashboard / Metrics | `DashboardScreen` | N/A (emptyCheck always false) |
| Packs / Plugin Status | `PacksScreen` | "Browse packs" |
| Settings | `SettingsScreen` | N/A (localStorage always has defaults) |

## Skeleton Layouts

Each panel has a skeleton that matches the populated layout:

| Panel | Skeleton description |
|-------|---------------------|
| Session list | 4 rows with title + meta placeholders |
| Agent chat | 3 message bubble rows alternating left/right |
| File tree | 6 rows at varying indent depths |
| Sessions | 4 session rows with title + status placeholders |
| Dashboard | 3-column stat grid + 3 memory entry rows |
| Packs | 4 pack rows with icon + name + badge placeholders |
| Settings | 2-row preference toggle block |

## Implementation

```
src/
  hooks/
    useDaemonStatus.ts     # polls /health; exports isConnected, licensed, retry
    useAsyncResult.ts      # wraps any Promise → Result<T,AppError>|'loading'
  components/
    ChatScreen.tsx         # AsyncScreen on session sidebar + agent chat
    SessionsScreen.tsx     # AsyncScreen on session list
    FilesScreen.tsx        # AsyncScreen on file tree
    DashboardScreen.tsx    # AsyncScreen on metrics
    PacksScreen.tsx        # AsyncScreen on plugin/pack list
    SettingsScreen.tsx     # AsyncScreen on settings form
```

## Testing

`src/__tests__/async-screen-states.test.tsx` — 8 panels × 7 states verified by mocking `useDaemonStatus` and `useAsyncResult`. All state transitions are testable without a real daemon or Tauri bridge.

Run: `pnpm test` from `clawde/apps/desktop/`.
