# ClawDE Local MCP Server Specification

**Feature ID:** F-CLAWDE:local-mcp-server
**Phase:** P1 · Epic E2 · Wave W4 · Sprint S04 · Ticket P1-E2-W4-S04-T01
**ADRs:** ADR-001 (service boundary), ADR-003 (policy + trust), ADR-004 (local auth), ADR-008 (hook contract)
**Status:** Planned · spec_path: `clawde/.github/wiki/mcp-server-spec.md` · owning_wave: W4

---

## 1. Transport Choice

The ClawDE local MCP server (`clawd` process) exposes tool surfaces to Claude Code (CC), OpenCode (OC), and the nClaw mobile companion. Three transport modes are supported:

| Transport | Platform | Default? | Discovery |
|---|---|---|---|
| **stdio** | macOS / Linux / Windows | **Yes — W4 default** | Hook script injects `CLAWDE_MCP_SOCKET_PATH` env var (per ADR-008) |
| **Unix domain socket** | macOS / Linux | Opt-in | `CLAWDE_MCP_SOCKET_PATH=/tmp/clawd.<workspace>.sock` |
| **Loopback TCP (`127.0.0.1:7430`)** | Windows / IDE plugins | Fallback | Token in `Authorization: Bearer <workspace_token>` |

**Rationale for stdio as default:**
- Zero port conflict — no port is reserved or opened.
- LEDGER §G lock: stdio is the lowest-friction integration for CC/OC standard MCP launchers.
- Stdio framing: JSON-RPC 2.0 newline-delimited over stdin/stdout (one JSON object per line).
- No persistent process to track — the MCP server subprocess is spawned on demand by the host.

**Unix socket opt-in:** For persistent/daemon connections where spawning per-session is expensive, operators may set `transport = "unix_socket"` in `~/.config/clawde/mcp-server.toml`. The socket path defaults to `/tmp/clawd.<workspace>.sock`.

**Windows fallback:** No Unix sockets on Windows. Loopback TCP `127.0.0.1:7430` is used. The binding asserts `SO_REUSEADDR=false` so only one clawd instance can hold the port. Bearer token auth is mandatory on TCP (see Section 3).

### Versioning

The server Info response includes a `version` field (semantic version). Clients negotiate minimum protocol version on connect. Clients with a higher minimum than the server's version must abort and display an upgrade prompt.

```json
{"protocolVersion":"2024-11-05","capabilities":{...},"serverInfo":{"name":"clawde-mcp","version":"1.0.0"}}
```

---

## 2. Tool Surface Registry

All tool calls flow through the 5-step dispatch chain (Section 4). Tools bound to the local filesystem enforce the SandboxGuard workspace-root constraint (per ADR-003).

### 2.1 `bash`

| Field | Value |
|---|---|
| **Permission hint** | `execute` |
| **Sandbox constraint** | Working directory pinned to workspace root; symlinks resolved before exec; no `..` traversal |

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "command": { "type": "string", "description": "Shell command string" },
    "working_dir": { "type": "string", "description": "Optional subdirectory within workspace root; defaults to workspace root" },
    "timeout_ms": { "type": "integer", "default": 2000, "description": "Execution timeout (per ADR-008 HookContract)" }
  },
  "required": ["command"]
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "exit_code": { "type": "integer" },
    "stdout": { "type": "string" },
    "stderr": { "type": "string" },
    "truncated": { "type": "boolean", "description": "True if output exceeded 8KB cap (ADR-008)" }
  },
  "required": ["exit_code", "stdout", "stderr"]
}
```

**Example call:**
```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bash","arguments":{"command":"cargo test --test integration_test"}}}
```

**Example response:**
```json
{"jsonrpc":"2.0","id":1,"result":{"exit_code":0,"stdout":"test result: ok. 3 passed","stderr":"","truncated":false}}
```

---

### 2.2 `read_file`

| Field | Value |
|---|---|
| **Permission hint** | `read` |
| **Sandbox constraint** | Path must be within workspace root (canonicalized + checked) |

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "path": { "type": "string", "description": "File path relative to workspace root, or absolute path within workspace" },
    "encoding": { "type": "string", "enum": ["utf8", "base64"], "default": "utf8" }
  },
  "required": ["path"]
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "content": { "type": "string" },
    "size_bytes": { "type": "integer" },
    "encoding": { "type": "string" },
    "truncated": { "type": "boolean" }
  },
  "required": ["content", "size_bytes", "encoding"]
}
```

**Example call:**
```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"src/main.rs"}}}
```

**Example response:**
```json
{"jsonrpc":"2.0","id":2,"result":{"content":"fn main() {\n    println!(\"hello\");\n}\n","size_bytes":38,"encoding":"utf8","truncated":false}}
```

---

### 2.3 `write_file`

| Field | Value |
|---|---|
| **Permission hint** | `write` |
| **Sandbox constraint** | Destination path must be within workspace root; no creation outside workspace root |

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "path": { "type": "string", "description": "File path relative to workspace root" },
    "content": { "type": "string" },
    "encoding": { "type": "string", "enum": ["utf8", "base64"], "default": "utf8" },
    "create_parents": { "type": "boolean", "default": false }
  },
  "required": ["path", "content"]
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "bytes_written": { "type": "integer" },
    "path": { "type": "string", "description": "Canonicalized absolute path written" }
  },
  "required": ["bytes_written", "path"]
}
```

**Example call:**
```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"write_file","arguments":{"path":"README.md","content":"# My Project\n"}}}
```

**Example response:**
```json
{"jsonrpc":"2.0","id":3,"result":{"bytes_written":14,"path":"/home/user/projects/myproject/README.md"}}
```

---

### 2.4 `list_dir`

| Field | Value |
|---|---|
| **Permission hint** | `read` |
| **Sandbox constraint** | Directory must be within workspace root |

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "path": { "type": "string", "description": "Directory path relative to workspace root; defaults to workspace root" },
    "recursive": { "type": "boolean", "default": false },
    "include_hidden": { "type": "boolean", "default": false },
    "max_depth": { "type": "integer", "default": 3 }
  }
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "entries": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": { "type": "string" },
          "path": { "type": "string" },
          "kind": { "type": "string", "enum": ["file", "dir", "symlink"] },
          "size_bytes": { "type": "integer" },
          "modified_at": { "type": "string", "format": "date-time" }
        }
      }
    },
    "total": { "type": "integer" },
    "truncated": { "type": "boolean" }
  },
  "required": ["entries", "total"]
}
```

**Example call:**
```json
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_dir","arguments":{"path":"src","recursive":false}}}
```

**Example response:**
```json
{"jsonrpc":"2.0","id":4,"result":{"entries":[{"name":"main.rs","path":"src/main.rs","kind":"file","size_bytes":38,"modified_at":"2026-06-01T10:00:00Z"}],"total":1,"truncated":false}}
```

---

### 2.5 `search`

| Field | Value |
|---|---|
| **Permission hint** | `read` |
| **Sandbox constraint** | Search root must be within workspace root |

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "query": { "type": "string", "description": "Search term or regex pattern" },
    "path": { "type": "string", "description": "Root directory to search; defaults to workspace root" },
    "glob": { "type": "string", "description": "File glob filter, e.g. '*.rs'" },
    "is_regex": { "type": "boolean", "default": false },
    "case_sensitive": { "type": "boolean", "default": false },
    "max_results": { "type": "integer", "default": 50 }
  },
  "required": ["query"]
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "matches": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "file": { "type": "string" },
          "line": { "type": "integer" },
          "col": { "type": "integer" },
          "text": { "type": "string", "description": "Matching line content" }
        }
      }
    },
    "total_matches": { "type": "integer" },
    "truncated": { "type": "boolean" }
  },
  "required": ["matches", "total_matches"]
}
```

**Example call:**
```json
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search","arguments":{"query":"fn main","glob":"*.rs"}}}
```

**Example response:**
```json
{"jsonrpc":"2.0","id":5,"result":{"matches":[{"file":"src/main.rs","line":1,"col":1,"text":"fn main() {"}],"total_matches":1,"truncated":false}}
```

---

## 3. Auth Boundary and Client Discovery

Per ADR-004. Discovery mechanism differs by caller type.

### 3.1 Claude Code (CC)

- Hook script `clawde-session-start.sh` (installed by `HostAdapterClaudeCode.Install()` per ADR-008) injects `CLAWDE_MCP_SOCKET_PATH` into the CC session environment.
- CC's `.mcp.json` in the workspace root references `clawd` as the MCP server process (stdio transport) or the socket path (Unix socket transport).
- **Unix socket:** CC connects; `clawd` reads `SO_PEERCRED`; rejects if peer UID differs from clawd's UID.
- **TCP loopback (Windows):** CC sends `Authorization: Bearer <token>`. Token file: `~/.claude/hooks/.clawde-token-<workspace>` mode `0600`. Token is HMAC-SHA256(workspace_id || pid || boot_time, $CLAWDE_SESSION_SECRET), TTL 24h.
- **Trust level:** CC = trust level 3 (elevated) in `cd_trust_registry`.

### 3.2 OpenCode (OC)

- Same hook contract as CC (per ADR-008 shared `HostAdapter` interface).
- `HostAdapterOpenCode.Install()` writes the `providers[clawde]` block to `~/.config/opencode/config.json`.
- Transport and token auth identical to CC; trust level 3.

### 3.3 nClaw Mobile Companion

- Discovery via QR code pairing per ADR-004 one-time code flow:
  1. User opens ClawDE app → taps "Pair mobile" → `clawd` generates 6-digit one-time code, valid 5 minutes, single-use.
  2. Mobile scans code → sends `(code, device_pubkey)` to local relay.
  3. `clawd` verifies code → issues device-scoped API key with `local` + `read` scopes.
  4. All future mobile calls use API key (Bearer token), not the one-time code.
- **Trust level:** nClaw mobile = trust level 1 (limited) in `cd_trust_registry`. Only tier-1 tools (`read_file`, `list_dir`, `search`) permitted; `bash` and `write_file` require trust level ≥ 2.

### 3.4 Token file location and permissions

```
~/.claude/hooks/.clawde-token-<workspace>
  permissions: 0600 (owner read-only)
  contents:    HMAC-SHA256 workspace token
  TTL:         24h or until clawd exits
  rotation:    clawde token rotate
```

---

## 4. Dispatch Chain

Per ADR-003. Every tool call passes all 5 steps. Any step failure short-circuits with deny + audit row; handler is never executed on failure.

```
mcp_request
  │
  ├── 1. auth (ADR-004)
  │      Bearer token validation OR Unix socket SO_PEERCRED check.
  │      Failure: {code: -32001, message: "unauthorized"} — no audit row (caller unknown).
  │
  ├── 2. mcp_trust_registry.lookup(client_id)
  │      Look up caller in cd_trust_registry. Unknown caller → deny.
  │      Returns: trust_level (0..3), per-tool permissions JSONB.
  │      Failure: {code: -32002, message: "unknown_entity"} + audit row.
  │
  ├── 3. PolicyEngine.evaluate(tool_id, caller_ctx)
  │      # P1: AllowAll stub — replace in W14-T02
  │      Algorithm (deny-by-default):
  │        a. Lookup caller — if unknown → DENY (unknown_entity)
  │        b. Explicit per-tool permission in trust_registry JSONB → ALLOW or DENY
  │        c. Otherwise: apply trust-level default table
  │      Returns: PolicyResult enum {Allow | Deny | NotConfigured}
  │      Deny: {code: -32003, message: "policy_denied", reason: <string>} + audit row.
  │
  ├── 4. SupplyChainPolicy.check(tool_id, model_id?)
  │      Validate server binary SHA256 against cd_mcp_server_allowlist.
  │      New server → DENY until operator runs: nself mcp allow --server <id> --sha256 <hash>
  │      Failure: {code: -32004, message: "supply_chain_denied"} + audit row.
  │
  ├── 5. SandboxGuard.check(workspace, path_args)
  │      Canonicalize all path arguments; resolve symlinks.
  │      Assert: canonical path must be a descendant of workspace root.
  │      Network-touching tools: validate target host against cd_egress_allowlist.
  │      Failure: {code: -32005, message: "sandbox_violation", path: <path>} + audit row.
  │
  └── 6. handler.dispatch(...)
         Execute the tool handler. Return result to caller.
```

### PolicyResult enum

```rust
pub enum PolicyResult {
    Allow,
    Deny { reason: String },
    NotConfigured,  // falls through to trust-level default
}
```

### Audit log row schema

Every dispatch step that produces a Deny result writes one row:

```json
{
  "timestamp": "2026-06-01T10:00:00.123Z",
  "entity_id": "cc:workspace-abc123",
  "tool_id": "bash",
  "trust_level": 3,
  "result": "Allow",
  "reason": "trust_level_default_allow",
  "latency_ms": 1
}
```

Table: `cd_policy_audit_log` (SQLite in `clawd`; columns as above + `id INTEGER PRIMARY KEY`).

---

## 5. Health Check Endpoint

**Path:** `GET /health`
**Latency SLO:** < 5ms at p99
**Auth:** None required (read-only, no sensitive data)

### Response schema

```json
{
  "type": "object",
  "properties": {
    "status": { "type": "string", "enum": ["ok", "degraded", "error"] },
    "pid": { "type": "integer" },
    "uptime_s": { "type": "integer", "description": "Seconds since clawd start" },
    "mcp_version": { "type": "string", "description": "MCP protocol version, e.g. '2024-11-05'" },
    "active_tools": { "type": "integer", "description": "Count of registered tools" },
    "last_policy_eval_ms": { "type": "integer", "description": "Latency of most recent PolicyEngine.evaluate call" }
  },
  "required": ["status", "pid", "uptime_s", "mcp_version", "active_tools"]
}
```

**Example response (200 OK):**
```json
{
  "status": "ok",
  "pid": 12345,
  "uptime_s": 3600,
  "mcp_version": "2024-11-05",
  "active_tools": 5,
  "last_policy_eval_ms": 1
}
```

**Availability:** Health endpoint is served on the stdio/socket/TCP transport only when `clawd` is running. On TCP, it is served at `http://127.0.0.1:7430/health`.

---

## 6. Local-Only Binding Invariant

The MCP server MUST bind to `127.0.0.1` only. Binding to `0.0.0.0` is explicitly rejected at startup. This is enforced by a unit test:

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_binding_rejects_0000() {
        let cfg = McpServerConfig {
            transport: Transport::TcpLoopback { addr: "0.0.0.0".parse().unwrap(), port: 7430 },
            ..Default::default()
        };
        let result = validate_config(&cfg);
        assert!(result.is_err(), "0.0.0.0 binding must be rejected");
        let err = result.unwrap_err().to_string();
        assert!(err.contains("bind_0000_rejected"), "error must name the invariant");
    }
}
```

Any config specifying `0.0.0.0` causes `clawd` to exit with error code 1 and log message `[ERROR] MCP server: bind_0000_rejected — external binding is forbidden`.

---

## 7. Config Contract

**Location:** `~/.config/clawde/mcp-server.toml`

```toml
# ClawDE MCP Server configuration
# Generated by: clawd init
# Do not set transport = tcp_loopback on macOS/Linux unless required by IDE plugin

# Transport mode: stdio (default), unix_socket, tcp_loopback
transport = "stdio"

# Unix socket path (used when transport = "unix_socket")
# Default: /tmp/clawd.<workspace>.sock (auto-derived from workspace_id)
# socket_path = "/tmp/clawd.myworkspace.sock"

# TCP port for loopback transport (Windows / IDE plugins)
# Default: 7430
# port = 7430

# Timeout for every ClawDE HTTP/RPC call from hook scripts (ADR-008 HookContract)
tool_timeout_ms = 2000

# P1: AllowAll policy stub mode (true in P1, replaced in W14)
# Set to false ONLY after W14-T02 ships the real PolicyEngine.evaluate()
policy_stub_mode = true

# Workspace Bearer token file path
# Default: ~/.claude/hooks/.clawde-token-<workspace>
# Per ADR-004: mode 0600, TTL 24h, rotated by `clawde token rotate`
# token_path = "~/.claude/hooks/.clawde-token-myworkspace"
```

### Required config fields

| Field | Type | Default | Notes |
|---|---|---|---|
| `transport` | enum | `stdio` | `stdio` / `unix_socket` / `tcp_loopback` |
| `socket_path` | string | `/tmp/clawd.<workspace>.sock` | Used when transport = unix_socket |
| `port` | integer | `7430` | Used when transport = tcp_loopback |
| `tool_timeout_ms` | integer | `2000` | ADR-008 HookContract hard limit |
| `policy_stub_mode` | boolean | `true` | True in P1; false only after W14-T02 |
| `token_path` | string | `~/.claude/hooks/.clawde-token-<workspace>` | LEDGER §G / ADR-004 |

---

## 8. Unit Test Plan

Tests cover the MCP server contract. These are spec-level descriptions of required tests; implementation in W4 build tickets.

| Test | Coverage | Expected result |
|---|---|---|
| `test_server_starts_on_stdio` | Server starts, emits MCP initialize response on stdio | `{"jsonrpc":"2.0","result":{"protocolVersion":"2024-11-05",...}}` |
| `test_server_starts_on_unix_socket` | Server binds Unix socket at configured path | Socket file exists at `socket_path`; `connect()` succeeds |
| `test_server_starts_on_tcp_loopback` | Server binds `127.0.0.1:7430` | `curl http://127.0.0.1:7430/health` returns `{"status":"ok",...}` |
| `test_binding_rejects_0000` | `0.0.0.0` binding is rejected at config validation | Returns error with `bind_0000_rejected` |
| `test_tools_list_returns_correct_schema` | `tools/list` RPC returns all 5 tools with correct schema | Response includes `bash`, `read_file`, `write_file`, `list_dir`, `search` each with `inputSchema` |
| `test_policy_engine_wired_allowany` | PolicyEngine.evaluate wired; returns Allow in stub mode | Tool call with `policy_stub_mode=true` completes; audit row result = "Allow" |
| `test_health_endpoint_returns_200` | `GET /health` returns 200 with correct fields | `status`, `pid`, `uptime_s`, `mcp_version`, `active_tools` all present |
| `test_sandbox_guard_rejects_path_traversal` | Path arg with `..` traversal rejected by SandboxGuard | Error `{code: -32005, message: "sandbox_violation"}` |
| `test_sandbox_guard_rejects_symlink_escape` | Symlink pointing outside workspace root rejected | Error `{code: -32005, message: "sandbox_violation"}` |
| `test_auth_rejects_invalid_token` | Request with wrong Bearer token rejected at auth step | Error `{code: -32001, message: "unauthorized"}` |

---

## 9. W14 Replacement Point

The `PolicyEngine.evaluate()` stub (AllowAll) ships in P1 Wave W4 through W13. The stub is marked in source code with:

```rust
// P1: AllowAll stub — replace in W14-T02
pub fn evaluate(&self, _tool_id: &str, _caller_ctx: &CallerContext) -> PolicyResult {
    PolicyResult::Allow
}
```

The real implementation lands in **W14-T02** (CC companion ticket). The closing_gate SIEGE security vector for P1 explicitly tests deny-by-default behavior against the live PolicyEngine.

---

*Spec created: 2026-06-01 by P1-E2-W4-S04-T01 executor. See ADR-001, ADR-003, ADR-004, ADR-008 for architectural decisions.*
