use anyhow::Result;
use std::path::Path;

use super::event_log::TaskEventLog;
use super::events::{new_correlation_id, TaskEventKind};
use super::schema::{Priority, RiskLevel, TaskSpec};
use super::storage::TaskStorage;

/// Migrate existing markdown/SQLite-based tasks to the .claw/ YAML spec format.
///
/// For each task in the DB that does not yet have a `task.yaml` in
/// `.claw/tasks/<id>/`, this function:
/// 1. Creates the task directory
/// 2. Writes a `task.yaml` with the task spec derived from the DB row
/// 3. Appends a `TaskCreated` event to the event log so replay works
///
/// This function is idempotent — it skips tasks that already have a spec file.
pub async fn migrate_tasks_to_claw(storage: &TaskStorage, data_dir: &Path) -> Result<()> {
    let params = super::storage::TaskListParams::default();
    let tasks = storage.list_tasks(&params).await?;

    let mut migrated = 0usize;
    let mut skipped = 0usize;

    for task in tasks {
        let task_dir = data_dir.join("tasks").join(&task.id);
        let spec_path = task_dir.join("task.yaml");

        if spec_path.exists() {
            skipped += 1;
            continue;
        }

        tokio::fs::create_dir_all(&task_dir).await?;

        let risk_level = match task.severity.as_deref().unwrap_or("medium") {
            "critical" => RiskLevel::Critical,
            "high" => RiskLevel::High,
            "low" => RiskLevel::Low,
            _ => RiskLevel::Medium,
        };

        let priority = match task.severity.as_deref().unwrap_or("medium") {
            "critical" => Priority::Critical,
            "high" => Priority::High,
            "low" => Priority::Low,
            _ => Priority::Medium,
        };

        let created_at =
            chrono::DateTime::from_timestamp(task.created_at, 0).unwrap_or_else(chrono::Utc::now);

        let spec = TaskSpec {
            id: task.id.clone(),
            title: task.title.clone(),
            repo: task.repo_path.clone(),
            summary: task.notes.clone(),
            acceptance_criteria: vec![],
            test_plan: None,
            risk_level,
            priority,
            labels: vec![],
            owner: task.claimed_by.clone(),
            worktree_path: None,
            worktree_branch: None,
            created_at,
        };

        // Write task.yaml
        let yaml = serde_norway::to_string(&spec)
            .unwrap_or_else(|_| format!("# failed to serialize task {}", task.id));
        tokio::fs::write(&spec_path, yaml).await?;

        // Append initial TaskCreated event so the event log is bootstrapped
        let log = TaskEventLog::new(&task.id, data_dir)?;
        let event_count = log.event_count().await?;
        if event_count == 0 {
            log.append(
                TaskEventKind::TaskCreated { spec },
                "daemon/migrate",
                &new_correlation_id(),
            )
            .await?;
        }

        migrated += 1;
    }

    if migrated > 0 || skipped > 0 {
        tracing::info!(migrated, skipped, "migrate_tasks_to_claw complete");
    }

    Ok(())
}
