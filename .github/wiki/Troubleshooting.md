# Troubleshooting

Solutions to common problems with ClawDE.

## Daemon won't start

**Check daemon status:**
```bash
clawd status
```

**Check for port conflicts** — ClawDE uses port 4300 by default:
```bash
lsof -i :4300   # macOS/Linux
netstat -ano | findstr :4300  # Windows
```

**Check logs:**
- macOS: `~/Library/Logs/clawd/clawd.log`
- Linux: `~/.local/share/clawd/clawd.log`
- Windows: `%APPDATA%\clawd\clawd.log`

**macOS Gatekeeper** — if you downloaded the binary directly (not via Homebrew), macOS may block it:
```bash
xattr -d com.apple.quarantine /usr/local/bin/clawd
```

## Desktop app can't connect to daemon

1. Verify the daemon is running: `clawd status`
2. Open app Settings → Connection and check the daemon URL (default: `ws://localhost:4300`)
3. Check your firewall isn't blocking port 4300
4. Try regenerating the auth token in Settings → Connection → Regenerate Token

## Session gets stuck / no response

**Check if the provider CLI is installed and authenticated:**
```bash
claude --version   # Claude Code
codex --version    # Codex
```

**Check session status:**
```bash
clawd sessions list
```

If a session shows status `busy`, it may have stalled. Cancel it from the app or:
```bash
clawd sessions cancel <session-id>
```

## Relay connection fails

1. Check your subscription tier — relay requires Personal Remote ($9.99/year) or Cloud tier
2. Verify `api.clawde.io` is reachable: `curl https://api.clawde.io/health`
3. Check your device is registered: Settings → Account → Devices
4. Try disconnecting and reconnecting in Settings → Connection

## High memory usage

ClawDE's resource governor manages memory automatically, but you can tune it.

Open `clawd.toml` (macOS: `~/Library/Application Support/clawd/clawd.toml`) and set:
```toml
[resources]
max_memory_percent = 60    # Lower this to free up RAM (default: 70)
idle_to_warm_secs = 60     # Freeze idle sessions sooner (default: 120)
```

See [Configuration](Configuration.md) for all resource settings.

## Database is corrupted

ClawDE stores sessions in SQLite at:
- macOS: `~/Library/Application Support/clawd/clawd.db`
- Linux: `~/.local/share/clawd/clawd.db`
- Windows: `%APPDATA%\clawd\clawd.db`

If the database is corrupted, stop the daemon, rename the file (as a backup), and restart — the daemon creates a fresh database on startup. Your sessions will be lost but the daemon will function normally.

## Getting help

- [GitHub Issues](https://github.com/nself-org/clawde/issues) — bug reports and feature requests
- Check the [FAQ](FAQ.md) for common questions
- Log files include timestamps and error details that help diagnose issues
