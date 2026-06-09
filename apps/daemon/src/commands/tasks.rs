//! `clawd tasks` command handler.
//!
//! Purpose: CLI surface for the clawd task tracker — list, claim, release, complete, log tasks.
//! Inputs:  `TasksAction` variant, optional data dir, quiet flag.
//! Outputs: Task tables, detail views, or silent confirmation depending on action.
//! Constraints: Async; opens a direct SQLite connection (no daemon required).

use crate::cli_args::TasksAction;
use anyhow::Result;
use clawd::{
    config::DaemonConfig,
    storage::Storage,
    tasks::{
        storage::{ActivityQueryParams, AgentTaskRow, TaskListParams},
        TaskStorage,
    },
};

/// Open the task DB for CLI commands (no server — just storage access).
///
/// Purpose: Construct a `TaskStorage` from the configured data directory.
/// Inputs:  Optional override data dir path.
/// Outputs: Ready `TaskStorage` connected to the daemon's SQLite database.
/// Constraints: Async; migrates the DB schema if needed.
pub async fn open_task_storage(data_dir: Option<std::path::PathBuf>) -> Result<TaskStorage> {
    let config = DaemonConfig::new(None, data_dir, Some("error".to_string()), None, None);
    let storage = Storage::new(&config.data_dir).await?;
    Ok(TaskStorage::new(storage.clone_pool()))
}

/// Resolve task ID from positional arg or --task flag.
///
/// Purpose: Accept task ID either as positional or named `--task` argument.
/// Inputs:  Optional positional `id`, optional `--task` value.
/// Outputs: The resolved task ID string.
/// Constraints: Returns error if neither is provided.
pub fn resolve_task_id(id: Option<String>, task: Option<String>) -> Result<String> {
    id.or(task)
        .ok_or_else(|| anyhow::anyhow!("task ID required (positional or --task)"))
}

pub async fn run_tasks(
    action: TasksAction,
    data_dir: Option<std::path::PathBuf>,
    quiet: bool,
) -> Result<()> {
    let ts = open_task_storage(data_dir).await?;

    match action {
        TasksAction::List {
            repo,
            status,
            phase,
            limit,
            json,
        } => {
            let tasks = ts
                .list_tasks(&TaskListParams {
                    repo_path: repo,
                    status,
                    phase,
                    limit: Some(limit),
                    ..Default::default()
                })
                .await?;
            if json {
                println!("{}", serde_json::to_string(&tasks)?);
            } else if tasks.is_empty() {
                println!("No tasks found.");
            } else {
                println!("{:<12} {:<10} {:<10} TITLE", "STATUS", "SEVERITY", "PHASE");
                println!("{}", "-".repeat(72));
                for t in &tasks {
                    println!(
                        "{:<12} {:<10} {:<10} {}",
                        t.status,
                        t.severity.as_deref().unwrap_or("-"),
                        t.phase.as_deref().unwrap_or("-"),
                        t.title
                    );
                }
                println!("\n{} task(s)", tasks.len());
            }
        }

        TasksAction::Get { id, task, .. } => {
            let task_id = resolve_task_id(id, task)?;
            match ts.get_task(&task_id).await? {
                None => {
                    eprintln!("Task not found: {task_id}");
                    std::process::exit(1);
                }
                Some(t) => print_task_detail(&t),
            }
        }

        TasksAction::Claim {
            id, task, agent, ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            let t = ts.claim_task(&task_id, &agent, None).await?;
            if !quiet {
                println!("Claimed: {} — {}", t.id, t.title);
                println!(
                    "Status: {} by {}",
                    t.status,
                    t.claimed_by.as_deref().unwrap_or("?")
                );
            }
        }

        TasksAction::Release { id, task, agent } => {
            let task_id = resolve_task_id(id, task)?;
            ts.release_task(&task_id, &agent).await?;
            if !quiet {
                println!("Released: {task_id}");
            }
        }

        TasksAction::Done {
            id,
            task,
            notes,
            agent: _,
            ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            let notes_text = notes.ok_or_else(|| anyhow::anyhow!("--notes required for done"))?;
            let t = ts
                .update_status(&task_id, "done", Some(&notes_text), None)
                .await?;
            if !quiet {
                println!("Done: {} — {}", t.id, t.title);
            }
        }

        TasksAction::Blocked {
            id, task, notes, ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            let t = ts
                .update_status(&task_id, "blocked", None, notes.as_deref())
                .await?;
            if !quiet {
                println!("Blocked: {} — {}", t.id, t.title);
            }
        }

        TasksAction::Heartbeat {
            id, task, agent, ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            ts.heartbeat_task(&task_id, &agent).await?;
            // Silent success — hook calls this fire-and-forget
        }

        TasksAction::Add {
            title,
            repo,
            phase,
            severity,
            file,
        } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let id = format!("{:x}", rand_u64());
            let t = ts
                .add_task(
                    &id,
                    &title,
                    None,
                    phase.as_deref(),
                    None,
                    None,
                    Some(&severity),
                    file.as_deref(),
                    None,
                    None,
                    None,
                    None,
                    repo_path,
                )
                .await?;
            if !quiet {
                println!("Added: {} — {}", t.id, t.title);
            }
        }

        TasksAction::Log {
            id,
            task,
            agent,
            action,
            detail,
            notes,
            entry_type,
            repo,
        } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let task_id = id.or(task);
            // Accept --detail or --notes as the detail field
            let detail_text = detail.or(notes);
            ts.log_activity(
                &agent,
                task_id.as_deref(),
                None,
                &action,
                &entry_type,
                detail_text.as_deref(),
                None,
                repo_path,
            )
            .await?;
            // Silent — called by PostToolUse hook fire-and-forget
        }

        TasksAction::Note {
            id,
            task,
            phase,
            text,
            note,
            agent,
            repo,
        } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let task_id = id.or(task);
            let note_text = text
                .or(note)
                .ok_or_else(|| anyhow::anyhow!("note text required (positional or --note)"))?;
            ts.post_note(
                &agent,
                task_id.as_deref(),
                phase.as_deref(),
                &note_text,
                repo_path,
            )
            .await?;
            if !quiet {
                println!("Note posted.");
            }
        }

        TasksAction::FromPlanning { file, repo } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let content = tokio::fs::read_to_string(&file)
                .await
                .map_err(|e| anyhow::anyhow!("Cannot read file {}: {e}", file.display()))?;
            let parsed = clawd::tasks::markdown_parser::parse_active_md(&content);
            if parsed.is_empty() {
                if !quiet {
                    println!("No tasks found in {}", file.display());
                }
            } else {
                let count = ts.backfill_from_tasks(parsed, repo_path).await?;
                if !quiet {
                    println!("Imported {count} new task(s) from {}", file.display());
                }
            }
        }

        TasksAction::Sync { repo, active_md } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let md_path = active_md.unwrap_or_else(|| {
                std::path::PathBuf::from(repo_path).join(".claude/tasks/active.md")
            });
            let content = tokio::fs::read_to_string(&md_path)
                .await
                .map_err(|e| anyhow::anyhow!("Cannot read {}: {e}", md_path.display()))?;
            let parsed = clawd::tasks::markdown_parser::parse_active_md(&content);
            let count = ts.backfill_from_tasks(parsed, repo_path).await?;
            clawd::tasks::queue_serializer::flush_queue(&ts, repo_path).await?;
            if !quiet {
                println!("Synced: {count} new task(s), queue.json updated.");
            }
        }

        TasksAction::Summary { repo, json } => {
            let summary = ts.summary(repo.as_deref()).await?;
            if json {
                println!("{}", serde_json::to_string_pretty(&summary)?);
            } else {
                let done = summary["done"].as_i64().unwrap_or(0);
                let in_progress = summary["in_progress"].as_i64().unwrap_or(0);
                let pending = summary["pending"].as_i64().unwrap_or(0);
                let blocked = summary["blocked"].as_i64().unwrap_or(0);
                let total = summary["total"].as_i64().unwrap_or(0);
                let avg = summary["avg_duration_minutes"].as_f64();
                let bar = "━".repeat(40);
                println!("Task Summary");
                println!("{bar}");
                if let Some(r) = &repo {
                    println!("Project:     {r}");
                }
                println!("Total:       {total}");
                println!("Done:        {done}");
                println!("In Progress: {in_progress}");
                println!("Pending:     {pending}");
                println!("Blocked:     {blocked}");
                if let Some(m) = avg {
                    println!("Avg time:    {m:.1}m per task");
                }
            }
        }

        TasksAction::Activity {
            repo,
            task,
            phase,
            limit,
        } => {
            let rows = ts
                .query_activity(&ActivityQueryParams {
                    repo_path: repo,
                    task_id: task,
                    phase,
                    limit: Some(limit),
                    ..Default::default()
                })
                .await?;
            if rows.is_empty() {
                println!("No activity found.");
            } else {
                for r in &rows {
                    let task_label = r.task_id.as_deref().unwrap_or("-");
                    println!(
                        "[{}] {} | {} | {} | {}",
                        r.ts,
                        r.agent,
                        r.action,
                        task_label,
                        r.detail.as_deref().unwrap_or("")
                    );
                }
            }
        }
    }

    Ok(())
}

/// Print a detailed view of a single task to stdout.
///
/// Purpose: Format all fields of an `AgentTaskRow` for human consumption.
/// Inputs:  A reference to the task row.
/// Outputs: Printed lines to stdout.
/// Constraints: Pure output — no I/O beyond stdout.
pub fn print_task_detail(t: &AgentTaskRow) {
    println!("ID:       {}", t.id);
    println!("Title:    {}", t.title);
    println!("Status:   {}", t.status);
    println!("Severity: {}", t.severity.as_deref().unwrap_or("-"));
    println!("Phase:    {}", t.phase.as_deref().unwrap_or("-"));
    println!("File:     {}", t.file.as_deref().unwrap_or("-"));
    if let Some(ref a) = t.claimed_by {
        println!("Claimed:  {a}");
    }
    if let Some(ref n) = t.notes {
        println!("Notes:    {n}");
    }
    if let Some(ref b) = t.block_reason {
        println!("Blocked:  {b}");
    }
    println!("Repo:     {}", t.repo_path);
    println!("Created:  {}", t.created_at);
}

/// Generate a simple non-cryptographic u64 ID from time + PID.
///
/// Purpose: Produce a unique-enough identifier for new tasks added via CLI.
/// Inputs:  Current system time and process ID (implicit).
/// Outputs: A `u64` suitable for hex-formatting as a task ID.
/// Constraints: Not suitable for security purposes; collisions possible under load.
fn rand_u64() -> u64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    let ns = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .subsec_nanos();
    let pid = std::process::id() as u64;
    // simple non-crypto ID
    (ns as u64).wrapping_mul(1_000_003).wrapping_add(pid)
}
