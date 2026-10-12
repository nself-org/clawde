/// MCP JSON-RPC 2.0 transport types and lifecycle handlers.
///
/// Supports the Model Context Protocol (MCP) specification version 2024-11-05.
/// Transport variants: stdio (for subprocess MCP servers) and WebSocket (for
/// network-attached MCP servers).
use serde::{Deserialize, Serialize};
use serde_json::Value;

// ─── Transport enum ───────────────────────────────────────────────────────────

/// Which transport a MCP connection uses.
#[derive(Debug, Clone)]
pub enum McpTransport {
    /// Standard I/O — used when spawning MCP servers as child processes.
    Stdio,
    /// WebSocket at the given address (e.g., `ws://127.0.0.1:9000`).
    WebSocket(String),
}

// ─── Core message types ───────────────────────────────────────────────────────

/// An outgoing MCP JSON-RPC 2.0 request or notification.
///
/// Notifications (no `id`) use the same wire format but expect no response.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpMessage {
    pub jsonrpc: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<Value>,
    pub method: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub params: Option<Value>,
}

impl McpMessage {
    /// Create a request (has an id, expects a response).
    pub fn request(id: Value, method: impl Into<String>, params: Option<Value>) -> Self {
        Self {
            jsonrpc: "2.0".into(),
            id: Some(id),
            method: method.into(),
            params,
        }
    }

    /// Create a notification (no id, no response expected).
    pub fn notification(method: impl Into<String>, params: Option<Value>) -> Self {
        Self {
            jsonrpc: "2.0".into(),
            id: None,
            method: method.into(),
            params,
        }
    }
}

/// A MCP JSON-RPC 2.0 response (success or error).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpResponse {
    pub jsonrpc: String,
    pub id: Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<McpError>,
}

impl McpResponse {
    /// Construct a successful response.
    pub fn ok(id: Value, result: Value) -> Self {
        Self {
            jsonrpc: "2.0".into(),
            id,
            result: Some(result),
            error: None,
        }
    }

    /// Construct an error response.
    pub fn error(id: Value, error: McpError) -> Self {
        Self {
            jsonrpc: "2.0".into(),
            id,
            result: None,
            error: Some(error),
        }
    }
}

/// A MCP JSON-RPC error object.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpError {
    pub code: i32,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

impl McpError {
    pub fn new(code: i32, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            data: None,
        }
    }

    pub fn with_data(mut self, data: Value) -> Self {
        self.data = Some(data);
        self
    }
}

// ─── Standard MCP error codes ─────────────────────────────────────────────────

pub const MCP_PARSE_ERROR: i32 = -32700;
pub const MCP_INVALID_REQUEST: i32 = -32600;
pub const MCP_METHOD_NOT_FOUND: i32 = -32601;
pub const MCP_INVALID_PARAMS: i32 = -32602;
pub const MCP_INTERNAL_ERROR: i32 = -32603;
/// Maps to clawd providerNotAvailable — task not in Active+Claimed state.
pub const MCP_PROVIDER_NOT_AVAILABLE: i32 = -32002;
/// Unknown or untrusted caller (spec section 4, unknown_entity). Same value as
/// [`MCP_PROVIDER_NOT_AVAILABLE`]; separate name so call sites say which they mean.
pub const MCP_UNKNOWN_ENTITY: i32 = -32002;
/// Tool call refused by the ADR-003 policy chain (trust, policy, pre-tool hook).
pub const MCP_POLICY_DENIED: i32 = -32003;
/// Tool call refused because the providing MCP server failed supply-chain checks.
pub const MCP_SUPPLY_CHAIN_DENIED: i32 = -32004;

// ─── Lifecycle params ─────────────────────────────────────────────────────────

/// Client information sent in the `initialize` request.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpClientInfo {
    pub name: String,
    pub version: String,
}

/// Params for the `initialize` RPC method.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpInitializeParams {
    #[serde(rename = "protocolVersion")]
    pub protocol_version: String,
    pub capabilities: Value,
    #[serde(rename = "clientInfo")]
    pub client_info: McpClientInfo,
}

/// Response body for the `initialize` RPC method.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpInitializeResult {
    #[serde(rename = "protocolVersion")]
    pub protocol_version: String,
    pub capabilities: Value,
    #[serde(rename = "serverInfo")]
    pub server_info: McpServerInfo,
}

/// Server identification block included in `initialize` responses.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpServerInfo {
    pub name: String,
    pub version: String,
}

// ─── Progress / cancellation notifications ────────────────────────────────────

/// `notifications/progress` — sent server → client to report long-running progress.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpProgressNotification {
    pub method: String,
    pub params: McpProgressParams,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpProgressParams {
    #[serde(rename = "progressToken")]
    pub progress_token: String,
    pub progress: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub total: Option<u64>,
}

impl McpProgressNotification {
    pub fn new(token: impl Into<String>, progress: u64, total: Option<u64>) -> Self {
        Self {
            method: "notifications/progress".into(),
            params: McpProgressParams {
                progress_token: token.into(),
                progress,
                total,
            },
        }
    }

    /// Serialise to a JSON string for sending over the wire.
    pub fn to_json(&self) -> String {
        serde_json::to_string(self).unwrap_or_default()
    }
}

/// `notifications/cancelled` — client cancels a pending request.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpCancelledNotification {
    pub method: String,
    pub params: McpCancelledParams,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpCancelledParams {
    #[serde(rename = "requestId")]
    pub request_id: Value,
    pub reason: String,
}

impl McpCancelledNotification {
    pub fn new(request_id: Value, reason: impl Into<String>) -> Self {
        Self {
            method: "notifications/cancelled".into(),
            params: McpCancelledParams {
                request_id,
                reason: reason.into(),
            },
        }
    }
}

/// Convenience function: send a progress notification as a serialised JSON string.
pub fn send_progress(token: &str, progress: u64, total: Option<u64>) -> String {
    McpProgressNotification::new(token, progress, total).to_json()
}

// ─── Transport handler trait ──────────────────────────────────────────────────

/// Abstraction over the MCP message dispatch loop.
///
/// Implementors receive parsed `McpMessage` values and return an optional
/// `McpResponse` (notifications return `None`).
#[async_trait::async_trait]
pub trait McpTransportHandler: Send + Sync {
    async fn handle_message(&self, msg: McpMessage) -> Option<McpResponse>;
}

// ─── Lifecycle handlers ───────────────────────────────────────────────────────

/// Handle an `initialize` request from an MCP client.
///
/// Parses the client's capabilities (for future negotiation), returns the
/// `initialize` response with our protocol version and server info.
pub fn handle_initialize(id: Value, _params: Option<Value>) -> McpResponse {
    let result = McpInitializeResult {
        protocol_version: "2024-11-05".into(),
        capabilities: serde_json::json!({
            "tools": { "listChanged": false }
        }),
        server_info: McpServerInfo {
            name: "clawd".into(),
            version: env!("CARGO_PKG_VERSION").to_string(),
        },
    };

    McpResponse::ok(
        id,
        serde_json::to_value(&result).unwrap_or(serde_json::Value::Null),
    )
}

/// Handle a `ping` request — respond with an empty result.
pub fn handle_ping(id: Value) -> McpResponse {
    McpResponse::ok(id, serde_json::json!({}))
}

/// Handle the `initialized` notification — no response needed.
///
/// Called after the client receives the `initialize` response and is ready.
/// We log the event; no reply is sent (returns `None` in the dispatch loop).
pub fn handle_initialized() {
    tracing::debug!("MCP client sent 'initialized' notification — session is ready");
}
