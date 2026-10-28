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
//! `MCP_SUPPLY_CHAIN_DENIED:` prefixes (unknown caller: `MCP_UNKNOWN_ENTITY:`),
//! which `McpDispatcher::classify_error` maps to wire codes. Arguments are
//! never logged.
//!
//! **Caller identity.** The `caller` passed to the gate is the client identity
//! established by ADR-004 transport auth (bearer token or peer credentials).
//! It must never be the self-reported `clientInfo.name` from `initialize`.

use std::fmt;
use std::future::Future;
use std::sync::Arc;

use serde_json::Value;
use tracing::warn;

use crate::policy::hooks::PolicyHooks;
use crate::policy::mcp_trust::TrustLevel;
use crate::policy::output_scan::scan_error_output;
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
    /// Caller missing or not trusted.
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
            Self::UnknownCaller(d) => write!(f, "MCP_UNKNOWN_ENTITY: {d}"),
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
        {
            let trust = self.engine.trust_db.read().await;
            if !trust.is_tool_allowed(caller, tool) {
                // A trusted caller whose allow-list omits the tool is a policy
                // denial; only a missing or untrusted caller is an unknown entity.
                return Err(if trust.get_trust(caller) == TrustLevel::Trusted {
                    GateDenial::PolicyDenied(format!(
                        "tool '{tool}' not permitted for caller '{caller}'"
                    ))
                } else {
                    GateDenial::UnknownCaller(format!(
                        "caller '{caller}' not trusted for tool '{tool}'"
                    ))
                });
            }
        }
        let state = task_state.map_err(|e| {
            // Real cause stays server-side; the caller gets fixed text.
            warn!(tool, error = %e, "MCP policy chain: task state lookup failed");
            GateDenial::ChainError("task state lookup failed".into())
        })?;

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
    /// redact the output. Handler errors are passed through the same secret
    /// redaction; their MCP code prefix is kept.
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
        match handler(args).await {
            Ok(result) => Ok(redact_output(tool, result, &task_id)),
            Err(e) => Err(anyhow::anyhow!("{}", scan_error_output(&e.to_string()))),
        }
    }
}

/// Redact a tool result through [`PolicyHooks::post_tool`], the single
/// post-tool entry point. Returns the redacted value (same shape) when
/// redaction changed something, otherwise `result` unchanged.
pub fn redact_output(tool: &str, result: Value, task_id: &str) -> Value {
    match PolicyHooks::post_tool(tool, &result, task_id).redacted {
        Some(v) => v,
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

    use crate::policy::mcp_trust::{McpTrustEntry, TrustDatabase, TrustLevel};
    use crate::policy::risk::RiskDatabase;
    use crate::policy::supply_chain::SupplyChainPolicy;
    use serde_json::json;
    use tokio::sync::RwLock;

    /// A gate over the default risk rules where `caller` is trusted and the
    /// providing server is registered, so only the policy rules decide.
    fn gate() -> PolicyGate {
        let mut trust = TrustDatabase::empty();
        trust.upsert(McpTrustEntry {
            server_name: "caller".into(),
            trust: TrustLevel::Trusted,
            command_hash: None,
            allowed_tools: vec![],
        });
        let mut supply_chain = SupplyChainPolicy::empty();
        supply_chain
            .verify_or_register("clawd", "clawd", &[])
            .expect("register server");
        let engine = PolicyEngine::new(
            Arc::new(RwLock::new(RiskDatabase::default_rules())),
            Arc::new(RwLock::new(trust)),
            Arc::new(supply_chain),
        );
        PolicyGate::new(
            Arc::new(engine),
            ServerIdentity {
                name: "clawd".into(),
                command: "clawd".into(),
                args: vec![],
            },
        )
    }

    /// A trusted caller whose allow-list omits the tool gets a policy denial,
    /// not the unknown-entity code; an untrusted or unlisted caller still gets
    /// the unknown-entity code.
    #[tokio::test]
    async fn tool_outside_trusted_callers_allow_list_is_policy_denied() {
        let gate = gate();
        {
            let mut trust = gate.engine.trust_db.write().await;
            trust.upsert(McpTrustEntry {
                server_name: "limited".into(),
                trust: TrustLevel::Trusted,
                command_hash: None,
                allowed_tools: vec!["read_file".into()],
            });
            trust.upsert(McpTrustEntry {
                server_name: "shady".into(),
                trust: TrustLevel::Untrusted,
                command_hash: None,
                allowed_tools: vec![],
            });
        }
        let args = json!({ "path": "p" });
        let out = gate
            .check("limited", "apply_patch", &args, "agent-1", Ok(None))
            .await;
        assert!(matches!(out, Err(GateDenial::PolicyDenied(_))), "{out:?}");
        let msg = out.unwrap_err().to_string();
        assert!(msg.starts_with("MCP_POLICY_DENIED:"), "{msg}");

        for caller in ["shady", "nobody"] {
            let out = gate
                .check(caller, "read_file", &args, "agent-1", Ok(None))
                .await;
            assert!(matches!(out, Err(GateDenial::UnknownCaller(_))), "{out:?}");
        }
    }

    /// D-1141: `create_task` takes no `task_id`, so no task state exists for
    /// the Medium-risk Active-task rule to inspect. It must still pass.
    #[tokio::test]
    async fn create_task_without_task_id_passes_gate() {
        let args = json!({ "title": "t", "repo": "/r" });
        let out = gate()
            .check("caller", "create_task", &args, "agent-1", Ok(None))
            .await;
        assert_eq!(out, Ok(None));
    }

    /// D-1142: `request_approval` is how approval is initiated, so the gate
    /// must not answer it with ApprovalRequired. It needs an Active task.
    #[tokio::test]
    async fn request_approval_on_active_task_passes_gate() {
        let args = json!({
            "task_id": "t1",
            "tool_name": "apply_patch",
            "arguments": {},
            "risk_level": "high"
        });
        let out = gate()
            .check(
                "caller",
                "request_approval",
                &args,
                "agent-1",
                Ok(Some(TaskState::Active)),
            )
            .await;
        assert_eq!(out, Ok(Some(TaskState::Active)));
    }

    #[tokio::test]
    async fn request_approval_without_active_task_is_denied() {
        let args = json!({ "task_id": "t1", "tool_name": "apply_patch", "arguments": {}, "risk_level": "high" });
        for state in [None, Some(TaskState::Pending), Some(TaskState::Done)] {
            let out = gate()
                .check("caller", "request_approval", &args, "agent-1", Ok(state))
                .await;
            assert!(matches!(out, Err(GateDenial::PolicyDenied(_))), "{out:?}");
        }
    }

    /// The exemptions are per tool: other Medium and High tools keep their rules.
    #[tokio::test]
    async fn other_tools_keep_their_rules() {
        let g = gate();
        let args = json!({});
        let out = g
            .check("caller", "run_tests", &args, "agent-1", Ok(None))
            .await;
        assert!(matches!(out, Err(GateDenial::PolicyDenied(_))), "{out:?}");
        let out = g
            .check(
                "caller",
                "apply_patch",
                &args,
                "agent-1",
                Ok(Some(TaskState::Active)),
            )
            .await;
        assert!(
            matches!(out, Err(GateDenial::ApprovalRequired(_))),
            "{out:?}"
        );
    }

    #[test]
    fn denial_prefixes() {
        let e = GateDenial::SupplyChain("x".into()).into_error().to_string();
        assert_eq!(e, "MCP_SUPPLY_CHAIN_DENIED: x");
        let e = GateDenial::ChainError("db".into()).into_error().to_string();
        assert_eq!(e, "MCP_POLICY_DENIED: chain_error: db");
    }
}
