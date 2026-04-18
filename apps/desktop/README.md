# ClawDE Desktop

Host-first AI development environment for macOS, Windows, and Linux.

## Description

This is the Flutter desktop client for ClawDE. It connects to the local `clawd` Rust daemon over JSON-RPC 2.0 / WebSocket and provides the desktop UI for sessions, repos, agent runs, and worktrees. The daemon owns all state; this app is a presentation layer.

For the full ClawDE README and architecture overview, see [../../README.md](../../README.md). For ecosystem context (nSelf product family, ClawDE+ Bundle, licensing), see the PRI at `~/Sites/nself/clawde/.claude/CLAUDE.md`.

## Prerequisites

- Flutter 3.x (`flutter doctor` should be clean)
- Dart 3.x
- A running `clawd` daemon (build it from `apps/daemon/`)
- Platform toolchain:
  - macOS: Xcode + CocoaPods
  - Windows: Visual Studio with Desktop C++ workload
  - Linux: standard Flutter desktop deps (`clang`, `cmake`, `ninja-build`, `pkg-config`, `libgtk-3-dev`)

## Run (dev)

```bash
# From the repo root
cd apps && melos bootstrap        # one-time, fetches Dart packages

# Desktop app, dev mode
cd apps/desktop
flutter run -d macos              # or: -d windows, -d linux
```

The app will try to connect to `ws://localhost:4300`. Start the daemon first:

```bash
cd ../daemon
cargo run                          # dev mode
# or: cargo build --release && ./target/release/clawd start
```

## Build (release)

```bash
cd apps/desktop

# macOS
flutter build macos
# Output: build/macos/Build/Products/Release/clawde.app

# Windows
flutter build windows
# Output: build\windows\x64\runner\Release\clawde.exe

# Linux
flutter build linux
# Output: build/linux/x64/release/bundle/clawde
```

## Test

```bash
cd apps/desktop
flutter test
```

For the daemon's tests:

```bash
cd ../daemon
cargo test
```

## Daemon connection

The desktop app talks to `clawd` exclusively through the `clawd_client` Dart package (`apps/packages/clawd_client/`). Never open raw WebSockets in app code. If the daemon is not running on `ws://localhost:4300`, the app will show a connection-status banner and retry until the daemon starts.

For pairing remote daemons (Personal Remote tier or ClawDE Cloud), see the [Connectivity wiki page](https://github.com/nself-org/clawde/wiki/Features/Connectivity).

## License

MIT. See [../../LICENSE](../../LICENSE).
