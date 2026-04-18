# Packages & Distribution

ClawDE daemon (`clawd`) is distributed as pre-compiled binaries for macOS, Linux, and Windows. Pick the method that fits your workflow.

---

## Install via Script (Recommended)

The install script auto-detects your platform, downloads the latest binary, verifies the SHA256 checksum, and places it in `~/.local/bin`.

```bash
curl -sSL https://clawde.io/install | bash
```

To install a specific version:

```bash
curl -sSL https://clawde.io/install | bash -s -- --version v0.2.0
```

To install to a custom directory:

```bash
curl -sSL https://clawde.io/install | bash -s -- --dir /usr/local/bin
```

After install:

```bash
clawd --version          # verify
clawd service install    # start as background service
clawd doctor             # run diagnostics
```

---

## Homebrew (macOS and Linux)

Requires [Homebrew](https://brew.sh).

```bash
brew tap clawde-io/clawde
brew install clawd
```

Start as a background service:

```bash
brew services start clawd
```

Update:

```bash
brew upgrade clawd
```

---

## Debian / Ubuntu (.deb)

```bash
# Replace VERSION with the release tag, e.g. 0.2.0
VERSION=0.2.0
curl -fLo clawd.deb \
  "https://github.com/nself-org/clawde/releases/download/v${VERSION}/clawd_${VERSION}_amd64.deb"
sudo dpkg -i clawd.deb
```

The `.deb` package includes a systemd user unit (`clawd.service`). After install:

```bash
systemctl --user enable --now clawd
```

---

## Windows

1. Download `clawd-x86_64-pc-windows-msvc.exe` from [GitHub Releases](https://github.com/nself-org/clawde/releases/latest).
2. Rename the file to `clawd.exe`.
3. Move it to a directory in your `PATH` (e.g. `C:\Users\you\AppData\Local\Programs\clawd\`).
4. Add that directory to `PATH` in System Settings if it is not already there.

Install as a Windows service:

```powershell
clawd service install
```

---

## Manual Download

Download any binary directly from [GitHub Releases](https://github.com/nself-org/clawde/releases/latest).

| Platform | Binary |
|----------|--------|
| macOS (Apple Silicon) | `clawd-aarch64-apple-darwin` |
| macOS (Intel) | `clawd-x86_64-apple-darwin` |
| Linux (x86_64) | `clawd-x86_64-unknown-linux-gnu` |
| Windows (x86_64) | `clawd-x86_64-pc-windows-msvc.exe` |

Each binary has a matching `.sha256` file. Verify before running:

```bash
# Linux / macOS
sha256sum -c clawd-aarch64-apple-darwin.sha256
```

---

## Verify Installation

```bash
clawd --version
clawd doctor
```

`clawd doctor` runs 8 checks: daemon reachability, port binding, SQLite integrity, auth token, provider connectivity, disk space, file permissions, and keychain access.

---

## Uninstall

```bash
clawd service uninstall    # stop and remove background service
rm ~/.local/bin/clawd      # remove binary (adjust path if installed elsewhere)
```

For Homebrew:

```bash
brew services stop clawd
brew uninstall clawd
```

---

## Next Steps

- [Getting Started](Getting-Started) — first-time setup and configuration
- [Configuration](Configuration) — config file reference
- [Providers](Providers) — connect Claude, Codex, or Cursor
- [Troubleshooting](Troubleshooting) — common issues
