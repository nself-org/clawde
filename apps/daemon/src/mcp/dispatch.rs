//! MCP `tools/call` dispatcher — routes tool invocations to internal handlers.
//!
//! `McpDispatcher` holds a reference to `AppContext` and maps tool names to
//! the handler functions in `mcp::tools::*`. Every call first passes the
//! ADR-003 policy chain in [`PolicyGate`](super::policy_gate::PolicyGate)
//! (trust, policy evaluate, supply chain, pre-tool hook) and its output is
//! redacted afterwards. Write tools (apply_patch, run_tests) additionally
//! verify that the referenced task is Active+Claimed. One dispatcher serves
//! one MCP connection; `client_id` is the client identity established by
//! ADR-004 transport auth (bearer token or peer credentials) and is the trust
//! key. It must NEVER be the self-reported `clientInfo.name`.
//!
//! **Not wired yet.** Do not connect this dispatcher to a server loop until
//! ADR-004 auth, the step-5 sandbox and the audit rows exist.
use crate::AppContext;
use serde_json::Value;
use std::sync::Arc;
use tracing::{info, warn};

use super::policy_gate::{task_state_from_status, GateDenial, PolicyGate};
use super::tools as tool_list;
use super::transport::{
    McpError, MCP_INVALID_PARAMS, MCP_POLICY_DENIED, MCP_PROVIDER_NOT_AVAILABLE,
    MCP_SUPPLY_CHAIN_DENIED, MCP_UNKNOWN_ENTITY,
};
use crate::tasks::reducer::TaskState;

/// Write tools that require the task to be Active+Claimed before proceeding.
/// `transition_task` and `claim_task` are included so agents cannot advance
/// task state or claim tasks without going through proper ownership checks.
const WRITE_TOOLS: &[&str] = &["apply_patch", "run_tests", "transition_task", "claim_task"];

/// Routes `tools/call` requests for one MCP connection through the policy gate.
pub struct McpDispatcher {
    ctx: Arc<AppContext>,
    gate: Arc<PolicyGate>,
    client_id: String,
}

impl McpDispatcher {
    /// Build a dispatcher for one connection. `client_id` is the identity
    /// established by ADR-004 transport auth (never the self-reported
    /// `clientInfo.name`) and is checked against the trust registry.
    pub fn new(ctx: Arc<AppContext>, gate: Arc<PolicyGate>, client_id: String) -> Self {
        Self {
            ctx,
            gate,
            client_id,
        }
    }

    /// Dispatch a `tools/call` invocation.
    ///
    /// `tool_name`  — the `name` field from the MCP `tools/call` params.
    /// `arguments`  — the `arguments` object from the MCP `tools/call` params.
    /// `agent_id`   — optional agent identifier from the calling session.
    ///
    /// Returns `Ok(Value)` with the (redacted) tool result, or
    /// `Err(anyhow::Error)` whose message encodes a MCP error code (for example
    /// `"MCP_INVALID_PARAMS: ..."`, `"MCP_POLICY_DENIED: ..."`) so callers can
    /// map it with [`classify_error`](Self::classify_error).
    pub async fn dispatch(
        &self,
        tool_name: &str,
        arguments: Value,
        agent_id: Option<String>,
    ) -> anyhow::Result<Value> {
        // Verify the tool is in our catalogue first.
        let known = tool_list::clawd_tools()
            .into_iter()
            .any(|t| t.name == tool_name);
        if !known {
            let msg = format!("unknown tool: {}", tool_name);
            warn!(tool = tool_name, "MCP unknown tool");
            return Err(anyhow::anyhow!("MCP_INVALID_PARAMS: {}", msg));
        }

        let state = self.resolve_task_state(&arguments).await;
        let aid = agent_id.clone();
        let result = self
            .gate
            .guard(
                &self.client_id,
                tool_name,
                arguments,
                agent_id.as_deref().unwrap_or(""),
                state,
                |args| async move {
                    // For write tools, verify the task is Active+Claimed.
                    if WRITE_TOOLS.contains(&tool_name) {
                        if aid.is_none() {
                            return Err(GateDenial::PolicyDenied(
                                "agent identity required for write tools".into(),
                            )
                            .into_error());
                        }
                        self.verify_active_claimed(&args, aid.as_deref()).await?;
                    }
                    self.route(tool_name, args, aid.as_deref()).await
                },
            )
            .await?;

        info!(
            tool = tool_name,
            agent = agent_id.as_deref().unwrap_or("unknown"),
            "MCP tool executed"
        );
        Ok(result)
    }

    /// Look up the task named by `arguments.task_id` (if any) and map its
    /// status to a `TaskState`. A storage error is passed on so the gate denies.
    async fn resolve_task_state(&self, arguments: &Value) -> anyhow::Result<Option<TaskState>> {
        let Some(id) = arguments.get("task_id").and_then(|v| v.as_str()) else {
            return Ok(None);
        };
        Ok(self
            .ctx
            .task_storage
            .get_task(id)
            .await?
            .and_then(|t| task_state_from_status(&t.status)))
    }

    /// Route to the correct handler.
    async fn route(
        &self,
        tool_name: &str,
        args: Value,
        agent_id: Option<&str>,
    ) -> anyhow::Result<Value> {
        use super::tools::{patch, task};
        match tool_name {
            "create_task" => task::create_task(&self.ctx, args).await,
            "claim_task" => task::claim_task(&self.ctx, args, agent_id).await,
            "log_event" => task::log_event(&self.ctx, args, agent_id).await,
            "apply_patch" => patch::apply_patch(&self.ctx, args, agent_id).await,
            "run_tests" => task::run_tests(&self.ctx, args, agent_id).await,
            "request_approval" => task::request_approval(&self.ctx, args, agent_id).await,
            "transition_task" => task::transition_task(&self.ctx, args, agent_id).await,
            other => Err(anyhow::anyhow!(
                "MCP_INVALID_PARAMS: unknown tool: {}",
                other
            )),
        }
    }

    /// Verify that the `task_id` in `arguments` corresponds to a task that is
    /// currently `in_progress` and claimed by `agent_id`.
    ///
    /// Returns `Err` with a `MCP_PROVIDER_NOT_AVAILABLE` message if the task is
    /// not in an Active+Claimed state, so the caller can map it to error -32002.
    async fn verify_active_claimed(
        &self,
        arguments: &Value,
        agent_id: Option<&str>,
    ) -> anyhow::Result<()> {
        let task_id = arguments
            .get("task_id")
            .and_then(|v| v.as_str())
            .ok_or_else(|| {
                anyhow::anyhow!("MCP_INVALID_PARAMS: missing required field 'task_id'")
            })?;

        let task = self
            .ctx
            .task_storage
            .get_task(task_id)
            .await?
            .ok_or_else(|| {
                anyhow::anyhow!("MCP_PROVIDER_NOT_AVAILABLE: task '{}' not found", task_id)
            })?;

        if task.status != "in_progress" {
            return Err(anyhow::anyhow!(
                "MCP_PROVIDER_NOT_AVAILABLE: task '{}' is in state '{}' — must be 'in_progress' (Active+Claimed)",
                task_id,
                task.status
            ));
        }

        // If agent_id is provided, verify ownership.
        if let Some(aid) = agent_id {
            if task.claimed_by.as_deref() != Some(aid) {
                return Err(anyhow::anyhow!(
                    "MCP_PROVIDER_NOT_AVAILABLE: task '{}' is claimed by '{}', not '{}'",
                    task_id,
                    task.claimed_by.as_deref().unwrap_or("unknown"),
                    aid
                ));
            }
        }

        Ok(())
    }

    /// Convert an `anyhow::Error` returned from `dispatch` into a `McpError`
    /// with the correct code.  This is a helper for the MCP message loop.
    pub fn classify_error(err: &anyhow::Error) -> McpError {
        let msg = err.to_string();
        if msg.starts_with("MCP_INVALID_PARAMS:") {
            let detail = msg.trim_start_matches("MCP_INVALID_PARAMS:").trim();
            McpError::new(MCP_INVALID_PARAMS, detail)
        } else if msg.starts_with("MCP_UNKNOWN_ENTITY:") {
            let detail = msg.trim_start_matches("MCP_UNKNOWN_ENTITY:").trim();
            McpError::new(MCP_UNKNOWN_ENTITY, detail)
        } else if msg.starts_with("MCP_POLICY_DENIED:") {
            let detail = msg.trim_start_matches("MCP_POLICY_DENIED:").trim();
            McpError::new(MCP_POLICY_DENIED, detail)
        } else if msg.starts_with("MCP_SUPPLY_CHAIN_DENIED:") {
            let detail = msg.trim_start_matches("MCP_SUPPLY_CHAIN_DENIED:").trim();
            McpError::new(MCP_SUPPLY_CHAIN_DENIED, detail)
        } else if msg.starts_with("MCP_PROVIDER_NOT_AVAILABLE:") {
            let detail = msg.trim_start_matches("MCP_PROVIDER_NOT_AVAILABLE:").trim();
            McpError::new(MCP_PROVIDER_NOT_AVAILABLE, detail)
        } else {
            McpError::new(-32603, "internal error")
        }
    }
}
