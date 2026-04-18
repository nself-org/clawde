# Changelog

All notable changes to ClawDE are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/).

---

## [Unreleased] — v0.2.0

### Added

#### Daemon (`clawd`)

##### Multi-account switching

- `account.list`, `account.create`, `account.delete`, `account.setPriority`, `account.history` RPCs
- Automatic account rotation when an active session hits a rate limit (`-32003`)
- Free tier: pause + prompt before switching. Personal Remote ($9.99/yr): silent automatic switch
- Account event log (`account_events` table, migration `010`) tracks limit signals, priority changes, and switches
- `routed_provider` column on sessions (migration `011`) records which provider was selected when `auto` routing is used
- Push events: `session.accountLimited`, `session.accountSwitched`

##### Task engine

- Full task lifecycle RPCs: `tasks.create`, `tasks.get`, `tasks.list`, `tasks.claim`, `tasks.update`, `tasks.complete`, `tasks.approve`, `tasks.reject`, `tasks.approve_spec`, `tasks.assign`, `tasks.release`, `tasks.interrupt`, `tasks.resume`, `tasks.unblock`, `tasks.prioritize`, `tasks.search`, `tasks.stats`, `tasks.progressEstimate`, `tasks.activityLog`, `tasks.testResult`
- SQLite task engine tables: `te_agents`, `te_phases`, `te_tasks`, `te_task_dependencies`, `te_events`, `te_notes`, `te_checkpoints` (migration `006`)
- Push events: `task.created`, `task.stateChanged`, `task.statusChanged`, `task.claimed`, `task.released`, `task.interrupted`, `task.resumed`, `task.approvalDenied`, `task.approvalGranted`, `task.specCreated`, `task.activityLogged`, `task.testResult`

##### Agent registry

- `agents.register`, `agents.list`, `agents.heartbeat`, `agents.unregister` RPCs
- Push events: `agent.spawned`, `agent.connected`, `agent.disconnected`, `agent.canceled`

##### Git worktrees

- `worktrees.create`, `worktrees.list`, `worktrees.diff`, `worktrees.commit`, `worktrees.accept`, `worktrees.reject`, `worktrees.delete`, `worktrees.merge`, `worktrees.cleanup` RPCs
- Task-scoped isolated git worktrees on branch `claw/<task_id>-<slug>`; AI agent works in worktree, never touches main workspace
- `worktree_mode = true` in config auto-creates worktree on task claim
- Blocking rule: `task.complete` returns `worktreeNotMerged` if active worktree exists
- `worktrees` SQLite table (migration `014`)
- Push events: `worktree.created`, `worktree.accepted`, `worktree.rejected`

##### Model Intelligence

- Heuristic complexity classifier (no LLM call, under 1ms): maps messages to Simple / Moderate / Complex / DeepReasoning
- Automatic model selection: Haiku for Simple, Sonnet for Moderate/Complex, Opus for DeepReasoning
- Auto-upgrade: retries once with next tier if response is empty, refused, or truncated
- Token usage tracking per AI response: `token_usage` SQLite table (migration `012`) stores input tokens, output tokens, and estimated cost
- `token.getUsage`, `token.listUsage`, `token.monthlySummary` RPCs
- Budget caps: `monthly_budget_usd` in config; warning at 80%, forced downgrade to Haiku at 100%
- Push event: `session.accountLimited` when budget cap is reached
- Session pin (`session.setProvider`) bypasses auto-select for the pinned model

##### Session GCI mode tracking

- `session.setMode` RPC — set mode to `NORMAL`, `LEARN`, `STORM`, `FORGE`, or `CRUNCH`
- `mode` column on sessions table (migration `013`), persists across daemon restarts
- Mode-specific context injection on each message dispatch
- Push event: `session.modeChanged`

##### Drift detection

- `drift.scan` and `drift.list` RPCs — detect features in FEATURES.md marked done with no matching implementation
- 24-hour background scanner runs automatically; emits `session.driftDetected` when new items appear
- `drift_items` SQLite table (migration `015`)

##### Tool call audit

- Append-only `tool_call_events` table (migration `016`) records every tool call attempt
- Not cascaded on session delete — audit log survives session removal
- `session.toolCallAudit` RPC for querying the audit trail

##### Provider knowledge injection

- `providers.detect` — scans `$PATH` and common install locations for Claude Code, Codex, Cursor, and Aider CLIs; returns version and auth status
- `providers.list` — returns all known providers with capability profiles
- Auto-injects provider profile into session context on `session.create`

##### Coding standards injection

- `standards.list` RPC — returns all available language/framework standards entries
- Auto-detects repo language from `Cargo.toml`, `tsconfig.json`, `pubspec.yaml`, etc.
- Auto-injects matching standards block into session context on `session.create`
- Pass `inject_standards: false` to `session.create` to opt out

##### License system

- `license.get`, `license.check`, `license.tier` RPCs
- `license_cache` SQLite table (migration `002`) caches the last successful license verification
- Tier values: `free`, `personal`, `cloud`

##### Auto-update

- `daemon.checkUpdate` — check GitHub Releases for a newer binary
- `daemon.applyUpdate` — download and swap binary on next idle period
- `daemon.updatePolicy` / `daemon.setUpdatePolicy` — get/set update channel (`stable` or `preview`)
- Push events: `daemon.updateAvailable`, `daemon.updating`, `daemon.updateFailed`

##### Repo enhancements

- `repo.list` — list all open repos
- `repo.tree` — directory tree for a repo path
- `repo.readFile` — read a file by path (path-traversal sanitized at handler level)
- `repo.close` — close a watched repo and stop its file watcher

##### Session enhancements

- `session.cancel` — cancel an active session mid-run
- `session.setProvider` — pin a specific model for the session (bypasses auto-select)

##### Doctor

- `doctor.scan` — scan a project for health issues (AFS compliance, docs completeness, release readiness)
- `doctor.fix` — auto-fix a subset of detected issues
- `doctor.approveRelease` — stamp a project as release-approved
- `doctor.hookInstall` — install Claude Code hooks into a project

##### AFS (Artifact File System)

- `afs.init` — initialize AFS structure in a project
- `afs.status` — check AFS compliance
- `afs.syncInstructions` — sync instruction files to connected clients
- `afs.register` — register a file as an AFS artifact
- Push events: `afs.activeMdSynced`, `afs.planningUpdated`, `afs.qaItemChecked`

##### Conversation threading

- `threads`, `thread_turns`, `thread_session_snapshots` tables (migration `007`)
- Persistent control threads (one per project) and task-scoped threads
- Vendor session snapshot storage for session resume

##### Project push events

- `project.created`, `project.updated`, `project.deleted`, `project.repoAdded`, `project.repoRemoved`

##### Device push events

- `device.paired`, `device.revoked`

##### Inbox

- `inbox.messageReceived` push event

##### Session Intelligence (Sprint G)

- `message.pin` / `message.unpin` — pin messages to always stay in context across compression and session bridging
- `session.contextStatus` — context window utilisation for a session: usedTokens, maxTokens, percent, status (ok/warning/critical)
- `session.health` — health score 0–100 derived from short responses, tool errors, truncations, and consecutive low-quality turns; `needsRefresh` flag when score < 40
- `session.splitProposed` — classify a prompt as Simple/Moderate/Complex/DeepReasoning and return a proposed task breakdown for complex prompts
- `context.bridge` — build a compact context snapshot (system prompt + pinned messages + last user/assistant messages + repo path) suitable for injecting into a fresh session
- `CursorRunner` — full Cursor provider runner: spawns `cursor --headless`, streams output, supports pause/resume/stop; detects auth token from `CURSOR_TOKEN` env or `~/.cursor/auth.json`
- Migration `019` adds `token_count` + `pinned` columns to `messages` and a `session_health` table
- Context compression: `compress_messages()` retains system and pinned messages, drops oldest regular messages, inserts a sentinel note
- Auto-continuation detection: `detect_stop_reason()` distinguishes Truncated/SelfInterrupted (should continue) from Complete/ContextFull/RateLimited
- Desktop: `HealthChip` in session header — shows health score when below 80, color-coded by severity; taps open a detail sheet with all counters
- Desktop: `SplitProposalDialog` shown when a sent prompt is classified as Complex or DeepReasoning — presents the proposed subtask breakdown

##### Repo Intelligence (Sprint F)

- `repo.scan` — detect primary language, frameworks, build tools, monorepo structure, and code conventions via filesystem heuristics; stores `RepoProfile` to SQLite (`repo_profiles` table, migration `018`)
- `repo.profile` — return stored `RepoProfile`; triggers background scan if no profile exists
- `repo.generateArtifacts` — generate `.claude/CLAUDE.md`, `.codex/AGENTS.md`, and `.cursor/rules` from the profile; respects `overwrite: bool`; returns per-artifact `action` (created/updated/skipped) + unified diff
- `repo.syncArtifacts` — propagate CLAUDE.md content to AGENTS.md and cursor rules when those files are missing
- `repo.driftScore` — return 0–100 artifact sync score (missing or stale >30 days deducts points)
- `repo.driftReport` — return itemized drift items with severity (info/warning/error)
- `validators.list` — return auto-derived validator commands for the repo's primary language
- `validators.run` — run a single validator subprocess with a 5-minute timeout; records result to `validator_runs` table
- Convention injection hook: `convention_injection()` injects detected conventions into the session system prompt on `session.create`
- Desktop app: `RepoIntelligencePanel` added to the Files tab left pane — shows stack chips, confidence, drift score bar, Scan + Generate AI configs buttons

#### Testing

- 384+ total Rust tests (unit + integration) passing; Sprint G adds tests for context_guard, health scoring, complexity classifier, continuation detection, and CursorRunner token detection
- Tests cover: context window guard (8 tests), session health scoring (7 tests), complexity classification (8 tests), auto-continuation detection (8 tests), bridge injection text (4 tests), CursorRunner token detection (2 tests); repo scanner, artifact generation, drift scoring, validator derivation (Sprint F coverage retained)
- CI continues to pass: cargo clippy + test, Dart analyze + test, flutter test on push/PR

---

## [0.1.0] — 2026-02-23

First public release. Binaries available for macOS (Apple Silicon + Intel), Linux x86\_64, and Windows x86\_64.

### Added

#### Daemon (`clawd`)

- JSON-RPC 2.0 over WebSocket server on `localhost:4300`
- Session management: create, list, get, delete, pause, resume, cancel
- Message streaming from Claude Code subprocess (`claude` CLI)
- Tool call lifecycle: pending → approve/reject → done
- Repo integration: open/close repos, watch file changes, git status/diff
- Project model: group repos into named projects (RPCs: `project.*`)
- Device pairing: QR code + PIN flow for remote mobile access (RPCs: `device.*`)
- HTTP health endpoint: `GET http://127.0.0.1:4300/health`
- Auth token stored at platform-standard path (mode 0600)
- `clawd start` / `clawd stop` / `clawd status` / `clawd token show` / `clawd token qr`
- SQLite WAL-mode database with versioned migrations
- mDNS LAN discovery (advertises `_clawd._tcp` service with port in TXT record)
- Configurable bind address (`--bind` flag, `CLAWD_BIND` env var, `config.toml`)
- Resource governor: RAM pressure monitoring, session eviction
- Structured TOML config (`config.toml` in platform data dir)
- SPDX license compliance (MIT headers, `NOTICE` file)

#### Dart packages

- `clawd_proto`: all protocol types (Session, Message, ToolCall, push events)
- `clawd_client`: typed WebSocket/JSON-RPC client with reconnection backoff
- `clawd_core`: Riverpod providers (daemon connection, session list, message list, tool calls)
- `clawd_ui`: shared Flutter widgets (ChatBubble, ToolCallCard, MessageInput, ConnectionBanner, ProviderBadge)

#### Flutter apps

Desktop app (macOS / Windows / Linux) and mobile app (iOS / Android) with session list,
chat view, tool approval flow, and settings screen. Platform runners and full UI polish
ship in v0.2.0.

#### Distribution

- `curl -fsSL https://clawde.io/install.sh | bash` — one-line installer (macOS + Linux)
- Homebrew tap: `brew tap clawde-io/clawde && brew install clawd`
- GitHub Releases with SHA256 checksums for all 4 platform binaries

#### Testing

- 264 Rust tests (unit + integration: session recovery, health endpoint)
- 153 Flutter tests (clawd\_core, desktop widget, mobile widget, proto/client/ui)
- CI: GitHub Actions on push/PR (cargo clippy, rustfmt, Dart analyze, dart test, flutter test)

---

[Unreleased]: <https://github.com/nself-org/clawde/compare/v0.1.0...HEAD>
[0.1.0]: <https://github.com/nself-org/clawde/releases/tag/v0.1.0>
