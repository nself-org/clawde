//! Model Context Protocol (MCP) implementation for `clawd`.
//!
//! This module covers two roles:
//!
//! 1. **MCP Server** — `clawd` exposes its task-management tools to MCP clients
//!    (e.g. Claude Code, Codex) via `tools/list` and `tools/call`.
//!
//! 2. **MCP Client** — `clawd` can connect to upstream MCP servers (configured
//!    in `.claw/mcp-servers.json`) and proxy their tools to local agents.
//!
//! ## Protocol version
//! MCP 2024-11-05.
//!
//! ## Submodules
//!
//! | Module | Role |
//! |--------|------|
//! | `transport` | JSON-RPC wire types, lifecycle handlers, progress notifications |
//! | `tools` | `tools/list` response — the 7 ClawDE tool definitions |
//! | `dispatch` | `tools/call` dispatcher — runs the policy gate, then routes to `tools::task` / `tools::patch` |
//! | `policy_gate` | ADR-003 chain (trust, evaluate, supply chain, pre-tool, redaction) in front of every handler |
//! | `capabilities` | Capability negotiation during `initialize` handshake |
//! | `config` | `.claw/mcp-servers.json` loader |
//! | `client` | Upstream MCP client (stdio subprocess) |
//! | `tools::task` | create_task, claim_task, log_event, run_tests, request_approval, transition_task |
//! | `tools::patch` | apply_patch with idempotency |

pub mod capabilities;
pub mod client;
pub mod config;
pub mod dispatch;
pub mod policy_gate;
pub mod resources;
pub mod tools;
pub mod transport;

// ─── Flat re-exports ──────────────────────────────────────────────────────────

pub use transport::{
    handle_initialize, handle_initialized, handle_ping, send_progress, McpCancelledNotification,
    McpError, McpMessage, McpProgressNotification, McpResponse, McpTransport, McpTransportHandler,
    MCP_INTERNAL_ERROR, MCP_INVALID_PARAMS, MCP_INVALID_REQUEST, MCP_METHOD_NOT_FOUND,
    MCP_PARSE_ERROR, MCP_POLICY_DENIED, MCP_PROVIDER_NOT_AVAILABLE, MCP_SUPPLY_CHAIN_DENIED,
};

pub use tools::{clawd_tools, handle_tools_list, McpToolDef};

pub use dispatch::McpDispatcher;

pub use policy_gate::{GateDenial, PolicyGate, ServerIdentity};

pub use client::{McpClient, McpServerConfig, McpTrustLevel};

pub use config::McpServersConfig;

pub use capabilities::{negotiate, ClawdCapabilities};

pub use resources::{list_resources, read_resource, ResourceDescriptor};
