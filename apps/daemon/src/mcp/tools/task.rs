/// MCP tool handlers for task lifecycle operations.
///
/// Covers: create_task, claim_task, log_event, run_tests, request_approval,
/// and transition_task.  `apply_patch` lives in `tools/patch.rs`.
use crate::AppContext;
use anyhow::Result;
use serde_json::{json, Value};
use uuid::Uuid;

// ─── Helpers ──────────────────────────────────────────────────────────────────

fn str_arg<'a>(args: &'a Value, key: &str) -> Result<&'a str> {
    args.get(key)
        .and_then(|v| v.as_str())
        .ok_or_else(|| anyhow::anyhow!("MCP_INVALID_PARAMS: missing required field '{}'", key))
}

fn opt_str<'a>(args: &'a Value, key: &str) -> Option<&'a str> {
    args.get(key).and_then(|v| v.as_str())
}

// ─── create_task ─────────────────────────────────────────────────────────────

/// MCP `create_task` handler.
///
/// Required: `title`, `repo`.
/// Optional: `summary`, `acceptance_criteria`, `priority`, `labels`.
///
/// Returns `{"task_id": "...", "status": "pending"}`.
pub async fn create_task(ctx: &AppContext, args: Value) -> Result<Value> {
    let title = str_arg(&args, "title")?;
    let repo = str_arg(&args, "repo")?;

    let summary = opt_str(&args, "summary");
    let priority = opt_str(&args, "priority").unwrap_or("medium");

    // Serialise acceptance_criteria and labels to JSON strings for storage.
    let acceptance_criteria: Option<Vec<String>> = args
        .get("acceptance_criteria")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(String::from))
                .collect()
        });

    let labels: Option<Vec<String>> = args.get("labels").and_then(|v| v.as_array()).map(|arr| {
        arr.iter()
            .filter_map(|v| v.as_str().map(String::from))
            .collect()
    });

    // Build the `notes` field from summary + acceptance criteria.
    let notes_parts: Vec<String> = {
        let mut parts = Vec::new();
        if let Some(s) = summary {
            parts.push(s.to_string());
        }
        if let Some(ref ac) = acceptance_criteria {
            if !ac.is_empty() {
                parts.push(format!("Acceptance criteria:\n{}", ac.join("\n- ")));
            }
        }
        parts
    };
    let notes_str = if notes_parts.is_empty() {
        None
    } else {
        Some(notes_parts.join("\n\n"))
    };

    // Serialise tags/labels to JSON array string.
    let tags_json = labels
        .map(|l| serde_json::to_string(&l).unwrap_or_else(|_| "[]".into()))
        .unwrap_or_else(|| "[]".into());

    // Validate repo path exists.
    if !std::path::Path::new(repo).exists() {
        anyhow::bail!("MCP_INVALID_PARAMS: repo path does not exist: {}", repo);
    }

    let task_id = Uuid::new_v4().to_string();

    let task = ctx
        .task_storage
        .add_task(
            &task_id,
            title,
            Some("code"),     // task_type
            None,             // phase
            None,             // group
            None,             // parent_id
            Some(priority),   // severity mapped from priority
            None,             // file
            None,             // files
            None,             // depends_on
            Some(&tags_json), // tags
            None,             // estimated_minutes
            repo,
        )
        .await?;

    // Append notes to the task if provided.
    if let Some(ref notes) = notes_str {
        // Update notes via status (notes param).
        // We use a no-op status update to just set the notes field.
        let _ = ctx
            .task_storage
            .update_status(&task_id, &task.status, Some(notes.as_str()), None)
            .await;
    }

    // Emit task.created (log only — broadcaster stub).
    tracing::info!(task_id = %task_id, title = %title, "MCP task.created");

    Ok(json!({
        "task_id": task_id,
        "status": "pending"
    }))
}

// ─── claim_task ───────────────────────────────────────────────────────────────

/// MCP `claim_task` handler.
///
/// Required: `task_id`.
/// `agent_id` is supplied by the dispatcher from the calling session context.
///
/// Returns `{"claimed": true}` or propagates an error.
pub async fn claim_task(ctx: &AppContext, args: Value, agent_id: Option<&str>) -> Result<Value> {
    let task_id = str_arg(&args, "task_id")?;
    let aid = agent_id.unwrap_or("mcp-agent");

    ctx.task_storage.claim_task(task_id, aid, None).await?;

    let _ = ctx
        .task_storage
        .log_activity(
            aid,
            Some(task_id),
            None,
            "task_claimed",
            "system",
            Some("Claimed via MCP tool"),
            None,
            "",
        )
        .await;

    tracing::info!(task_id = %task_id, agent = %aid, "MCP claim_task");

    Ok(json!({ "claimed": true }))
}

// ─── log_event ────────────────────────────────────────────────────────────────

/// MCP `log_event` handler.
///
/// Required: `task_id`, `event_type`, `data`.
///
/// Returns `{"logged": true}`.
pub async fn log_event(ctx: &AppContext, args: Value, agent_id: Option<&str>) -> Result<Value> {
    let task_id = str_arg(&args, "task_id")?;
    let event_type = str_arg(&args, "event_type")?;
    let data = args
        .get("data")
        .ok_or_else(|| anyhow::anyhow!("MCP_INVALID_PARAMS: missing required field 'data'"))?;

    let aid = agent_id.unwrap_or("mcp-agent");
    let meta_str = serde_json::to_string(data).ok();

    let task = ctx
        .task_storage
        .get_task(task_id)
        .await?
        .ok_or_else(|| anyhow::anyhow!("MCP_INVALID_PARAMS: task '{}' not found", task_id))?;

    ctx.task_storage
        .log_activity(
            aid,
            Some(task_id),
            task.phase.as_deref(),
            event_type,
            "system",
            None,
            meta_str.as_deref(),
            &task.repo_path,
        )
        .await?;

    tracing::debug!(task_id = %task_id, event_type = %event_type, "MCP log_event");

    Ok(json!({ "logged": true }))
}

// ─── run_tests ────────────────────────────────────────────────────────────────

/// MCP `run_tests` handler.
///
/// Required: `task_id`, `idempotency_key`.
/// Optional: `command` (defaults to `"cargo test"`).
///
/// Spawns the test command in the task's repo path as a background job.
/// Returns `{"started": true, "job_id": "<uuid>"}` immediately.
pub async fn run_tests(ctx: &AppContext, args: Value, agent_id: Option<&str>) -> Result<Value> {
    let task_id = str_arg(&args, "task_id")?;
    let idempotency_key = str_arg(&args, "idempotency_key")?;
    let command = opt_str(&args, "command").unwrap_or("cargo test");
    let aid = agent_id.unwrap_or("mcp-agent");

    let task = ctx
        .task_storage
        .get_task(task_id)
        .await?
        .ok_or_else(|| anyhow::anyhow!("MCP_INVALID_PARAMS: task '{}' not found", task_id))?;

    let job_id = Uuid::new_v4().to_string();

    // Log the test run intention.
    let detail = format!(
        "run_tests: command='{}' idempotency_key='{}' job_id='{}'",
        command, idempotency_key, job_id
    );
    let _ = ctx
        .task_storage
        .log_activity(
            aid,
            Some(task_id),
            task.phase.as_deref(),
            "run_tests",
            "system",
            Some(&detail),
            None,
            &task.repo_path,
        )
        .await;

    // Spawn the test command as a detached background task.
    // Results are reported via broadcaster events when the command completes.
    let repo_path = task.repo_path.clone();
    let command_str = command.to_string();
    let broadcaster = ctx.broadcaster.clone();
    let task_id_owned = task_id.to_string();
    let job_id_clone = job_id.clone();

    tokio::spawn(async move {
        let parts: Vec<&str> = command_str.splitn(2, ' ').collect();
        let (prog, arg) = match parts.as_slice() {
            [p] => (*p, ""),
            [p, a] => (*p, *a),
            _ => return,
        };

        let mut cmd = tokio::process::Command::new(prog);
        if !arg.is_empty() {
            cmd.arg(arg);
        }
        cmd.current_dir(&repo_path);

        let output = cmd.output().await;
        let (success, stdout, stderr) = match output {
            Ok(o) => (
                o.status.success(),
                String::from_utf8_lossy(&o.stdout).to_string(),
                String::from_utf8_lossy(&o.stderr).to_string(),
            ),
            Err(e) => (false, String::new(), e.to_string()),
        };

        broadcaster.broadcast(
            "task.testResult",
            serde_json::json!({
                "task_id": task_id_owned,
                "job_id": job_id_clone,
                "success": success,
                "stdout": stdout,
                "stderr": stderr,
            }),
        );
    });

    tracing::info!(
        task_id = %task_id,
        command = %command,
        job_id = %job_id,
        "MCP run_tests started"
    );

    Ok(json!({
        "started": true,
        "job_id": job_id
    }))
}

// ─── request_approval ────────────────────────────────────────────────────────

/// MCP `request_approval` handler.
///
/// Required: `task_id`, `tool_name`, `arguments`, `risk_level`.
///
/// Broadcasts a `tool.approvalRequested` push event.  Returns `{"pending": true, "approval_id": "..."}`.
pub async fn request_approval(
    ctx: &AppContext,
    args: Value,
    agent_id: Option<&str>,
) -> Result<Value> {
    let task_id = str_arg(&args, "task_id")?;
    let tool_name = str_arg(&args, "tool_name")?;
    let risk_level = str_arg(&args, "risk_level")?;
    let tool_arguments = args
        .get("arguments")
        .cloned()
        .unwrap_or(Value::Object(Default::default()));

    let aid = agent_id.unwrap_or("mcp-agent");
    let approval_id = Uuid::new_v4().to_string();

    // Broadcast the approval request to connected clients (Flutter / web app).
    ctx.broadcaster.broadcast(
        "tool.approvalRequested",
        json!({
            "approval_id": approval_id,
            "task_id": task_id,
            "agent_id": aid,
            "tool_name": tool_name,
            "arguments": tool_arguments,
            "risk_level": risk_level,
        }),
    );

    // Transition task to needs_approval state so approval.list can find it.
    // Store approval_id in notes for correlation by approval.respond.
    let _ = ctx
        .task_storage
        .update_status(task_id, "needs_approval", Some(&approval_id), None)
        .await;

    let _ = ctx
        .task_storage
        .log_activity(
            aid,
            Some(task_id),
            None,
            "approval_requested",
            "system",
            Some(&format!(
                "approval_id={} tool={} risk={}",
                approval_id, tool_name, risk_level
            )),
            None,
            "",
        )
        .await;

    tracing::info!(
        task_id = %task_id,
        tool = %tool_name,
        risk = %risk_level,
        approval_id = %approval_id,
        "MCP request_approval broadcast"
    );

    Ok(json!({
        "pending": true,
        "approval_id": approval_id
    }))
}

// ─── transition_task ─────────────────────────────────────────────────────────

/// MCP `transition_task` handler.
///
/// Required: `task_id`, `new_state`.
/// Optional: `reason` (required when transitioning to `done` or `blocked`).
///
/// Returns `{"transitioned": true}`.
pub async fn transition_task(
    ctx: &AppContext,
    args: Value,
    agent_id: Option<&str>,
) -> Result<Value> {
    let task_id = str_arg(&args, "task_id")?;
    let new_state = str_arg(&args, "new_state")?;
    let reason = opt_str(&args, "reason");
    let aid = agent_id.unwrap_or("mcp-agent");

    // `done` requires completion notes.
    let notes = if new_state == "done" {
        Some(reason.unwrap_or("Completed via MCP transition_task."))
    } else {
        reason
    };

    let block_reason = if new_state == "blocked" { reason } else { None };

    ctx.task_storage
        .update_status(task_id, new_state, notes, block_reason)
        .await?;

    let _ = ctx
        .task_storage
        .log_activity(
            aid,
            Some(task_id),
            None,
            "task_transition",
            "system",
            Some(&format!("→ {} {}", new_state, reason.unwrap_or(""))),
            None,
            "",
        )
        .await;

    ctx.broadcaster.broadcast(
        "task.statusChanged",
        json!({
            "task_id": task_id,
            "new_state": new_state,
            "agent_id": aid,
        }),
    );

    tracing::info!(task_id = %task_id, new_state = %new_state, agent = %aid, "MCP transition_task");

    Ok(json!({ "transitioned": true }))
}
