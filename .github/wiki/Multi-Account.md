# Multi-Account

ClawDE supports multiple AI provider accounts. This is useful when one account hits a
rate limit or usage cap — the daemon can switch automatically (on paid tiers) or prompt
you to confirm (Free tier).

## How account switching works

| Tier | Behavior when rate-limited |
| --- | --- |
| **Free** ($0) | Daemon pauses and shows a prompt asking you to confirm the switch |
| **Personal Remote** ($9.99/yr) | Daemon switches silently to the next account in your list |
| **Cloud** ($20+/month) | We manage the account pool — no configuration needed |

Starting with v1.0.9, the daemon uses round-robin selection when multiple non-rate-limited accounts are available, rather than always picking by priority order. The daemon tracks a cursor through the account list and advances it on each new session, wrapping back to the start after the last account. Priority ordering is still used as a fallback when only one account is available or when all others are rate-limited.

## Adding accounts (self-hosted)

Account management is configured via `config.toml`. The daemon reads all accounts in
order and rotates through them when a rate limit is encountered.

```toml
[accounts]
provider = "claude"

[[accounts.entries]]
name = "primary"
cli_path = "/usr/local/bin/claude"   # optional: override PATH lookup

[[accounts.entries]]
name = "backup"
cli_path = "/home/user/.nvm/versions/node/v20/bin/claude"
```

Each entry uses the auth credentials stored in the corresponding CLI installation.
Add accounts by authenticating with `claude auth login` in each CLI location.

## Rate limit handling

When the current session hits a rate limit (`error -32003 rateLimited`):

1. The daemon logs the event
2. On Free tier: the session is paused (`session.statusChanged { status: "paused" }`);
   a push event is sent to connected clients with a reason of `"rate_limited"`; the user
   must confirm account switch via the client app or RPC
3. On Personal Remote: the daemon selects the next account, restarts the provider
   subprocess, and resumes the session automatically

## Checking account status

```sh
clawd status
```

The output includes the active account name and whether the daemon is rate-limited.

Over JSON-RPC:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "daemon.status",
  "params": {}
}
```

Response includes `activeAccount` and `rateLimited` fields.

## account.* RPC API

Manage accounts programmatically via the JSON-RPC 2.0 API.

### account.list

Returns all configured accounts in priority order.

```json
{ "jsonrpc": "2.0", "id": 1, "method": "account.list", "params": {} }
```

```json
{
  "accounts": [
    { "id": "acc-001", "name": "primary", "provider": "claude", "priority": 0 },
    { "id": "acc-002", "name": "backup", "provider": "claude", "priority": 1 }
  ]
}
```

### account.create

Add a new account. The `credentials_path` is the directory where the provider CLI stores its auth (optional — defaults to the CLI's standard location).

```json
{
  "jsonrpc": "2.0", "id": 2, "method": "account.create",
  "params": { "name": "backup", "provider": "claude", "credentials_path": "/home/user/.claude-backup" }
}
```

### account.delete

```json
{
  "jsonrpc": "2.0", "id": 3, "method": "account.delete",
  "params": { "account_id": "acc-002" }
}
```

### account.setPriority

Set the priority order for account rotation. Lower number = tried first.

```json
{
  "jsonrpc": "2.0", "id": 4, "method": "account.setPriority",
  "params": { "account_id": "acc-002", "priority": 0 }
}
```

### account.history

View recent limit events and switch history for an account.

```json
{
  "jsonrpc": "2.0", "id": 5, "method": "account.history",
  "params": { "account_id": "acc-001", "limit": 20 }
}
```

Returns a list of events with `event_type` (`limited` / `switched` / `priority_changed`) and timestamps.

## Push events

| Event | When |
| --- | --- |
| `session.accountLimited` | The active account hit a rate limit |
| `session.accountSwitched` | The daemon switched to a different account |

Both events include `session_id`, `from_account`, and `to_account` fields.

## Cloud tier

Cloud tier users don't configure accounts. The ClawDE relay provisions AI capacity from
our account pool and routes requests automatically. Rate limits are handled transparently
with no user intervention.

---

## Related

- [[Configuration]] — full `config.toml` reference including `[accounts]`
- [[Daemon-Reference|Daemon API Reference]] — `daemon.status`, error code `-32003`
- [[Providers]] — provider setup (Claude Code, Codex)
