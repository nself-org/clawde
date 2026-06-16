# ClawDE

The host-first AI development environment.

One daemon. Every provider. Any device.

[![Version](https://img.shields.io/badge/version-1.1.1-blue.svg)](https://github.com/nself-org/clawde/releases)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Build](https://github.com/nself-org/clawde/actions/workflows/test.yml/badge.svg)](https://github.com/nself-org/clawde/actions/workflows/test.yml)
<!-- VERSION_BADGE -->

## Description

**ClawDE** is a host-first AI developer environment. One always-on local daemon (`clawd`) owns the filesystem, sessions, validators, and orchestration. Flutter apps on desktop and mobile are thin JSON-RPC 2.0 clients of that daemon, so every device sees the same state.

The daemon is the source of truth, not any UI. That means no cold starts, no drift between sessions, and no agent inventing files that aren't on disk. Works with Claude Code, Codex, Cursor, and Aider through one interface.

## Documentation

See the [Wiki](https://github.com/nself-org/clawde/wiki) for full documentation:

- [Getting Started](https://github.com/nself-org/clawde/wiki/Getting-Started)
- [Architecture](https://github.com/nself-org/clawde/wiki/Architecture)
- [Features](https://github.com/nself-org/clawde/wiki/Features)
- [Contributing](https://github.com/nself-org/clawde/wiki/Contributing)
- [Changelog](https://github.com/nself-org/clawde/wiki/Changelog)

## Quick Start

```bash
# Clone the repo
git clone https://github.com/nself-org/clawde.git
cd clawde

# Build and run the daemon (Rust)
cd apps/daemon && cargo run

# Run the desktop app (Tauri 2 + React)
cd ../desktop && pnpm install && pnpm tauri dev

# Run the mobile app (React Native + Expo)
cd ../mobile && pnpm install && pnpm start
```

See [Getting Started](https://github.com/nself-org/clawde/wiki/Getting-Started) for full setup instructions.

## Structure

```text
apps/                        # All application code
  daemon/                    # clawd — Rust/Tokio daemon (port 4300)
  desktop/                   # Tauri 2 + React + Vite (macOS/Windows/Linux)
  mobile/                    # React Native + Expo (iOS/Android)
  packages/clawd_plugin_abi/ # Rust crate: plugin ABI
  packages-flutter-archive/  # Retired Flutter/Dart packages (archive only)
site/                        # Website (clawde.io)
.github/                     # CI/CD workflows, wiki source, brand assets
```

## Features

- Host-first daemon (`clawd`) owns sessions, filesystem state, and orchestration
- Works with Claude Code, Codex, Cursor, and Aider through one interface
- Desktop apps for macOS, Windows, and Linux
- Mobile companion for iOS and Android (review and approve agent runs from anywhere)
- Multi-account switching when one provider hits a rate limit
- Task worktrees: each AI task gets its own git branch you can accept or reject
- LAN discovery via mDNS plus an outbound mTLS relay for off-LAN access
- End-to-end encryption for remote sessions
- SQLite-backed local state (no external DB)
- Free for local use, open source under MIT

## Installation

### macOS

```bash
brew install nself-org/clawde/clawde
```

### macOS / Linux (one-liner)

```bash
curl -fsSL https://clawde.io/install.sh | bash
```

### Windows

Download the `clawd-x86_64-pc-windows-msvc.exe` binary from the [Releases page](https://github.com/nself-org/clawde/releases) and place it on your PATH. The Flutter desktop app installs separately via the platform-specific build (see [Wiki / Getting-Started](https://github.com/nself-org/clawde/wiki/Getting-Started)).

### Build from source

```bash
git clone https://github.com/nself-org/clawde.git

# Daemon (Rust)
cd clawde/apps/daemon && cargo build --release

# Desktop (Tauri 2 + React)
cd ../desktop && pnpm install && pnpm tauri build

# Mobile (React Native + Expo)
cd ../mobile && pnpm install && pnpm ios   # or pnpm android
```

## Usage

```bash
# Start the daemon (listens on ws://localhost:4300)
clawd start
```

The desktop and mobile apps connect to that WebSocket and stay in sync.

```bash
# Pair a mobile device
clawd pair --show-qr
```

Scan the QR with the mobile app to trust the device.

```bash
# Run a session against Claude Code
clawd session new --provider claude-code
```

The daemon spawns the provider CLI and brokers messages over JSON-RPC.

## Architecture

ClawDE is built around a single Rust daemon (`clawd`) that holds session state in embedded SQLite, brokers JSON-RPC 2.0 over WebSocket, and supervises provider CLIs (Claude Code, Codex, Cursor, Aider). The desktop app (Tauri 2 + React) and mobile app (React Native + Expo) are thin TypeScript clients of that daemon and never talk to providers directly. ClawDE Cloud is a proprietary fork hosted by us; the open-source code in this repo covers the self-hosted mode entirely.

See the [Architecture wiki page](https://github.com/nself-org/clawde/wiki/Architecture) for the deep-dive.

## Contributing

See [Contributing](https://github.com/nself-org/clawde/wiki/Contributing) for the contributor guide.

## License

MIT. See [LICENSE](LICENSE).

## Related Repos

- [nself-org/cli](https://github.com/nself-org/cli): the nSelf CLI. ClawDE+ sync features run on top of an nSelf backend.
- [nself-org/plugins-pro](https://github.com/nself-org/plugins-pro): license-gated pro plugins. ClawDE+ Bundle pulls cloud sync / mobile twin features from here.
- [nself-org/web](https://github.com/nself-org/web): `clawde.io` marketing site, `api.clawde.io` relay, and `cloud.clawde.io` Cloud product surface.
