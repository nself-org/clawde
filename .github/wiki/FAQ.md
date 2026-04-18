# FAQ

**Do I need a ClawDE account to use the desktop app?**
No. The Free tier runs entirely on your machine with no account required. You only need an account for remote access ($9.99/year) or ClawDE Cloud.

**Does ClawDE replace Claude Code / Codex?**
No — it wraps them. ClawDE calls `claude`, `codex`, or `cursor` as subprocesses using their standard CLIs. You still need your own AI subscriptions.

**What AI providers are supported?**
Tier 1 (subprocess, full support): Claude Code, Codex. Tier 2 (coming soon): Cursor, Aider. Tier 3 (API key, planned): ChatGPT, Gemini.

**Is the daemon always running?**
Yes — `clawd` runs as a system service and starts on login. It uses very little CPU when idle. Sessions are persistent; you can close the app and reopen it without losing context.

**Can I use ClawDE on a VPS?**
The daemon is designed to run on a local machine you own, not a cloud VPS. If you want cloud hosting, that's what ClawDE Cloud provides.

**Where is my data stored?**
All sessions, messages, and settings are stored in a local SQLite database at `~/.clawd/clawd.db`. Nothing leaves your machine unless you enable remote access.

**Is ClawDE open source?**
Yes. The Rust daemon and Flutter apps are MIT licensed in this repository. The ClawDE Cloud backend (which we operate) is proprietary.

**How do I report a bug?**
Open an [issue](https://github.com/nself-org/clawde/issues/new/choose) using the bug report template.
