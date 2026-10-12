//! ADR-003 policy gate for MCP `tools/call`.
//!
//! `PolicyGate` runs the whole policy chain in front of every tool handler,
//! in this order, stopping at the first failure (deny by default):
//!
//! 1. caller identity is present
//! 2. trust registry: `TrustDatabase::is_tool_allowed(caller, tool)`
//! 3. task-state lookup succeeded (a lookup error denies)
//! 4. `PolicyEngine::evaluate` (Deny and NeedsApproval both refuse; this
//!    gate has no approval-grant path, so it fails closed)
//! 5. supply chain: read-only `SupplyChainPolicy::verify` of the providing server
//! 6. `PolicyHooks::pre_tool` (write-tool state gate, secrets in arguments)
//! 7. handler runs, then `PolicyHooks::post_tool` redaction of the output
//!
//! Denials are returned as `anyhow` errors carrying `MCP_POLICY_DENIED:` or
//! `MCP_SUPPLY_CHAIN_DENIED:` prefixes, which `McpDispatcher::classify_error`
//! maps to wire codes. Arguments are never logged.

use std::fmt;
use std::future::Future;
use std::sync::Arc;

use serde_json::{json, Value};
use tracing::warn;

use crate::policy::hooks::PolicyHooks;
use crate::policy::{PolicyDecision, PolicyEngine};
use crate::tasks::reducer::TaskState;

/// The MCP server that provides the tools being gated.
///
/// For clawd's built-in tools the caller passes clawd's own identity.
#[derive(Debug, Clone)]
pub struct ServerIdentity {
    /// Server name as registered in the supply-chain allowlist.
    pub name: String,
    /// Command used to launch the server.
    pub command: String,
    /// Arguments passed to the command.
    pub args: Vec<String>,
}

/// Why the gate refused a call.
#[derive(Debug, Clone, PartialEq)]
pub enum GateDenial {
    /// Caller missing, or not trusted for this tool.
    UnknownCaller(String),
    /// The policy engine or the pre-tool hook denied the call.
    PolicyDenied(String),
    /// The call needs approval and no approval was granted.
    ApprovalRequired(String),
    /// The providing server failed the supply-chain check.
    SupplyChain(String),
    /// A step of the chain itself failed (for example task lookup).
    ChainError(String),
}

impl GateDenial {
    fn kind(&self) -> &'static str {
        match self {
            Self::UnknownCaller(_) => "unknown_caller",
            Self::PolicyDenied(_) => "policy_denied",
            Self::ApprovalRequired(_) => "approval_required",
            Self::SupplyChain(_) => "supply_chain",
            Self::ChainError(_) => "chain_error",
        }
    }

    fn detail(&self) -> &str {
        match self {
            Self::UnknownCaller(d)
            | Self::PolicyDenied(d)
            | Self::ApprovalRequired(d)
            | Self::SupplyChain(d)
            | Self::ChainError(d) => d,
        }
    }

    /// Convert to an `anyhow::Error` with the wire-mapping message prefix.
    pub fn into_error(self) -> anyhow::Error {
        anyhow::anyhow!("{}", self)
    }
}

impl fmt::Display for GateDenial {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::SupplyChain(d) => write!(f, "MCP_SUPPLY_CHAIN_DENIED: {d}"),
            other => write!(f, "MCP_POLICY_DENIED: {}: {}", other.kind(), other.detail()),
        }
    }
}

/// Runs the ADR-003 chain in front of tool handlers.
pub struct PolicyGate {
    engine: Arc<PolicyEngine>,
    server: ServerIdentity,
}

impl PolicyGate {
    /// Build a gate over `engine` for tools provided by `server`.
    pub fn new(engine: Arc<PolicyEngine>, server: ServerIdentity) -> Self {
        Self { engine, server }
    }

    /// Run steps 1-6 of the chain. Returns the resolved task state on success.
    pub async fn check(
        &self,
        caller: &str,
        tool: &str,
        args: &Value,
        agent_id: &str,
        task_state: anyhow::Result<Option<TaskState>>,
    ) -> Result<Option<TaskState>, GateDenial> {
        if caller.is_empty() {
            return Err(GateDenial::UnknownCaller("caller identity missing".into()));
        }
        if !self
            .engine
            .trust_db
            .read()
            .await
            .is_tool_allowed(caller, tool)
        {
            return Err(GateDenial::UnknownCaller(format!(
                "caller '{caller}' not trusted for tool '{tool}'"
            )));
        }
        let state = task_state
            .map_err(|e| GateDenial::ChainError(format!("task state lookup failed: {e}")))?;

        match self
            .engine
            .evaluate(tool, args, state.as_ref(), agent_id)
            .await
        {
            PolicyDecision::Allow => {}
            PolicyDecision::Deny { reason } => return Err(GateDenial::PolicyDenied(reason)),
            PolicyDecision::NeedsApproval { reason, .. } => {
                return Err(GateDenial::ApprovalRequired(reason))
            }
        }

        let sc_args: Vec<&str> = self.server.args.iter().map(String::as_str).collect();
        self.engine
            .supply_chain
            .verify(&self.server.name, &self.server.command, &sc_args)
            .map_err(|v| GateDenial::SupplyChain(v.to_string()))?;

        let task_id = args.get("task_id").and_then(Value::as_str).unwrap_or("");
        let hook_state = state.clone().unwrap_or(TaskState::Pending);
        PolicyHooks::pre_tool(tool, args, task_id, &hook_state)
            .map_err(|v| GateDenial::PolicyDenied(v.to_string()))?;
        Ok(state)
    }

    /// Check the chain, call `handler(args)` only if every step passed, then
    /// redact the output. Handler errors propagate unchanged.
    pub async fn guard<F, Fut>(
        &self,
        caller: &str,
        tool: &str,
        args: Value,
        agent_id: &str,
        task_state: anyhow::Result<Option<TaskState>>,
        handler: F,
    ) -> anyhow::Result<Value>
    where
        F: FnOnce(Value) -> Fut,
        Fut: Future<Output = anyhow::Result<Value>>,
    {
        if let Err(denial) = self.check(caller, tool, &args, agent_id, task_state).await {
            warn!(
                tool,
                caller,
                kind = denial.kind(),
                "MCP tool call denied by policy chain"
            );
            return Err(denial.into_error());
        }
        let task_id = args
            .get("task_id")
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string();
        let result = handler(args).await?;
        Ok(redact_output(tool, result, &task_id))
    }
}

/// Redact secrets from a tool result using `PolicyHooks::post_tool`.
///
/// Returns the redacted value (parsed back to JSON when possible, otherwise
/// wrapped as `{"redacted": "<text>"}`), or `result` unchanged when clean.
pub fn redact_output(tool: &str, result: Value, task_id: &str) -> Value {
    match PolicyHooks::post_tool(tool, &result, task_id).redacted_display {
        Some(s) => serde_json::from_str(&s).unwrap_or_else(|_| json!({ "redacted": s })),
        None => result,
    }
}

/// Map a task storage status string to a reducer `TaskState`.
///
/// Unknown strings return `None` (callers then treat the state as unknown).
pub fn task_state_from_status(status: &str) -> Option<TaskState> {
    Some(match status {
        "pending" => TaskState::Pending,
        "planned" => TaskState::Planned,
        "claimed" => TaskState::Claimed,
        "in_progress" | "active" => TaskState::Active,
        "blocked" => TaskState::Blocked,
        "needs_approval" => TaskState::NeedsApproval,
        "code_review" => TaskState::CodeReview,
        "qa" => TaskState::Qa,
        "done" => TaskState::Done,
        "canceled" | "cancelled" => TaskState::Canceled,
        "failed" => TaskState::Failed,
        _ => return None,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn status_mapping() {
        assert_eq!(
            task_state_from_status("in_progress"),
            Some(TaskState::Active)
        );
        assert_eq!(
            task_state_from_status("cancelled"),
            Some(TaskState::Canceled)
        );
        assert_eq!(task_state_from_status("bogus"), None);
    }

    #[test]
    fn denial_prefixes() {
        let e = GateDenial::SupplyChain("x".into()).into_error().to_string();
        assert_eq!(e, "MCP_SUPPLY_CHAIN_DENIED: x");
        let e = GateDenial::ChainError("db".into()).into_error().to_string();
        assert_eq!(e, "MCP_POLICY_DENIED: chain_error: db");
    }
}
