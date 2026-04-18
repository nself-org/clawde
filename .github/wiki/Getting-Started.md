# Getting Started

Get from download to your first AI session in under 5 minutes.

## Prerequisites

- macOS 13+, Windows 10+, or Ubuntu 22.04+
- Claude Code CLI installed and authenticated (`claude --version` should work)

---

## Install the daemon

### macOS — Homebrew (recommended)

```bash
brew tap clawde-io/clawde
brew install clawd
```

### macOS / Linux — one-liner

```bash
curl -fsSL https://clawde.io/install.sh | bash
```

Installs `clawd` to `~/.local/bin/`. Verifies SHA256 checksum before installing. Then add it to your PATH if prompted.

### Direct download

Download the binary for your platform from [GitHub Releases](https://github.com/nself-org/clawde/releases):

| Platform | Binary |
| --- | --- |
| macOS (Apple Silicon) | `clawd-aarch64-apple-darwin` |
| macOS (Intel) | `clawd-x86_64-apple-darwin` |
| Linux x86_64 | `clawd-x86_64-unknown-linux-gnu` |
| Windows x86_64 | `clawd-x86_64-pc-windows-msvc.exe` |

After downloading, make it executable and move to your PATH:

```bash
chmod +x clawd-aarch64-apple-darwin
mv clawd-aarch64-apple-darwin /usr/local/bin/clawd
```

**macOS Gatekeeper:** the first time, run `xattr -d com.apple.quarantine /usr/local/bin/clawd` to remove the quarantine flag.

---

## Step 2: Start the daemon

```bash
clawd start
```

The daemon runs on `localhost:4300` and persists in the background.

---

## Step 3: Verify everything is working

```bash
clawd doctor
```

Expected output:

```text
  ✓ Port 4300 available       port 4300 is free
  ✓ claude CLI installed      claude 1.x.x
  ✓ claude CLI authenticated  logged in
  ✓ SQLite DB accessible      ~/.local/share/clawd/clawd.db
  ✓ Disk space                45GB free
  ✓ Relay reachable           api.clawde.io reachable

All checks passed.
```

---

## Step 4: Download the client app

Download the desktop or mobile app from [clawde.io/#download](https://clawde.io/#download) and connect it to your daemon.

---

## Step 5: Connect remote devices (optional)

To use ClawDE from your phone or another computer:

1. Open **Settings → Remote Access → Add Device** on the host machine
2. Scan the QR code or enter the 6-digit PIN on your other device
3. For off-network access (not just your home/office LAN), subscribe to [Personal Remote](https://clawde.io/#pricing) ($9.99/yr)

---

## Next Steps

- [[Features/Projects]] — organize your repos into projects
- [[Features/Remote-Access]] — connect from your phone or another machine
- [[Configuration]] — customize the daemon with config.toml
- [[Daemon-Reference]] — JSON-RPC 2.0 API for building clients
- [[Troubleshooting]] — if something isn't working
