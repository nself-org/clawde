# Roadmap

## v0.1.0 — Shipped (2026-02-23)

ClawDE's initial public release:

- **Rust daemon** (`clawd`) — always-on background service with WebSocket IPC, SQLite state, session management
- **Desktop app** — Flutter app for macOS, Windows, and Linux with chat-first interface
- **Mobile app** — Flutter app for iOS and Android
- **LAN discovery** — automatic device detection via mDNS/DNS-SD
- **Relay** — remote access via `api.clawde.io` (Personal Remote tier)
- **Multi-provider** — Claude Code and Codex support; Cursor placeholder
- **Multi-account** — automatic account rotation on rate limits
- **Git integration** — repo awareness, worktree isolation per session
- **Task system** — structured task tracking with agent orchestration
- **MCP support** — Model Context Protocol for tool integration
- **E2E encryption** — X25519 + ChaCha20-Poly1305 for relay traffic

## Upcoming

These areas are in development. No specific dates.

**Resource management** — daemon resource governor for memory-conscious multi-session workflows; tiered session states (Active/Warm/Cold) for RAM efficiency

**Task engine** — persistent task engine with atomic claiming, event sourcing, and checkpoint/handoff for multi-agent coordination

**Pack marketplace** — community packs for rules, agents, skills, and workflow templates

**Session intelligence** — proactive context management, automatic session splitting, cross-session context bridging

**AI code review** — CodeRabbit-parity automated code review integrated into the daemon workflow

**Web app** — browser-based daemon client for ClawDE Cloud tier users

## Internationalization

ClawDE is designed for international use. The architecture uses ARB files (Flutter) and constants-based strings (React) that are localization-ready. Community translations are welcome.

## How to contribute

See [Contributing](Contributing.md) for development setup, code style, and PR process.

Feature requests and bug reports go to [GitHub Issues](https://github.com/nself-org/clawde/issues).
