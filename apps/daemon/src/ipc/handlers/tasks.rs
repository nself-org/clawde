use crate::tasks::{
    events::{new_correlation_id, TaskEventKind},
    markdown_parser, queue_serializer,
    replay::ReplayEngine,
    schema::{Priority, RiskLevel, TaskSpec},
    storage::{ActivityQueryParams, TaskListParams, TASK_NOT_FOUND},
};
use crate::AppContext;
use anyhow::{anyhow, bail, Result};
use chrono::Utc;
use serde_json::{json, Value};
use sqlx::Row;

fn s(v: &Value, key: &str) -> Option<String> {
    v.get(key).and_then(|v| v.as_str()).map(String::from)
}
fn sv<'a>(v: &'a Value, key: &str) -> Option<&'a str> {
    v.get(key).and_then(|v| v.as_str())
}
fn n(v: &Value, key: &str) -> Option<i64> {
    v.get(key).and_then(|v| v.as_i64())
}

/// Validate a task ID: alphanumeric + hyphens + underscores only, 1-64 chars.
/// Prevents path traversal when task_id is used in filesystem paths.
fn validate_task_id(id: &str) -> Result<()> {
    if id.is_empty() || id.len() > 64 {
        bail!("invalid task_id: must be 1-64 characters");
    }
    if id.contains('\0') {
        bail!("invalid task_id: null byte");
    }
    if !id
        .chars()
        .all(|c| c.is_alphanumeric() || c == '-' || c == '_')
    {
        bail!(
            "invalid task_id: only alphanumeric characters, hyphens, and underscores are allowed"
        );
    }
    Ok(())
}

/// Validate a planning doc path for `tasks.fromPlanning`.
/// The path must be absolute and must be within the repo directory.
fn validate_planning_path(path: &str, repo_path: &str) -> Result<()> {
    if path.contains('\0') {
        bail!("invalid path: null byte");
    }
    let p = std::path::Path::new(path);
    if !p.is_absolute() {
        bail!("invalid path: planning doc path must be absolute");
    }
    // Verify the path is within the repo directory tree.
    if !p.starts_with(repo_path) {
        bail!("invalid path: planning doc must be within the repository directory");
    }
    Ok(())
}

pub async fn list(params: Value, ctx: &AppContext) -> Result<Value> {
    let query = TaskListParams {
        repo_path: s(&params, "repo_path"),
        status: s(&params, "status"),
        agent: s(&params, "agent"),
        severity: s(&params, "severity"),
        phase: s(&params, "phase"),
        tag: s(&params, "tag"),
        search: s(&params, "search"),
        limit: n(&params, "limit"),
        offset: n(&params, "offset"),
    };
    let tasks = ctx.task_storage.list_tasks(&query).await?;
    Ok(json!({ "tasks": tasks }))
}

pub async fn get(params: Value, ctx: &AppContext) -> Result<Value> {
    let id =
        sv(&params, "task_id").ok_or_else(|| anyhow::anyhow!("TASK_CODE:{}", TASK_NOT_FOUND))?;
    let task = ctx
        .task_storage
        .get_task(id)
        .await?
        .ok_or_else(|| anyhow::anyhow!("TASK_CODE:{}", TASK_NOT_FOUND))?;
    Ok(json!({ "task": task }))
}

pub async fn claim(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = sv(&params, "task_id").ok_or_else(|| anyhow::anyhow!("missing task_id"))?;
    let agent_id = sv(&params, "agent_id").ok_or_else(|| anyhow::anyhow!("missing agent_id"))?;

    let existing = ctx.task_storage.get_task(task_id).await?;
    let is_resume = existing
        .as_ref()
        .map(|t| t.status == "interrupted")
        .unwrap_or(false);

    let task = ctx.task_storage.claim_task(task_id, agent_id, None).await?;
    let _ = ctx
        .task_storage
        .set_agent_current_task(agent_id, Some(task_id))
        .await;

    let (action, detail) = if is_resume {
        (
            "session_resume",
            "Resumed from interrupted state.".to_string(),
        )
    } else {
        ("task_claimed", "pending → in_progress".to_string())
    };

    let _ = ctx
        .task_storage
        .log_activity(
            agent_id,
            Some(task_id),
            task.phase.as_deref(),
            action,
            "system",
            Some(&detail),
            None,
            &task.repo_path,
        )
        .await;

    let event = if is_resume {
        "task.resumed"
    } else {
        "task.claimed"
    };
    ctx.broadcaster.broadcast(
        event,
        json!({
            "task_id": task_id,
            "agent_id": agent_id,
            "is_resume": is_resume,
        }),
    );

    // WI.T10: Auto-create worktree on task claim (if repo_path is a valid git repo).
    if !task.repo_path.is_empty() {
        let repo_path = std::path::Path::new(&task.repo_path);
        if repo_path.exists() && ctx.worktree_manager.get(task_id).await.is_none() {
            match ctx
                .worktree_manager
                .create(task_id, &task.title, repo_path)
                .await
            {
                Ok(wt) => {
                    ctx.storage
                        .create_worktree(
                            task_id,
                            &wt.worktree_path.to_string_lossy(),
                            &wt.branch,
                            &task.repo_path,
                        )
                        .await
                        .ok();
                    tracing::info!(task_id, branch = %wt.branch, "auto-created worktree on task claim");
                }
                Err(e) => {
                    // Non-fatal: task claim still succeeds even if worktree creation fails.
                    tracing::warn!(task_id, err = %e, "worktree auto-create failed (non-fatal)");
                }
            }
        }
    }

    let _ = queue_serializer::flush_queue(&ctx.task_storage, &task.repo_path).await;
    Ok(json!({ "task": task, "is_resume": is_resume }))
}

pub async fn release(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = sv(&params, "task_id").ok_or_else(|| anyhow::anyhow!("missing task_id"))?;
    let agent_id = sv(&params, "agent_id").ok_or_else(|| anyhow::anyhow!("missing agent_id"))?;
    let task = ctx
        .task_storage
        .get_task(task_id)
        .await?
        .ok_or_else(|| anyhow::anyhow!("TASK_CODE:{}", TASK_NOT_FOUND))?;

    ctx.task_storage.release_task(task_id, agent_id).await?;
    let _ = ctx
        .task_storage
        .set_agent_current_task(agent_id, None)
        .await;
    let _ = queue_serializer::flush_queue(&ctx.task_storage, &task.repo_path).await;

    ctx.broadcaster
        .broadcast("task.released", json!({ "task_id": task_id }));
    Ok(json!({ "ok": true }))
}

pub async fn heartbeat(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = sv(&params, "task_id").ok_or_else(|| anyhow::anyhow!("missing task_id"))?;
    let agent_id = sv(&params, "agent_id").ok_or_else(|| anyhow::anyhow!("missing agent_id"))?;
    ctx.task_storage.heartbeat_task(task_id, agent_id).await?;
    ctx.task_storage.update_agent_heartbeat(agent_id).await?;
    Ok(json!({ "ok": true }))
}

pub async fn update_status(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = sv(&params, "task_id").ok_or_else(|| anyhow::anyhow!("missing task_id"))?;
    let new_status = sv(&params, "status").ok_or_else(|| anyhow::anyhow!("missing status"))?;
    let notes = sv(&params, "notes");
    let block_reason = sv(&params, "block_reason");
    let agent_id = sv(&params, "agent_id").unwrap_or("system");

    // EP.T03: Require evidence_pack_id when marking done/completed.
    if new_status == "done" || new_status == "completed" {
        let evidence_pack_id = sv(&params, "evidence_pack_id");
        if evidence_pack_id.is_none() || evidence_pack_id.map(|s| s.is_empty()).unwrap_or(true) {
            // Check if the task already has an evidence_pack_id recorded
            let has_evidence: Option<String> = sqlx::query_scalar(
                "SELECT evidence_pack_id FROM agent_tasks WHERE id = ? AND evidence_pack_id IS NOT NULL",
            )
            .bind(task_id)
            .fetch_optional(ctx.storage.pool())
            .await
            .unwrap_or(None)
            .flatten();

            if has_evidence.is_none() {
                tracing::warn!(
                    task_id,
                    "task.complete called without evidence_pack_id — consider running task.evidencePack first"
                );
                // Non-blocking warning for now — will become blocking in Sprint AA
            }
        }
    }

    // V02.T06-T08: Stub gate — check modified_files for stubs when marking done.
    if new_status == "done" || new_status == "completed" {
        if let Some(files_arr) = params.get("modified_files").and_then(|v| v.as_array()) {
            let modified_files: Vec<std::path::PathBuf> = files_arr
                .iter()
                .filter_map(|v| v.as_str())
                .map(std::path::PathBuf::from)
                .collect();

            if !modified_files.is_empty() {
                let project_root = infer_project_root(&modified_files)
                    .unwrap_or_else(|| std::path::PathBuf::from("."));
                let config = crate::tasks::stub_gate::CompletionChecksConfig::load(&project_root);
                let matches =
                    crate::tasks::stub_gate::check(&modified_files, &project_root, &config);
                if !matches.is_empty() {
                    anyhow::bail!("{}", crate::tasks::stub_gate::format_error(&matches));
                }
            }
        }
    }

    // WI.T10: Block task.complete if an active unmerged worktree exists.
    if new_status == "done" || new_status == "completed" {
        if let Some(wt) = ctx.worktree_manager.get(task_id).await {
            use crate::worktree::manager::WorktreeStatus;
            if matches!(wt.status, WorktreeStatus::Active | WorktreeStatus::Done) {
                anyhow::bail!(
                    "worktreeNotMerged: task '{}' has an active worktree on branch '{}' — \
                     call worktrees.accept or worktrees.reject before marking done",
                    task_id,
                    wt.branch
                );
            }
        }
    }

    let task = ctx
        .task_storage
        .update_status(task_id, new_status, notes, block_reason)
        .await?;

    let _ = ctx
        .task_storage
        .log_activity(
            agent_id,
            Some(task_id),
            task.phase.as_deref(),
            "status_transition",
            "system",
            Some(&format!("→ {}", new_status)),
            None,
            &task.repo_path,
        )
        .await;

    ctx.broadcaster.broadcast(
        "task.statusChanged",
        json!({
            "task_id": task_id,
            "status": new_status,
        }),
    );

    let _ = queue_serializer::flush_queue(&ctx.task_storage, &task.repo_path).await;
    Ok(json!({ "task": task }))
}

pub async fn add_task(params: Value, ctx: &AppContext) -> Result<Value> {
    let id = sv(&params, "id").ok_or_else(|| anyhow::anyhow!("missing id"))?;
    let title = sv(&params, "title").ok_or_else(|| anyhow::anyhow!("missing title"))?;
    let repo_path = sv(&params, "repo_path").ok_or_else(|| anyhow::anyhow!("missing repo_path"))?;

    let files_str = params.get("files").map(|v| v.to_string());
    let depends_str = params.get("depends_on").map(|v| v.to_string());
    let tags_str = params.get("tags").map(|v| v.to_string());

    let task = ctx
        .task_storage
        .add_task(
            id,
            title,
            sv(&params, "type"),
            sv(&params, "phase"),
            sv(&params, "group"),
            sv(&params, "parent_id"),
            sv(&params, "severity"),
            sv(&params, "file"),
            files_str.as_deref(),
            depends_str.as_deref(),
            tags_str.as_deref(),
            n(&params, "estimated_minutes"),
            repo_path,
        )
        .await?;

    ctx.broadcaster
        .broadcast("task.created", json!({ "task_id": id }));
    let _ = queue_serializer::flush_queue(&ctx.task_storage, repo_path).await;
    Ok(json!({ "task": task }))
}

pub async fn bulk_add(params: Value, ctx: &AppContext) -> Result<Value> {
    let tasks_arr = params
        .get("tasks")
        .and_then(|v| v.as_array())
        .ok_or_else(|| anyhow::anyhow!("missing tasks array"))?
        .clone();
    let repo_path = sv(&params, "repo_path")
        .ok_or_else(|| anyhow::anyhow!("missing repo_path"))?
        .to_string();

    let mut created = 0usize;
    for t in &tasks_arr {
        let id = t
            .get("id")
            .and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("task missing id"))?;
        let title = t
            .get("title")
            .and_then(|v| v.as_str())
            .ok_or_else(|| anyhow::anyhow!("task missing title"))?;
        ctx.task_storage
            .add_task(
                id,
                title,
                t.get("type").and_then(|v| v.as_str()),
                t.get("phase").and_then(|v| v.as_str()),
                t.get("group").and_then(|v| v.as_str()),
                t.get("parent_id").and_then(|v| v.as_str()),
                t.get("severity").and_then(|v| v.as_str()),
                t.get("file").and_then(|v| v.as_str()),
                None,
                None,
                None,
                t.get("estimated_minutes").and_then(|v| v.as_i64()),
                &repo_path,
            )
            .await?;
        created += 1;
    }

    let _ = queue_serializer::flush_queue(&ctx.task_storage, &repo_path).await;
    Ok(json!({ "created": created }))
}

pub async fn log_activity(params: Value, ctx: &AppContext) -> Result<Value> {
    let agent = sv(&params, "agent_id").ok_or_else(|| anyhow::anyhow!("missing agent_id"))?;
    let action = sv(&params, "action").ok_or_else(|| anyhow::anyhow!("missing action"))?;
    let repo_path = sv(&params, "repo_path").ok_or_else(|| anyhow::anyhow!("missing repo_path"))?;
    let meta_str = params.get("meta").map(|v| v.to_string());

    let entry = ctx
        .task_storage
        .log_activity(
            agent,
            sv(&params, "task_id"),
            sv(&params, "phase"),
            action,
            sv(&params, "entry_type").unwrap_or("auto"),
            sv(&params, "detail"),
            meta_str.as_deref(),
            repo_path,
        )
        .await?;

    ctx.broadcaster
        .broadcast("task.activityLogged", serde_json::to_value(&entry)?);
    Ok(json!({ "ok": true, "id": entry.id }))
}

pub async fn note(params: Value, ctx: &AppContext) -> Result<Value> {
    let agent = sv(&params, "agent_id").ok_or_else(|| anyhow::anyhow!("missing agent_id"))?;
    let note_text = sv(&params, "note").ok_or_else(|| anyhow::anyhow!("missing note"))?;
    let repo_path = sv(&params, "repo_path").ok_or_else(|| anyhow::anyhow!("missing repo_path"))?;
    // Truncate at char boundary — slicing by byte length panics on multi-byte chars.
    let note_text: &str = if note_text.chars().count() > 2000 {
        let byte_end = note_text
            .char_indices()
            .nth(2000)
            .map(|(i, _)| i)
            .unwrap_or(note_text.len());
        &note_text[..byte_end]
    } else {
        note_text
    };

    let entry = ctx
        .task_storage
        .post_note(
            agent,
            sv(&params, "task_id"),
            sv(&params, "phase"),
            note_text,
            repo_path,
        )
        .await?;

    ctx.broadcaster
        .broadcast("task.activityLogged", serde_json::to_value(&entry)?);
    Ok(json!({ "ok": true, "id": entry.id }))
}

pub async fn activity(params: Value, ctx: &AppContext) -> Result<Value> {
    let query = ActivityQueryParams {
        repo_path: s(&params, "repo_path"),
        task_id: s(&params, "task_id"),
        agent: s(&params, "agent"),
        phase: s(&params, "phase"),
        entry_type: s(&params, "entry_type"),
        action: s(&params, "action"),
        since: n(&params, "since"),
        limit: n(&params, "limit"),
        offset: n(&params, "offset"),
    };

    let entries = ctx.task_storage.query_activity(&query).await?;
    Ok(json!({ "entries": entries, "count": entries.len() }))
}

pub async fn from_planning(params: Value, ctx: &AppContext) -> Result<Value> {
    let path = sv(&params, "path").ok_or_else(|| anyhow::anyhow!("missing path"))?;
    let repo_path = sv(&params, "repo_path").ok_or_else(|| anyhow::anyhow!("missing repo_path"))?;

    // Validate that the path is absolute and within the repo directory.
    validate_planning_path(path, repo_path)?;

    let content = tokio::fs::read_to_string(path)
        .await
        .map_err(|e| anyhow::anyhow!("cannot read planning doc: {e}"))?;
    let parsed = markdown_parser::parse_active_md(&content);
    let count = ctx
        .task_storage
        .backfill_from_tasks(parsed, repo_path)
        .await?;
    let _ = queue_serializer::flush_queue(&ctx.task_storage, repo_path).await;

    Ok(json!({ "imported": count }))
}

pub async fn from_checklist(params: Value, ctx: &AppContext) -> Result<Value> {
    from_planning(params, ctx).await
}

pub async fn summary(params: Value, ctx: &AppContext) -> Result<Value> {
    let repo_path = sv(&params, "repo_path");
    ctx.task_storage.summary(repo_path).await
}

pub async fn export(params: Value, ctx: &AppContext) -> Result<Value> {
    let format = sv(&params, "format").unwrap_or("json");
    let repo_path = sv(&params, "repo_path");

    let tasks = ctx
        .task_storage
        .list_tasks(&TaskListParams {
            repo_path: repo_path.map(String::from),
            ..Default::default()
        })
        .await?;

    match format {
        "csv" => {
            let mut csv = "id,title,type,phase,severity,status,claimed_by,file,notes\n".to_string();
            for t in &tasks {
                csv.push_str(&format!(
                    "{},{},{},{},{},{},{},{},{}\n",
                    csv_escape(&t.id),
                    csv_escape(&t.title),
                    csv_escape(t.task_type.as_deref().unwrap_or("")),
                    csv_escape(t.phase.as_deref().unwrap_or("")),
                    csv_escape(t.severity.as_deref().unwrap_or("")),
                    t.status,
                    csv_escape(t.claimed_by.as_deref().unwrap_or("")),
                    csv_escape(t.file.as_deref().unwrap_or("")),
                    csv_escape(t.notes.as_deref().unwrap_or("")),
                ));
            }
            Ok(json!({ "format": "csv", "data": csv }))
        }
        _ => Ok(json!({ "format": "json", "tasks": tasks })),
    }
}

fn csv_escape(s: &str) -> String {
    if s.contains(',') || s.contains('"') || s.contains('\n') {
        format!("\"{}\"", s.replace('"', "\"\""))
    } else {
        s.to_string()
    }
}

pub async fn validate(params: Value, ctx: &AppContext) -> Result<Value> {
    let repo_path = sv(&params, "repo_path").unwrap_or("");

    // Validate that the resolved active.md path stays within the declared repo root.
    if !repo_path.is_empty() {
        let root = std::path::Path::new(repo_path);
        if !root.is_absolute() {
            anyhow::bail!("repo_path must be absolute");
        }
        let candidate = root.join(".claude/tasks/active.md");
        let canonical_root = std::fs::canonicalize(root).unwrap_or_else(|_| root.to_path_buf());
        let canonical_candidate =
            std::fs::canonicalize(&candidate).unwrap_or_else(|_| candidate.clone());
        if !canonical_candidate.starts_with(&canonical_root) {
            anyhow::bail!("active.md path escapes repo root");
        }
    }

    let active_md_path = format!("{}/.claude/tasks/active.md", repo_path);
    let content = tokio::fs::read_to_string(&active_md_path)
        .await
        .unwrap_or_default();
    let md_tasks = markdown_parser::parse_active_md(&content);

    let db_tasks = ctx
        .task_storage
        .list_tasks(&TaskListParams {
            repo_path: if repo_path.is_empty() {
                None
            } else {
                Some(repo_path.to_string())
            },
            ..Default::default()
        })
        .await?;

    let md_ids: std::collections::HashSet<String> = md_tasks.iter().map(|t| t.id.clone()).collect();
    let db_ids: std::collections::HashSet<String> = db_tasks.iter().map(|t| t.id.clone()).collect();

    let only_in_md: Vec<&String> = md_ids.difference(&db_ids).collect();
    let only_in_db: Vec<&String> = db_ids.difference(&md_ids).collect();

    let mismatches: Vec<Value> = md_tasks
        .iter()
        .filter_map(|md| {
            db_tasks.iter().find(|db| db.id == md.id).and_then(|db| {
                if db.status != md.status {
                    Some(json!({ "id": md.id, "md": md.status, "db": db.status }))
                } else {
                    None
                }
            })
        })
        .collect();

    Ok(json!({
        "only_in_markdown": only_in_md,
        "only_in_db": only_in_db,
        "status_mismatches": mismatches,
        "ok": only_in_md.is_empty() && only_in_db.is_empty() && mismatches.is_empty()
    }))
}

pub async fn sync(params: Value, ctx: &AppContext) -> Result<Value> {
    let repo_path = sv(&params, "repo_path").unwrap_or("");

    // Validate that the resolved active.md path stays within the declared repo root.
    if !repo_path.is_empty() {
        let root = std::path::Path::new(repo_path);
        if !root.is_absolute() {
            anyhow::bail!("repo_path must be absolute");
        }
        let candidate = root.join(".claude/tasks/active.md");
        let canonical_root = std::fs::canonicalize(root).unwrap_or_else(|_| root.to_path_buf());
        let canonical_candidate =
            std::fs::canonicalize(&candidate).unwrap_or_else(|_| candidate.clone());
        if !canonical_candidate.starts_with(&canonical_root) {
            anyhow::bail!("active.md path escapes repo root");
        }
    }

    let active_md_path = format!("{}/.claude/tasks/active.md", repo_path);
    let content = tokio::fs::read_to_string(&active_md_path)
        .await
        .unwrap_or_default();
    let parsed = markdown_parser::parse_active_md(&content);
    let count = ctx
        .task_storage
        .backfill_from_tasks(parsed, repo_path)
        .await?;
    let _ = queue_serializer::flush_queue(&ctx.task_storage, repo_path).await;
    Ok(json!({ "synced": count }))
}

// ─── Phase 43b: Task State Engine RPC handlers ───────────────────────────────

/// `tasks.createSpec` — Create a new task from a full TaskSpec.
///
/// Params:
/// ```json
/// {
///   "id": "uuid",          // Required: task ID
///   "title": "...",        // Required
///   "repo": "/abs/path",   // Required: absolute repo path
///   "summary": "...",      // Optional
///   "acceptance_criteria": [],  // Optional
///   "test_plan": "...",    // Optional
///   "risk_level": "low",   // Optional: low|medium|high|critical
///   "priority": "medium",  // Optional: low|medium|high|critical
///   "labels": [],          // Optional
///   "owner": "agent-id"    // Optional
/// }
/// ```
pub async fn create_from_spec(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = sv(&params, "id")
        .map(String::from)
        .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());
    validate_task_id(&task_id)?;
    let title = sv(&params, "title").ok_or_else(|| anyhow::anyhow!("missing title"))?;
    let repo = sv(&params, "repo").ok_or_else(|| anyhow::anyhow!("missing repo"))?;

    let risk_level = match sv(&params, "risk_level").unwrap_or("medium") {
        "critical" => RiskLevel::Critical,
        "high" => RiskLevel::High,
        "low" => RiskLevel::Low,
        _ => RiskLevel::Medium,
    };

    let priority = match sv(&params, "priority").unwrap_or("medium") {
        "critical" => Priority::Critical,
        "high" => Priority::High,
        "low" => Priority::Low,
        _ => Priority::Medium,
    };

    let acceptance_criteria: Vec<String> = params
        .get("acceptance_criteria")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default();

    let labels: Vec<String> = params
        .get("labels")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default();

    let spec = TaskSpec {
        id: task_id.clone(),
        title: title.to_string(),
        repo: repo.to_string(),
        summary: s(&params, "summary"),
        acceptance_criteria,
        test_plan: s(&params, "test_plan"),
        risk_level,
        priority,
        labels,
        owner: s(&params, "owner"),
        worktree_path: s(&params, "worktree_path"),
        worktree_branch: s(&params, "worktree_branch"),
        created_at: Utc::now(),
    };

    // Write task.yaml into .claw/tasks/<id>/
    let task_dir = ctx.config.data_dir.join("tasks").join(&task_id);
    tokio::fs::create_dir_all(&task_dir).await?;
    let yaml = serde_norway::to_string(&spec)
        .map_err(|e| anyhow::anyhow!("spec serialization failed: {e}"))?;
    tokio::fs::write(task_dir.join("task.yaml"), yaml).await?;

    // Append TaskCreated event to the event log
    let engine = ReplayEngine::new(&task_id, &ctx.config.data_dir)?;
    let correlation_id = new_correlation_id();
    engine
        .event_log
        .append(
            TaskEventKind::TaskCreated { spec },
            "daemon",
            &correlation_id,
        )
        .await?;

    ctx.broadcaster
        .broadcast("task.specCreated", json!({ "task_id": task_id }));

    Ok(json!({ "task_id": task_id }))
}

/// `tasks.transition` — Transition a task to a new state via an event.
///
/// Params:
/// ```json
/// {
///   "task_id": "uuid",         // Required
///   "event_type": "task_active",  // Required: snake_case event name
///   "actor": "agent-id",       // Optional (default: "user")
///   // Additional fields depending on event_type:
///   "reason": "...",           // For task_blocked, task_canceled, task_failed
///   "completion_notes": "...", // For task_done
///   "agent_id": "...",         // For task_claimed
///   "role": "...",             // For task_claimed
///   "approval_id": "...",      // For task_needs_approval, approval_granted, etc.
///   "tool_name": "...",        // For task_needs_approval, approval events
///   "risk_level": "...",       // For task_needs_approval, approval_requested
///   "reviewer_id": "...",      // For task_code_review
///   "qa_agent_id": "...",      // For task_qa
///   "granted_by": "...",       // For approval_granted
///   "denied_by": "...",        // For approval_denied
/// }
/// ```
pub async fn transition(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = sv(&params, "task_id").ok_or_else(|| anyhow::anyhow!("missing task_id"))?;
    validate_task_id(task_id)?;
    let event_type =
        sv(&params, "event_type").ok_or_else(|| anyhow::anyhow!("missing event_type"))?;
    let actor = sv(&params, "actor").unwrap_or("user");

    let kind = match event_type {
        "task_active" => TaskEventKind::TaskActive,
        "task_planned" => TaskEventKind::TaskPlanned {
            phases: params
                .get("phases")
                .and_then(|v| v.as_array())
                .cloned()
                .unwrap_or_default(),
        },
        "task_claimed" => TaskEventKind::TaskClaimed {
            agent_id: sv(&params, "agent_id")
                .ok_or_else(|| anyhow::anyhow!("task_claimed requires agent_id"))?
                .to_string(),
            role: sv(&params, "role").unwrap_or("implementer").to_string(),
        },
        "task_blocked" => TaskEventKind::TaskBlocked {
            reason: sv(&params, "reason")
                .ok_or_else(|| anyhow::anyhow!("task_blocked requires reason"))?
                .to_string(),
            retry_after: None,
        },
        "task_needs_approval" => TaskEventKind::TaskNeedsApproval {
            approval_id: s(&params, "approval_id")
                .unwrap_or_else(|| uuid::Uuid::new_v4().to_string()),
            tool_name: sv(&params, "tool_name").unwrap_or("unknown").to_string(),
            risk_level: sv(&params, "risk_level").unwrap_or("high").to_string(),
        },
        "task_code_review" => TaskEventKind::TaskCodeReview {
            reviewer_id: s(&params, "reviewer_id"),
        },
        "task_qa" => TaskEventKind::TaskQa {
            qa_agent_id: s(&params, "qa_agent_id"),
        },
        "task_done" => TaskEventKind::TaskDone {
            completion_notes: sv(&params, "completion_notes")
                .ok_or_else(|| anyhow::anyhow!("task_done requires completion_notes"))?
                .to_string(),
        },
        "task_canceled" => TaskEventKind::TaskCanceled {
            reason: sv(&params, "reason").unwrap_or("user canceled").to_string(),
        },
        "task_failed" => TaskEventKind::TaskFailed {
            error: sv(&params, "reason")
                .or_else(|| sv(&params, "error"))
                .unwrap_or("unknown error")
                .to_string(),
        },
        "approval_granted" => TaskEventKind::ApprovalGranted {
            approval_id: sv(&params, "approval_id")
                .ok_or_else(|| anyhow::anyhow!("approval_granted requires approval_id"))?
                .to_string(),
            granted_by: sv(&params, "granted_by").unwrap_or("user").to_string(),
        },
        "approval_denied" => TaskEventKind::ApprovalDenied {
            approval_id: sv(&params, "approval_id")
                .ok_or_else(|| anyhow::anyhow!("approval_denied requires approval_id"))?
                .to_string(),
            denied_by: sv(&params, "denied_by").unwrap_or("user").to_string(),
            reason: sv(&params, "reason").unwrap_or("denied").to_string(),
        },
        other => {
            return Err(anyhow::anyhow!("unknown event_type: {}", other));
        }
    };

    let engine = ReplayEngine::new(task_id, &ctx.config.data_dir)?;
    let current_state = engine.replay().await?;
    let (new_state, _seq) = engine.append_and_reduce(kind, actor, current_state).await?;

    ctx.broadcaster.broadcast(
        "task.stateChanged",
        json!({
            "task_id": task_id,
            "state": new_state.state,
        }),
    );

    Ok(json!({ "state": new_state.state, "task_id": task_id }))
}

/// `tasks.listEvents` — Query the JSONL event log for a task.
///
/// Params:
/// ```json
/// {
///   "task_id": "uuid",      // Required
///   "from_seq": 0,          // Optional: start after this seq (inclusive)
///   "limit": 100            // Optional: max events to return
/// }
/// ```
pub async fn list_events(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = sv(&params, "task_id").ok_or_else(|| anyhow::anyhow!("missing task_id"))?;
    validate_task_id(task_id)?;
    let from_seq = params.get("from_seq").and_then(|v| v.as_u64()).unwrap_or(0);
    let limit = params
        .get("limit")
        .and_then(|v| v.as_u64())
        .unwrap_or(200)
        .min(1000);

    let log = crate::tasks::event_log::TaskEventLog::new(task_id, &ctx.config.data_dir)?;
    let total = log.event_count().await?;
    let mut events = log.read_from(from_seq).await?;

    // Apply limit
    events.truncate(limit as usize);

    Ok(json!({
        "events": events,
        "total": total,
        "task_id": task_id,
        "from_seq": from_seq,
    }))
}

/// `tasks.progressEstimate` — V02.T17.
///
/// Returns avg time per task from history, % complete, and an ETA in minutes.
///
/// Params: `{ repo_path?: String }`
/// Returns: `{ done, pending, in_progress, total, pct_complete, avg_minutes_per_task, eta_minutes }`
pub async fn progress_estimate(params: Value, ctx: &AppContext) -> Result<Value> {
    let repo_path = sv(&params, "repo_path");
    let summary = ctx.task_storage.summary(repo_path).await?;

    let done = summary["done"].as_i64().unwrap_or(0);
    let total = summary["total"].as_i64().unwrap_or(0);
    let pending = summary["pending"].as_i64().unwrap_or(0);
    let in_progress = summary["in_progress"].as_i64().unwrap_or(0);
    let avg = summary["avg_duration_minutes"].as_f64();

    let pct_complete: f64 = if total > 0 {
        (done as f64 / total as f64 * 100.0).round()
    } else {
        0.0
    };

    let remaining = pending + in_progress;
    let eta_minutes: Option<f64> = avg.map(|a| (remaining as f64 * a).round());

    Ok(json!({
        "done": done,
        "pending": pending,
        "in_progress": in_progress,
        "total": total,
        "pct_complete": pct_complete,
        "avg_minutes_per_task": avg,
        "eta_minutes": eta_minutes,
    }))
}

// ── Sprint CC TG.2 — Task Genealogy RPCs ─────────────────────────────────────

/// `task.spawn` — create a child task with a genealogy link to its parent.
pub async fn spawn(params: Value, ctx: &AppContext) -> Result<Value> {
    let parent_id = s(&params, "parentId").ok_or_else(|| anyhow!("missing parentId"))?;
    let title = s(&params, "title").ok_or_else(|| anyhow!("missing title"))?;
    let description = s(&params, "description");
    let relationship = s(&params, "relationship").unwrap_or_else(|| "spawned_from".into());

    validate_task_id(&parent_id)?;

    // Verify parent exists.
    ctx.task_storage
        .get_task(&parent_id)
        .await?
        .ok_or_else(|| anyhow!("parent task not found"))?;

    let child_id = format!("task-{}", uuid::Uuid::new_v4());
    let full_title = match &description {
        Some(d) => format!("{title}: {d}"),
        None => title.clone(),
    };

    ctx.task_storage
        .add_task(
            &child_id,
            &full_title,
            Some("spawned"),
            None,
            None,
            Some(&parent_id),
            None,
            None,
            None,
            None,
            None,
            None,
            "",
        )
        .await?;

    // Insert genealogy record.
    sqlx::query(
        "INSERT OR IGNORE INTO task_genealogy (parent_task_id, child_task_id, relationship)
         VALUES (?, ?, ?)",
    )
    .bind(&parent_id)
    .bind(&child_id)
    .bind(&relationship)
    .execute(ctx.task_storage.pool())
    .await?;

    Ok(json!({
        "childId": child_id,
        "parentId": parent_id,
        "relationship": relationship,
        "title": full_title,
    }))
}

/// `task.lineage` — return full ancestor + descendant tree for a task.
pub async fn lineage(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = s(&params, "taskId").ok_or_else(|| anyhow!("missing taskId"))?;
    validate_task_id(&task_id)?;

    // Ancestors (this task is a child).
    let ancestors = sqlx::query(
        "SELECT g.parent_task_id, g.relationship, t.title
         FROM task_genealogy g
         JOIN agent_tasks t ON t.id = g.parent_task_id
         WHERE g.child_task_id = ?",
    )
    .bind(&task_id)
    .fetch_all(ctx.task_storage.pool())
    .await?
    .into_iter()
    .map(|row| {
        json!({
            "taskId": row.get::<String, _>("parent_task_id"),
            "title":  row.get::<Option<String>, _>("title"),
            "relationship": row.get::<String, _>("relationship"),
        })
    })
    .collect::<Vec<_>>();

    // Descendants (this task is a parent).
    let descendants = sqlx::query(
        "SELECT g.child_task_id, g.relationship, t.title
         FROM task_genealogy g
         JOIN agent_tasks t ON t.id = g.child_task_id
         WHERE g.parent_task_id = ?",
    )
    .bind(&task_id)
    .fetch_all(ctx.task_storage.pool())
    .await?
    .into_iter()
    .map(|row| {
        json!({
            "taskId": row.get::<String, _>("child_task_id"),
            "title":  row.get::<Option<String>, _>("title"),
            "relationship": row.get::<String, _>("relationship"),
        })
    })
    .collect::<Vec<_>>();

    Ok(json!({
        "taskId": task_id,
        "ancestors": ancestors,
        "descendants": descendants,
    }))
}

// ── Stub gate helpers ─────────────────────────────────────────────────────────

/// Walk up from the first modified file's parent directories to find one that
/// contains a `.claude/` subdirectory. Falls back to the first file's parent.
fn infer_project_root(files: &[std::path::PathBuf]) -> Option<std::path::PathBuf> {
    let first = files.first()?;
    let mut dir = first.parent()?;
    loop {
        if dir.join(".claude").exists() {
            return Some(dir.to_path_buf());
        }
        match dir.parent() {
            Some(p) if p != dir => dir = p,
            _ => break,
        }
    }
    // Fallback: directory of the first file.
    first.parent().map(|p| p.to_path_buf())
}
