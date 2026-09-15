use anyhow::{anyhow, Result};
use serde::{Deserialize, Serialize};
use sqlx::SqlitePool;
use std::time::{SystemTime, UNIX_EPOCH};

/// Default timeout for individual SQLite queries (same as storage/mod.rs).
const QUERY_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(30);

/// Execute a future with the standard query timeout.
async fn with_timeout<T>(fut: impl std::future::Future<Output = Result<T>>) -> Result<T> {
    match tokio::time::timeout(QUERY_TIMEOUT, fut).await {
        Ok(result) => result,
        Err(_) => Err(anyhow!(
            "database query timed out after {}s",
            QUERY_TIMEOUT.as_secs()
        )),
    }
}

// ─── Error codes ─────────────────────────────────────────────────────────────

pub const TASK_NOT_FOUND: i32 = -32010;
pub const TASK_ALREADY_CLAIMED: i32 = -32011;
pub const TASK_ALREADY_DONE: i32 = -32012;
pub const AGENT_NOT_FOUND: i32 = -32013;
pub const MISSING_COMPLETION_NOTES: i32 = -32014;
pub const TASK_NOT_RESUMABLE: i32 = -32015;

// ─── Row types ────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, sqlx::FromRow, Serialize, Deserialize)]
pub struct AgentTaskRow {
    pub id: String,
    pub title: String,
    #[serde(rename = "type")]
    #[sqlx(rename = "type")]
    pub task_type: Option<String>,
    pub phase: Option<String>,
    pub group: Option<String>,
    pub parent_id: Option<String>,
    pub severity: Option<String>,
    pub status: String,
    pub claimed_by: Option<String>,
    pub claimed_at: Option<i64>,
    pub started_at: Option<i64>,
    pub completed_at: Option<i64>,
    pub last_heartbeat: Option<i64>,
    pub file: Option<String>,
    pub files: Option<String>,
    pub depends_on: Option<String>,
    pub blocks: Option<String>,
    pub tags: Option<String>,
    pub notes: Option<String>,
    pub block_reason: Option<String>,
    pub estimated_minutes: Option<i64>,
    pub actual_minutes: Option<i64>,
    pub repo_path: String,
    pub created_at: i64,
    pub updated_at: i64,
}

#[derive(Debug, Clone, sqlx::FromRow, Serialize, Deserialize)]
pub struct ActivityLogRow {
    pub id: String,
    pub ts: i64,
    pub agent: String,
    pub task_id: Option<String>,
    pub phase: Option<String>,
    pub action: String,
    pub entry_type: String,
    pub detail: Option<String>,
    pub meta: Option<String>,
    pub repo_path: String,
}

#[derive(Debug, Clone, sqlx::FromRow, Serialize, Deserialize)]
pub struct AgentRegistryRow {
    pub agent_id: String,
    pub agent_type: String,
    pub session_id: Option<String>,
    pub status: String,
    pub current_task_id: Option<String>,
    pub connected_at: i64,
    pub last_seen: i64,
    pub repo_path: String,
}

#[derive(Debug, Clone, sqlx::FromRow, Serialize, Deserialize)]
pub struct WorkSessionRow {
    pub id: String,
    pub started_at: i64,
    pub ended_at: Option<i64>,
    pub tasks_completed: i64,
    pub tasks_created: i64,
    pub agents_active: String,
    pub repo_path: String,
}

// ─── Query params ─────────────────────────────────────────────────────────────

#[derive(Debug, Default, Deserialize)]
pub struct TaskListParams {
    pub repo_path: Option<String>,
    pub status: Option<String>,
    pub agent: Option<String>,
    pub severity: Option<String>,
    pub phase: Option<String>,
    pub tag: Option<String>,
    pub search: Option<String>,
    pub limit: Option<i64>,
    pub offset: Option<i64>,
}

#[derive(Debug, Default, Deserialize)]
pub struct ActivityQueryParams {
    pub repo_path: Option<String>,
    pub task_id: Option<String>,
    pub agent: Option<String>,
    pub phase: Option<String>,
    pub entry_type: Option<String>,
    pub action: Option<String>,
    pub since: Option<i64>,
    pub limit: Option<i64>,
    pub offset: Option<i64>,
}

// ─── TaskStorage ──────────────────────────────────────────────────────────────

#[derive(Clone)]
pub struct TaskStorage {
    pool: SqlitePool,
}

fn now_ts() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}

impl TaskStorage {
    pub fn new(pool: SqlitePool) -> Self {
        Self { pool }
    }

    /// Expose the pool for direct queries (genealogy, confidence, etc.).
    pub fn pool(&self) -> &SqlitePool {
        &self.pool
    }

    // ─── Tasks ────────────────────────────────────────────────────────────────

    pub async fn list_tasks(&self, params: &TaskListParams) -> Result<Vec<AgentTaskRow>> {
        let limit = params.limit.unwrap_or(200).min(500) as usize;
        let offset = params.offset.unwrap_or(0) as usize;
        let pool = self.pool.clone();

        // Fetch all rows matching the ordering, then apply post-filters and
        // LIMIT/OFFSET in Rust so filters run before the row limit is enforced.
        let mut rows: Vec<AgentTaskRow> = with_timeout(async {
            Ok(sqlx::query_as(
                "SELECT * FROM agent_tasks ORDER BY
                 CASE severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,
                 updated_at DESC"
            )
            .fetch_all(&pool)
            .await?)
        })
        .await?;

        // Post-filter (SQLite has limited dynamic WHERE support without a query builder)
        if let Some(ref repo) = params.repo_path {
            rows.retain(|r| &r.repo_path == repo);
        }
        if let Some(ref status) = params.status {
            rows.retain(|r| &r.status == status);
        }
        if let Some(ref agent) = params.agent {
            rows.retain(|r| r.claimed_by.as_deref() == Some(agent.as_str()));
        }
        if let Some(ref sev) = params.severity {
            rows.retain(|r| r.severity.as_deref() == Some(sev.as_str()));
        }
        if let Some(ref phase) = params.phase {
            rows.retain(|r| r.phase.as_deref() == Some(phase.as_str()));
        }
        if let Some(ref search) = params.search {
            let q = search.to_lowercase();
            rows.retain(|r| {
                r.title.to_lowercase().contains(&q) || r.id.to_lowercase().contains(&q)
            });
        }
        if let Some(ref tag) = params.tag {
            rows.retain(|r| {
                // Parse the JSON array to avoid substring false-positives (e.g.
                // tag "foo" matching tags JSON containing "foobar").
                let tags_json = r.tags.as_deref().unwrap_or("[]");
                serde_json::from_str::<Vec<String>>(tags_json)
                    .map(|tags| tags.iter().any(|t| t == tag))
                    .unwrap_or(false)
            });
        }

        // Apply offset and limit after all in-process filters.
        if offset > 0 {
            rows = rows.into_iter().skip(offset).collect();
        }
        rows.truncate(limit);

        Ok(rows)
    }

    pub async fn get_task(&self, id: &str) -> Result<Option<AgentTaskRow>> {
        Ok(sqlx::query_as("SELECT * FROM agent_tasks WHERE id = ?")
            .bind(id)
            .fetch_optional(&self.pool)
            .await?)
    }

    #[allow(clippy::too_many_arguments)]
    pub async fn add_task(
        &self,
        id: &str,
        title: &str,
        task_type: Option<&str>,
        phase: Option<&str>,
        group: Option<&str>,
        parent_id: Option<&str>,
        severity: Option<&str>,
        file: Option<&str>,
        files: Option<&str>,
        depends_on: Option<&str>,
        tags: Option<&str>,
        estimated_minutes: Option<i64>,
        repo_path: &str,
    ) -> Result<AgentTaskRow> {
        let now = now_ts();
        sqlx::query(
            "INSERT INTO agent_tasks
             (id, title, type, phase, \"group\", parent_id, severity, file, files, depends_on, tags,
              estimated_minutes, repo_path, created_at, updated_at)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(id)
        .bind(title)
        .bind(task_type.unwrap_or("code"))
        .bind(phase)
        .bind(group)
        .bind(parent_id)
        .bind(severity.unwrap_or("medium"))
        .bind(file)
        .bind(files.unwrap_or("[]"))
        .bind(depends_on.unwrap_or("[]"))
        .bind(tags.unwrap_or("[]"))
        .bind(estimated_minutes)
        .bind(repo_path)
        .bind(now)
        .bind(now)
        .execute(&self.pool)
        .await?;

        self.get_task(id)
            .await?
            .ok_or_else(|| anyhow!("task not found after insert"))
    }

    /// Atomic claim — single UPDATE that only touches unclaimed/pending rows.
    /// Returns Ok(task) if claimed, Err with TASK_ALREADY_CLAIMED code if someone beat us.
    ///
    /// For interrupted tasks, enforces a re-claim window: the task must have been
    /// interrupted within `heartbeat_timeout_secs` (default 90s) to be re-claimed.
    /// Tasks interrupted longer ago are treated as stale and must be explicitly
    /// reset to pending before being claimed.
    pub async fn claim_task(
        &self,
        task_id: &str,
        agent_id: &str,
        heartbeat_timeout_secs: Option<i64>,
    ) -> Result<AgentTaskRow> {
        let now = now_ts();
        let reclaim_window = heartbeat_timeout_secs.unwrap_or(90);

        // For interrupted tasks, verify they are within the re-claim window.
        // This prevents stale interrupted tasks from being silently re-claimed
        // long after the original agent disappeared.
        if let Some(task) = self.get_task(task_id).await? {
            if task.status == "interrupted" {
                if let Some(last_hb) = task.last_heartbeat {
                    let elapsed = now - last_hb;
                    if elapsed > reclaim_window * 2 {
                        // Task has been interrupted for too long — reject re-claim.
                        // The caller should reset it to pending first.
                        return Err(anyhow!(
                            "TASK_CODE:{} — interrupted task exceeded re-claim window ({}s elapsed, {}s max)",
                            TASK_NOT_RESUMABLE,
                            elapsed,
                            reclaim_window * 2
                        ));
                    }
                }
            }
        }

        // Allow re-claim of interrupted tasks by any agent.
        // LH.T04: Also allow claiming tasks with expired leases (lease_expires_at < now).
        let rows_affected = sqlx::query(
            "UPDATE agent_tasks
             SET status = 'in_progress',
                 claimed_by = ?,
                 claimed_at = ?,
                 started_at = COALESCE(started_at, ?),
                 last_heartbeat = ?,
                 updated_at = ?
             WHERE id = ?
               AND (
                   (status = 'pending' OR status = 'interrupted')
                   AND (claimed_by IS NULL OR status = 'interrupted')
                   OR
                   (status = 'claimed' AND lease_expires_at IS NOT NULL AND lease_expires_at < ?)
               )",
        )
        .bind(agent_id)
        .bind(now)
        .bind(now)
        .bind(now)
        .bind(now)
        .bind(task_id)
        .bind(now) // LH.T04: lease_expires_at < now check
        .execute(&self.pool)
        .await?
        .rows_affected();

        if rows_affected == 0 {
            return Err(anyhow!("TASK_CODE:{}", TASK_ALREADY_CLAIMED));
        }

        // FO.T05 — Conflict detection at claim time: warn if owned_paths overlap
        // with any other currently-active task.
        let new_paths_json: Option<String> =
            sqlx::query_scalar("SELECT owned_paths_json FROM agent_tasks WHERE id = ?")
                .bind(task_id)
                .fetch_optional(&self.pool)
                .await
                .unwrap_or(None)
                .flatten();

        if let Some(ref new_paths) = new_paths_json {
            if new_paths != "[]" && new_paths != "null" {
                let active_tasks: Vec<(String, String)> = sqlx::query_as(
                    "SELECT id, owned_paths_json FROM agent_tasks \
                     WHERE status IN ('claimed', 'in_progress', 'active') \
                       AND id != ? AND owned_paths_json IS NOT NULL",
                )
                .bind(task_id)
                .fetch_all(&self.pool)
                .await
                .unwrap_or_default();

                for (other_id, other_paths) in &active_tasks {
                    let overlaps =
                        crate::tasks::ownership::check_ownership_overlap(new_paths, other_paths);
                    if !overlaps.is_empty() {
                        tracing::warn!(
                            task_id,
                            other_task_id = %other_id,
                            conflicts = ?overlaps,
                            "FO.T05: owned-paths conflict at claim time"
                        );
                    }
                }
            }
        }

        self.get_task(task_id)
            .await?
            .ok_or_else(|| anyhow!("task not found after claim"))
    }

    pub async fn release_task(&self, task_id: &str, agent_id: &str) -> Result<()> {
        let now = now_ts();
        sqlx::query(
            "UPDATE agent_tasks
             SET status = 'pending', claimed_by = NULL, claimed_at = NULL, last_heartbeat = NULL, updated_at = ?
             WHERE id = ? AND claimed_by = ?"
        )
        .bind(now)
        .bind(task_id)
        .bind(agent_id)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    pub async fn heartbeat_task(&self, task_id: &str, agent_id: &str) -> Result<()> {
        let now = now_ts();
        sqlx::query(
            "UPDATE agent_tasks SET last_heartbeat = ?, updated_at = ?
             WHERE id = ? AND claimed_by = ?",
        )
        .bind(now)
        .bind(now)
        .bind(task_id)
        .bind(agent_id)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    /// Update task status. Enforces non-empty notes when transitioning to 'done'.
    pub async fn update_status(
        &self,
        task_id: &str,
        new_status: &str,
        notes: Option<&str>,
        block_reason: Option<&str>,
    ) -> Result<AgentTaskRow> {
        // Enforce completion notes
        if new_status == "done" {
            match notes {
                None | Some("") => {
                    return Err(anyhow!("TASK_CODE:{}", MISSING_COMPLETION_NOTES));
                }
                _ => {}
            }
        }

        let now = now_ts();
        let completed_at = if new_status == "done" {
            Some(now)
        } else {
            None
        };

        // Compute actual_minutes when completing
        let task = self
            .get_task(task_id)
            .await?
            .ok_or_else(|| anyhow!("TASK_CODE:{}", TASK_NOT_FOUND))?;
        let actual_minutes = if new_status == "done" {
            task.started_at.map(|s| (now - s) / 60)
        } else {
            task.actual_minutes
        };

        sqlx::query(
            "UPDATE agent_tasks
             SET status = ?, notes = COALESCE(?, notes), block_reason = ?,
                 completed_at = COALESCE(?, completed_at),
                 actual_minutes = COALESCE(?, actual_minutes),
                 updated_at = ?
             WHERE id = ?",
        )
        .bind(new_status)
        .bind(notes)
        .bind(block_reason)
        .bind(completed_at)
        .bind(actual_minutes)
        .bind(now)
        .bind(task_id)
        .execute(&self.pool)
        .await?;

        self.get_task(task_id)
            .await?
            .ok_or_else(|| anyhow!("task not found after update"))
    }

    /// Mark stale in-progress tasks (heartbeat > timeout_secs) as interrupted.
    /// Returns list of task IDs that were interrupted.
    pub async fn interrupt_stale_tasks(&self, timeout_secs: i64) -> Result<Vec<String>> {
        let cutoff = now_ts() - timeout_secs;
        let now = now_ts();

        // Wrap SELECT+UPDATE in a transaction so no task can be claimed between
        // the read and the write (prevents TOCTOU between scheduler and agent).
        let mut tx = self.pool.begin().await?;

        let stale: Vec<(String,)> = sqlx::query_as(
            "SELECT id FROM agent_tasks
             WHERE status = 'in_progress'
               AND last_heartbeat IS NOT NULL
               AND last_heartbeat < ?",
        )
        .bind(cutoff)
        .fetch_all(&mut *tx)
        .await?;

        let ids: Vec<String> = stale.into_iter().map(|(id,)| id).collect();
        if ids.is_empty() {
            tx.commit().await?;
            return Ok(ids);
        }

        for id in &ids {
            sqlx::query(
                "UPDATE agent_tasks SET status = 'interrupted', updated_at = ? WHERE id = ?",
            )
            .bind(now)
            .bind(id)
            .execute(&mut *tx)
            .await?;
        }

        tx.commit().await?;
        Ok(ids)
    }

    /// Archive done tasks older than `visible_hours`. Interrupted tasks are NEVER archived.
    pub async fn archive_done_tasks(&self, visible_hours: i64) -> Result<usize> {
        let cutoff = now_ts() - visible_hours * 3600;
        let done_old: Vec<AgentTaskRow> =
            sqlx::query_as("SELECT * FROM agent_tasks WHERE status = 'done' AND completed_at < ?")
                .bind(cutoff)
                .fetch_all(&self.pool)
                .await?;

        let count = done_old.len();
        if count == 0 {
            return Ok(0);
        }

        // Wrap all INSERT+DELETE pairs in one transaction so the archive is
        // all-or-nothing — a partial failure won't leave tasks in limbo.
        let mut tx = self.pool.begin().await?;

        for task in &done_old {
            sqlx::query(
                "INSERT OR IGNORE INTO agent_tasks_archive
                 (id, title, type, phase, \"group\", severity, status, claimed_by, claimed_at,
                  completed_at, actual_minutes, repo_path, archived_at)
                 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, unixepoch())",
            )
            .bind(&task.id)
            .bind(&task.title)
            .bind(&task.task_type)
            .bind(&task.phase)
            .bind(&task.group)
            .bind(&task.severity)
            .bind(&task.status)
            .bind(&task.claimed_by)
            .bind(task.claimed_at)
            .bind(task.completed_at)
            .bind(task.actual_minutes)
            .bind(&task.repo_path)
            .execute(&mut *tx)
            .await?;

            sqlx::query("DELETE FROM agent_tasks WHERE id = ?")
                .bind(&task.id)
                .execute(&mut *tx)
                .await?;
        }

        tx.commit().await?;
        Ok(count)
    }

    /// Backfill from active.md if queue.json missing but tasks.md exists.
    pub async fn backfill_from_tasks(
        &self,
        tasks: Vec<super::markdown_parser::ParsedTask>,
        repo_path: &str,
    ) -> Result<usize> {
        let mut count = 0;
        for t in tasks {
            // Only insert if not already present
            let existing = self.get_task(&t.id).await?;
            if let Some(ref ex) = existing {
                // Task already exists — sync status if it differs.
                if ex.status != t.status {
                    sqlx::query(
                        "UPDATE agent_tasks SET status = ?, updated_at = unixepoch() WHERE id = ?",
                    )
                    .bind(&t.status)
                    .bind(&t.id)
                    .execute(&self.pool)
                    .await?;
                }
                continue;
            }
            self.add_task(
                &t.id,
                &t.title,
                Some("code"),
                t.phase.as_deref(),
                t.group.as_deref(),
                None,
                Some(t.severity.as_deref().unwrap_or("medium")),
                t.file.as_deref(),
                None,
                None,
                None,
                None,
                repo_path,
            )
            .await?;
            // Update status to match active.md
            if t.status != "pending" {
                sqlx::query(
                    "UPDATE agent_tasks SET status = ?, updated_at = unixepoch() WHERE id = ?",
                )
                .bind(&t.status)
                .bind(&t.id)
                .execute(&self.pool)
                .await?;
            }
            count += 1;
        }
        Ok(count)
    }

    // ─── Activity log ─────────────────────────────────────────────────────────

    #[allow(clippy::too_many_arguments)]
    pub async fn log_activity(
        &self,
        agent: &str,
        task_id: Option<&str>,
        phase: Option<&str>,
        action: &str,
        entry_type: &str,
        detail: Option<&str>,
        meta: Option<&str>,
        repo_path: &str,
    ) -> Result<ActivityLogRow> {
        let id = format!("{:x}", uuid_v4_hex());
        let now = now_ts();
        sqlx::query(
            "INSERT INTO agent_activity_log
             (id, ts, agent, task_id, phase, action, entry_type, detail, meta, repo_path)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(&id)
        .bind(now)
        .bind(agent)
        .bind(task_id)
        .bind(phase)
        .bind(action)
        .bind(entry_type)
        .bind(detail)
        .bind(meta)
        .bind(repo_path)
        .execute(&self.pool)
        .await?;

        Ok(
            sqlx::query_as("SELECT * FROM agent_activity_log WHERE id = ?")
                .bind(&id)
                .fetch_one(&self.pool)
                .await?,
        )
    }

    /// Shorthand: post an agent note (entry_type = "note").
    pub async fn post_note(
        &self,
        agent: &str,
        task_id: Option<&str>,
        phase: Option<&str>,
        note: &str,
        repo_path: &str,
    ) -> Result<ActivityLogRow> {
        let action = if task_id.is_some() {
            "agent_note"
        } else {
            "phase_note"
        };
        self.log_activity(
            agent,
            task_id,
            phase,
            action,
            "note",
            Some(note),
            None,
            repo_path,
        )
        .await
    }

    pub async fn query_activity(
        &self,
        params: &ActivityQueryParams,
    ) -> Result<Vec<ActivityLogRow>> {
        let limit = params.limit.unwrap_or(100).min(500);
        let offset = params.offset.unwrap_or(0);

        // Phase query: aggregate all entries where task.phase = X plus phase-level notes
        if let Some(ref phase) = params.phase {
            let rows: Vec<ActivityLogRow> = sqlx::query_as(
                "SELECT a.* FROM agent_activity_log a
                 LEFT JOIN agent_tasks t ON t.id = a.task_id
                 WHERE (t.phase = ? OR (a.phase = ? AND a.task_id IS NULL))
                   AND (? IS NULL OR a.repo_path = ?)
                 ORDER BY a.ts DESC
                 LIMIT ? OFFSET ?",
            )
            .bind(phase)
            .bind(phase)
            .bind(params.repo_path.as_deref())
            .bind(params.repo_path.as_deref())
            .bind(limit)
            .bind(offset)
            .fetch_all(&self.pool)
            .await?;
            return Ok(rows);
        }

        let mut rows: Vec<ActivityLogRow> =
            sqlx::query_as("SELECT * FROM agent_activity_log ORDER BY ts DESC LIMIT ? OFFSET ?")
                .bind(limit)
                .bind(offset)
                .fetch_all(&self.pool)
                .await?;

        if let Some(ref task_id) = params.task_id {
            rows.retain(|r| r.task_id.as_deref() == Some(task_id.as_str()));
        }
        if let Some(ref agent) = params.agent {
            rows.retain(|r| &r.agent == agent);
        }
        if let Some(ref et) = params.entry_type {
            rows.retain(|r| &r.entry_type == et);
        }
        if let Some(ref action) = params.action {
            rows.retain(|r| &r.action == action);
        }
        if let Some(since) = params.since {
            rows.retain(|r| r.ts >= since);
        }
        if let Some(ref repo) = params.repo_path {
            rows.retain(|r| &r.repo_path == repo);
        }

        Ok(rows)
    }

    pub async fn prune_activity_log(&self, retention_days: i64) -> Result<u64> {
        let cutoff = now_ts() - retention_days * 86400;
        let result = sqlx::query("DELETE FROM agent_activity_log WHERE ts < ?")
            .bind(cutoff)
            .execute(&self.pool)
            .await?;
        Ok(result.rows_affected())
    }

    // ─── Agent registry ───────────────────────────────────────────────────────

    pub async fn register_agent(
        &self,
        agent_id: &str,
        agent_type: &str,
        session_id: Option<&str>,
        repo_path: &str,
    ) -> Result<AgentRegistryRow> {
        let now = now_ts();
        sqlx::query(
            "INSERT INTO agent_registry (agent_id, agent_type, session_id, repo_path, connected_at, last_seen)
             VALUES (?, ?, ?, ?, ?, ?)
             ON CONFLICT(agent_id) DO UPDATE SET
               status = 'idle', session_id = excluded.session_id,
               last_seen = excluded.last_seen"
        )
        .bind(agent_id)
        .bind(agent_type)
        .bind(session_id)
        .bind(repo_path)
        .bind(now)
        .bind(now)
        .execute(&self.pool)
        .await?;

        self.get_agent(agent_id)
            .await?
            .ok_or_else(|| anyhow!("agent not found after register"))
    }

    pub async fn get_agent(&self, agent_id: &str) -> Result<Option<AgentRegistryRow>> {
        Ok(
            sqlx::query_as("SELECT * FROM agent_registry WHERE agent_id = ?")
                .bind(agent_id)
                .fetch_optional(&self.pool)
                .await?,
        )
    }

    pub async fn list_agents(&self, repo_path: Option<&str>) -> Result<Vec<AgentRegistryRow>> {
        let rows: Vec<AgentRegistryRow> = sqlx::query_as(
            "SELECT * FROM agent_registry WHERE status != 'disconnected' ORDER BY last_seen DESC",
        )
        .fetch_all(&self.pool)
        .await?;

        Ok(rows
            .into_iter()
            .filter(|r| repo_path.map(|p| r.repo_path == p).unwrap_or(true))
            .collect())
    }

    pub async fn update_agent_heartbeat(&self, agent_id: &str) -> Result<()> {
        let now = now_ts();
        sqlx::query(
            "UPDATE agent_registry SET last_seen = ?, status = 'active' WHERE agent_id = ?",
        )
        .bind(now)
        .bind(agent_id)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    pub async fn set_agent_current_task(
        &self,
        agent_id: &str,
        task_id: Option<&str>,
    ) -> Result<()> {
        let now = now_ts();
        let status = if task_id.is_some() { "active" } else { "idle" };
        sqlx::query(
            "UPDATE agent_registry SET current_task_id = ?, status = ?, last_seen = ? WHERE agent_id = ?"
        )
        .bind(task_id)
        .bind(status)
        .bind(now)
        .bind(agent_id)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    pub async fn mark_agent_disconnected(&self, agent_id: &str) -> Result<()> {
        let now = now_ts();
        sqlx::query(
            "UPDATE agent_registry SET status = 'disconnected', current_task_id = NULL, last_seen = ? WHERE agent_id = ?"
        )
        .bind(now)
        .bind(agent_id)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    // ─── Work sessions ────────────────────────────────────────────────────────

    pub async fn open_work_session(&self, repo_path: &str) -> Result<WorkSessionRow> {
        let id = format!("{:x}", uuid_v4_hex());
        let now = now_ts();
        sqlx::query("INSERT INTO work_sessions (id, started_at, repo_path) VALUES (?, ?, ?)")
            .bind(&id)
            .bind(now)
            .bind(repo_path)
            .execute(&self.pool)
            .await?;

        Ok(sqlx::query_as("SELECT * FROM work_sessions WHERE id = ?")
            .bind(&id)
            .fetch_one(&self.pool)
            .await?)
    }

    pub async fn close_work_session(
        &self,
        session_id: &str,
        tasks_completed: i64,
        tasks_created: i64,
    ) -> Result<()> {
        let now = now_ts();
        sqlx::query(
            "UPDATE work_sessions SET ended_at = ?, tasks_completed = ?, tasks_created = ? WHERE id = ?"
        )
        .bind(now)
        .bind(tasks_completed)
        .bind(tasks_created)
        .bind(session_id)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    // ─── Summary ──────────────────────────────────────────────────────────────

    pub async fn summary(&self, repo_path: Option<&str>) -> Result<serde_json::Value> {
        let total: (i64,) =
            sqlx::query_as("SELECT COUNT(*) FROM agent_tasks WHERE (? IS NULL OR repo_path = ?)")
                .bind(repo_path)
                .bind(repo_path)
                .fetch_one(&self.pool)
                .await?;

        let done: (i64,) = sqlx::query_as(
            "SELECT COUNT(*) FROM agent_tasks WHERE status = 'done' AND (? IS NULL OR repo_path = ?)"
        )
        .bind(repo_path)
        .bind(repo_path)
        .fetch_one(&self.pool)
        .await?;

        let in_progress: (i64,) = sqlx::query_as(
            "SELECT COUNT(*) FROM agent_tasks WHERE status = 'in_progress' AND (? IS NULL OR repo_path = ?)"
        )
        .bind(repo_path)
        .bind(repo_path)
        .fetch_one(&self.pool)
        .await?;

        let blocked: (i64,) = sqlx::query_as(
            "SELECT COUNT(*) FROM agent_tasks WHERE status IN ('blocked','interrupted') AND (? IS NULL OR repo_path = ?)"
        )
        .bind(repo_path)
        .bind(repo_path)
        .fetch_one(&self.pool)
        .await?;

        let avg_mins: (Option<f64>,) = sqlx::query_as(
            "SELECT AVG(actual_minutes) FROM agent_tasks WHERE status = 'done' AND actual_minutes IS NOT NULL AND (? IS NULL OR repo_path = ?)"
        )
        .bind(repo_path)
        .bind(repo_path)
        .fetch_one(&self.pool)
        .await?;

        Ok(serde_json::json!({
            "total": total.0,
            "done": done.0,
            "in_progress": in_progress.0,
            "blocked": blocked.0,
            "pending": total.0 - done.0 - in_progress.0 - blocked.0,
            "avg_duration_minutes": avg_mins.0
        }))
    }
}

// Unique hex ID — generates a proper 128-bit random value using the
// OS CSPRNG (via rand_core/getrandom) for collision resistance.
fn uuid_v4_hex() -> u128 {
    use rand_core::{OsRng, RngCore};
    let mut bytes = [0u8; 16];
    OsRng.fill_bytes(&mut bytes);
    u128::from_le_bytes(bytes)
}

// Kept in its own file under storage/: the tests are a self-contained block.
#[cfg(test)]
mod tests;
