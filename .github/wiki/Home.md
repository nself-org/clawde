# ClawDE

**Your IDE. Your Rules.**

ClawDE is an AI-first developer environment that runs on your machine. One always-on local daemon (`clawd`) manages your AI sessions, tracks your code, and keeps every agent in sync. Flutter apps on desktop and mobile connect to it over a local WebSocket.

## Why ClawDE?

| Problem | ClawDE's answer |
| --- | --- |
| **Drift** — AI agents forget context between sessions | `clawd` persists every session, message, and repo state in SQLite |
| **Gaps** — switching tools resets your AI's understanding | Continuous daemon means no cold starts |
| **Hallucinations** — agents invent things that aren't there | Daemon validates against the real filesystem and git history |

## Key features

- Works with **Claude Code, Codex, Cursor, and Aider** — one interface for all
- **Desktop app** for macOS, Windows, and Linux
- **Mobile companion** for iOS and Android — monitor and reply from anywhere
- **Multi-account switching** — hit a rate limit, daemon rotates to the next account automatically
- **Task worktrees** — each AI task gets its own git branch; accept or reject changes before they land
- **GCI mode system** — session modes (LEARN, STORM, FORGE, CRUNCH) injected automatically into context
- **Provider detection** — daemon knows which AI CLIs are installed and auto-routes sessions
- **Coding standards injection** — language-specific style guides added to context on session open
- **Free forever** for local use — no subscription required to run on your own machine
- **Open source** — Rust daemon + Flutter apps, MIT licensed

## Get started

**macOS / Linux — one-liner:**

```sh
curl -fsSL https://clawde.io/install.sh | bash
```

**macOS — Homebrew:**

```sh
brew tap clawde-io/clawde
brew install clawd
```

**Direct downloads — v0.1.0:**

| Platform | Binary |
| --- | --- |
| macOS (Apple Silicon) | [clawd-aarch64-apple-darwin](https://github.com/nself-org/clawde/releases/download/v0.1.0/clawd-aarch64-apple-darwin) |
| macOS (Intel) | [clawd-x86_64-apple-darwin](https://github.com/nself-org/clawde/releases/download/v0.1.0/clawd-x86_64-apple-darwin) |
| Linux x86_64 | [clawd-x86_64-unknown-linux-gnu](https://github.com/nself-org/clawde/releases/download/v0.1.0/clawd-x86_64-unknown-linux-gnu) |
| Windows x86_64 | [clawd-x86_64-pc-windows-msvc.exe](https://github.com/nself-org/clawde/releases/download/v0.1.0/clawd-x86_64-pc-windows-msvc.exe) |

All releases: [github.com/nself-org/clawde/releases](https://github.com/nself-org/clawde/releases)

## Quick links

- [[Getting-Started]] — install and run in under 5 minutes
- [[Architecture]] — how the daemon, apps, and packages fit together
- [[Features]] — full feature list with status
- [[Contributing]] — how to contribute code
- [[Changelog]] — version history
- [[Contributors]] — project contributors
- [[FAQ]] — common questions
- [[Branding]] — brand guide and asset downloads

## Distribution

| Mode | Who hosts | Price |
| --- | --- | --- |
| **Self-hosted** | You, on your machine | Free or $9.99/year (remote access) |
| **ClawDE Cloud** | Us, on Hetzner | $20–$200/month |

The open-source code in this repo covers the **self-hosted** mode entirely.
