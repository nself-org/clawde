# Security

ClawDE is built for developers who want AI assistance without routing their code through
third-party servers. This page explains the security model for both local and remote use.

## Self-hosted mode (Free + Personal Remote)

Your code never leaves your machine unless you explicitly enable remote access.

- The daemon runs locally on `localhost:4300`
- AI provider processes (`claude`, `codex`) run on your machine using your credentials
- No code, messages, or session data is sent to ClawDE servers
- Your Anthropic/OpenAI credentials stay in their own CLI config (`~/.claude/`, etc.)

## Local auth token

Every connection to the daemon must present an auth token before calling any RPC method.

The token is generated at first start and stored at:

| Platform | Path |
| --- | --- |
| macOS | `~/Library/Application Support/clawd/auth_token` |
| Linux | `~/.local/share/clawd/auth_token` |
| Windows | `%APPDATA%\clawd\auth_token` |

The file is readable only by the current user (`chmod 0600` on Unix). The daemon rejects
any connection that sends a wrong or missing token and closes the WebSocket.

To print your token:

```sh
clawd token show
```

To display it as a QR code for mobile pairing:

```sh
clawd token qr
```

## Remote access (Personal Remote tier)

Remote access routes traffic through the ClawDE relay at `api.clawde.io`.

- **TLS in transit** — all relay traffic is encrypted with TLS 1.3
- **Device pairing** — each remote device is issued a unique device token via the QR/PIN
  pairing flow. Device tokens are stored by the daemon in SQLite and can be revoked
  with `device.revoke`
- **Relay never sees plaintext session data** — the relay is a WebSocket proxy; it
  forwards framed JSON-RPC messages without inspecting content
- **Bearer token auth** — the daemon validates device tokens on every connection;
  an expired or revoked token is rejected with error `-32004`

## Threat model

| Threat | Mitigation |
| --- | --- |
| Local process reads auth token | Token file is 0600 (owner read-only) |
| Network attacker intercepts LAN traffic | Bind to `127.0.0.1` by default; LAN mode requires explicit `--bind 0.0.0.0` |
| Stolen device token | Revoke via `device.revoke` RPC or `clawd` CLI; token is invalidated immediately |
| Malicious tool call | Tool calls are logged; destructive calls can be configured to require approval |
| Relay MitM | TLS 1.3; cert pinning planned for a future release |

## Reporting vulnerabilities

Report security issues privately via GitHub Security Advisories:
<https://github.com/nself-org/clawde/security/advisories/new>

Please do not open public issues for security vulnerabilities.

---

## Related

- [[Architecture]] — relay topology
- [[Getting-Started]] — install and first run
- [[Daemon-Reference|Daemon API Reference]] — `device.pair`, `device.revoke`, `daemon.auth`
