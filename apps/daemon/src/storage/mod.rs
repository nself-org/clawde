pub mod event_log;

use anyhow::{Context as _, Result};
use chrono::Utc;
use sqlx::{sqlite::SqliteConnectOptions, ConnectOptions, SqlitePool};
use std::{path::Path, str::FromStr};
use uuid::Uuid;

/// Default timeout for individual SQLite queries.
/// Prevents hung queries from blocking the daemon indefinitely.
const QUERY_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(30);

/// Execute a future with the standard query timeout.
/// Returns an error if the operation takes longer than `QUERY_TIMEOUT`.
async fn with_timeout<T>(fut: impl std::future::Future<Output = Result<T>>) -> Result<T> {
    match tokio::time::timeout(QUERY_TIMEOUT, fut).await {
        Ok(result) => result,
        Err(_) => Err(anyhow::anyhow!(
            "database query timed out after {}s",
            QUERY_TIMEOUT.as_secs()
        )),
    }
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct SessionRow {
    pub id: String,
    pub provider: String,
    pub repo_path: String,
    pub title: String,
    pub status: String,
    pub created_at: String,
    pub updated_at: String,
    pub message_count: i64,
    /// JSON array of permission scopes, e.g. `["file_read","file_write","shell_exec","git"]`.
    /// NULL means all permissions granted (default).
    pub permissions: Option<String>,
    pub tier: String,
    pub tier_changed_at: i64,
    pub last_user_interaction_at: i64,
    pub pid: Option<i64>,
    /// Provider that was auto-selected when `provider = 'auto'` was requested.
    /// NULL when the provider was explicitly specified.
    pub routed_provider: Option<String>,
    /// GCI mode for this session: NORMAL | LEARN | STORM | FORGE | CRUNCH
    pub mode: String,
    /// Explicit model override set by the user via session.setModel.
    /// NULL = auto-route; non-NULL bypasses the classifier.
    pub model_override: Option<String>,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct MessageRow {
    pub id: String,
    pub session_id: String,
    pub role: String,
    pub content: String,
    pub status: String,
    pub created_at: String,
    /// Heuristic token count (ceil(len/4)). Zero for legacy rows. (SI.T01)
    pub token_count: i64,
    /// Pinned messages are always included in context window. (SI.T04)
    pub pinned: bool,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct LicenseCacheRow {
    pub id: i64,
    pub tier: String,
    pub features: String, // JSON string
    pub cached_at: String,
    pub valid_until: String,
    pub hmac: Option<String>, // HMAC-SHA256 hex digest for integrity verification
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct AccountRow {
    pub id: String,
    pub name: String,
    pub provider: String,
    pub credentials_path: String,
    pub priority: i64,
    pub limited_until: Option<String>,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct AccountEventRow {
    pub id: String,
    pub account_id: String,
    pub event_type: String,
    pub metadata: Option<String>,
    pub created_at: String,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct ToolCallRow {
    pub id: String,
    pub message_id: String,
    pub session_id: String,
    pub name: String,
    pub input: String,
    pub output: Option<String>,
    pub status: String,
    pub created_at: String,
    pub completed_at: Option<String>,
}

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct WorktreeRow {
    pub task_id: String,
    pub worktree_path: String,
    pub branch: String,
    pub repo_path: String,
    pub status: String,
    pub created_at: String,
    pub updated_at: String,
}

/// Audit log row for tool call events (DC.T43).
#[derive(Debug, Clone, sqlx::FromRow, serde::Serialize)]
pub struct ToolCallEventRow {
    pub id: String,
    pub session_id: String,
    pub tool_name: String,
    pub sanitized_input: Option<String>,
    pub approved_by: String,
    pub rejection_reason: Option<String>,
    pub created_at: String,
}

/// A file or directory added to a session's repo context registry (MI.T11).
#[derive(Debug, Clone, sqlx::FromRow, serde::Serialize)]
pub struct SessionContextRow {
    pub id: String,
    pub session_id: String,
    pub path: String,
    /// Priority 1 (lowest) … 10 (highest).  Low-priority items evicted first.
    pub priority: i64,
    pub added_at: String,
}

#[derive(Clone)]
pub struct Storage {
    pool: SqlitePool,
}

impl Storage {
    pub async fn new(data_dir: &Path) -> Result<Self> {
        Self::new_with_slow_query(data_dir, 0).await
    }

    /// Open storage **without** running migrations — recovery mode (UX.2 — Sprint BB).
    ///
    /// Connects to the database but skips `migrate()`. Use when the daemon is
    /// started with `--no-migrate` after a failed upgrade.
    pub async fn new_no_migrate(data_dir: &Path) -> Result<Self> {
        tokio::fs::create_dir_all(data_dir).await?;
        let db_path = data_dir.join("clawd.db");
        let opts =
            SqliteConnectOptions::from_str(&format!("sqlite://{}?mode=rwc", db_path.display()))?
                .journal_mode(sqlx::sqlite::SqliteJournalMode::Wal)
                .synchronous(sqlx::sqlite::SqliteSynchronous::Normal)
                .create_if_missing(true);
        let pool = SqlitePool::connect_with(opts).await?;
        tracing::warn!("recovery mode: database migrations skipped (--no-migrate)");
        Ok(Self { pool })
    }

    /// Create storage with slow-query logging enabled (DC.T50).
    ///
    /// `slow_query_ms` is the threshold in milliseconds — queries exceeding it are
    /// logged at WARN level. Set to 0 to disable slow-query logging.
    pub async fn new_with_slow_query(data_dir: &Path, slow_query_ms: u64) -> Result<Self> {
        tokio::fs::create_dir_all(data_dir).await?;
        let db_path = data_dir.join("clawd.db");
        let mut opts =
            SqliteConnectOptions::from_str(&format!("sqlite://{}?mode=rwc", db_path.display()))?
                .journal_mode(sqlx::sqlite::SqliteJournalMode::Wal)
                .synchronous(sqlx::sqlite::SqliteSynchronous::Normal)
                .create_if_missing(true);

        if slow_query_ms > 0 {
            opts = opts.log_slow_statements(
                log::LevelFilter::Warn,
                std::time::Duration::from_millis(slow_query_ms),
            );
        }

        // Back up the database before running migrations (UX.1 — Sprint BB).
        // Creates ~/.clawd/backups/clawd-{version}.db so users can roll back.
        Self::backup_before_migrate(data_dir, &db_path).await;

        let pool = SqlitePool::connect_with(opts).await?;
        Self::migrate(&pool).await?;
        Ok(Self { pool })
    }

    /// Expose the connection pool for direct queries (attention map, intent, etc.).
    pub fn pool(&self) -> &SqlitePool {
        &self.pool
    }

    /// Return a clone of the connection pool (cheap — Arc-backed).
    ///
    /// Use this when you need an **owned** `SqlitePool` — e.g. to construct a
    /// sub-storage type (`TopologyStorage::new`, `PairingStorage::new`, …).
    /// For direct query execution prefer [`Storage::pool`].
    pub fn clone_pool(&self) -> SqlitePool {
        self.pool.clone()
    }

    /// Copy `db_path` to `{data_dir}/backups/clawd-{version}.db` before running
    /// migrations (UX.1 — Sprint BB).  Failure is non-fatal: if the copy fails
    /// (e.g. first-run, no existing DB) we warn and continue.
    async fn backup_before_migrate(data_dir: &Path, db_path: &std::path::PathBuf) {
        if !db_path.exists() {
            return; // fresh install — nothing to back up
        }
        let backup_dir = data_dir.join("backups");
        if let Err(e) = tokio::fs::create_dir_all(&backup_dir).await {
            tracing::warn!(err = %e, "could not create backup dir — skipping pre-migration backup");
            return;
        }
        let version = env!("CARGO_PKG_VERSION");
        let backup_path = backup_dir.join(format!("clawd-{version}.db"));
        if backup_path.exists() {
            return; // already backed up for this version
        }
        if let Err(e) = tokio::fs::copy(db_path, &backup_path).await {
            tracing::warn!(
                err = %e,
                src = %db_path.display(),
                dst = %backup_path.display(),
                "pre-migration backup failed — continuing without backup"
            );
        } else {
            tracing::info!(
                path = %backup_path.display(),
                "pre-migration SQLite backup created"
            );
        }
    }

    async fn migrate(pool: &SqlitePool) -> Result<()> {
        sqlx::migrate!("src/storage/migrations")
            .run(pool)
            .await
            .context("Failed to run database migrations")?;

        // Idempotent column additions (ALTER TABLE IF NOT EXISTS is not
        // supported in SQLite, so we attempt the ALTER and ignore the
        // "duplicate column name" error).
        let alter_stmts = [
            "ALTER TABLE license_cache ADD COLUMN hmac TEXT",
            "ALTER TABLE sessions ADD COLUMN permissions TEXT",
            "ALTER TABLE sessions ADD COLUMN tier TEXT NOT NULL DEFAULT 'cold'",
            "ALTER TABLE sessions ADD COLUMN tier_changed_at INTEGER NOT NULL DEFAULT 0",
            "ALTER TABLE sessions ADD COLUMN last_user_interaction_at INTEGER NOT NULL DEFAULT 0",
            "ALTER TABLE sessions ADD COLUMN pid INTEGER",
            "ALTER TABLE messages ADD COLUMN token_estimate INTEGER",
        ];
        for stmt in alter_stmts {
            let result = sqlx::query(stmt).execute(pool).await;
            if let Err(e) = result {
                let msg = e.to_string();
                if !msg.contains("duplicate column") {
                    return Err(e.into());
                }
            }
        }

        Ok(())
    }

    // ─── Sessions ───────────────────────────────────────────────────────────

    pub async fn create_session(
        &self,
        provider: &str,
        repo_path: &str,
        title: &str,
        permissions: Option<&str>,
    ) -> Result<SessionRow> {
        let id = Uuid::new_v4().to_string();
        let now = Utc::now().to_rfc3339();
        sqlx::query(
            "INSERT INTO sessions (id, provider, repo_path, title, status, created_at, updated_at, message_count, permissions)
             VALUES (?, ?, ?, ?, 'idle', ?, ?, 0, ?)",
        )
        .bind(&id)
        .bind(provider)
        .bind(repo_path)
        .bind(title)
        .bind(&now)
        .bind(&now)
        .bind(permissions)
        .execute(&self.pool)
        .await?;
        self.get_session(&id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("session not found after insert"))
    }

    pub async fn get_session(&self, id: &str) -> Result<Option<SessionRow>> {
        Ok(sqlx::query_as("SELECT * FROM sessions WHERE id = ?")
            .bind(id)
            .fetch_optional(&self.pool)
            .await?)
    }

    pub async fn list_sessions(&self) -> Result<Vec<SessionRow>> {
        with_timeout(async {
            Ok(
                sqlx::query_as("SELECT * FROM sessions ORDER BY created_at DESC")
                    .fetch_all(&self.pool)
                    .await?,
            )
        })
        .await
    }

    pub async fn count_sessions(&self) -> Result<u64> {
        let row: (i64,) = sqlx::query_as("SELECT COUNT(*) FROM sessions")
            .fetch_one(&self.pool)
            .await?;
        Ok(row.0 as u64)
    }

    /// Set the `routed_provider` for a session (used when `provider = "auto"`).
    pub async fn update_session_routed_provider(
        &self,
        id: &str,
        routed_provider: &str,
    ) -> Result<()> {
        let now = Utc::now().to_rfc3339();
        sqlx::query("UPDATE sessions SET routed_provider = ?, updated_at = ? WHERE id = ?")
            .bind(routed_provider)
            .bind(&now)
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn update_session_status(&self, id: &str, status: &str) -> Result<()> {
        let now = Utc::now().to_rfc3339();
        sqlx::query("UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?")
            .bind(status)
            .bind(&now)
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    /// Atomically claims a session for a new turn by setting its status to
    /// `"running"` only when it is currently `"idle"` or `"error"`. Returns
    /// `true` if the claim succeeded, `false` if another caller already holds
    /// the session (i.e. status was `"running"` or `"paused"`).
    ///
    /// This eliminates the TOCTOU window that would otherwise exist between
    /// reading the status and starting the runner.
    pub async fn claim_session_for_run(&self, id: &str) -> Result<bool> {
        let now = Utc::now().to_rfc3339();
        let result = sqlx::query(
            "UPDATE sessions SET status = 'running', updated_at = ? \
             WHERE id = ? AND status IN ('idle', 'error')",
        )
        .bind(&now)
        .bind(id)
        .execute(&self.pool)
        .await?;
        Ok(result.rows_affected() > 0)
    }

    pub async fn increment_message_count(&self, session_id: &str) -> Result<()> {
        let now = Utc::now().to_rfc3339();
        sqlx::query(
            "UPDATE sessions SET message_count = message_count + 1, updated_at = ? WHERE id = ?",
        )
        .bind(&now)
        .bind(session_id)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    pub async fn delete_session(&self, id: &str) -> Result<()> {
        sqlx::query("DELETE FROM sessions WHERE id = ?")
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    /// Set the GCI mode on a session.  Valid values: NORMAL, LEARN, STORM, FORGE, CRUNCH.
    pub async fn set_session_mode(&self, id: &str, mode: &str) -> Result<()> {
        let now = Utc::now().to_rfc3339();
        sqlx::query("UPDATE sessions SET mode = ?, updated_at = ? WHERE id = ?")
            .bind(mode)
            .bind(&now)
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    // ─── Messages ───────────────────────────────────────────────────────────

    pub async fn create_message(
        &self,
        session_id: &str,
        role: &str,
        content: &str,
        status: &str,
    ) -> Result<MessageRow> {
        let id = Uuid::new_v4().to_string();
        let now = Utc::now().to_rfc3339();
        let token_count = crate::intelligence::context::estimate_tokens(content) as i64;
        sqlx::query(
            "INSERT INTO messages (id, session_id, role, content, status, created_at, token_count)
             VALUES (?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(&id)
        .bind(session_id)
        .bind(role)
        .bind(content)
        .bind(status)
        .bind(&now)
        .bind(token_count)
        .execute(&self.pool)
        .await?;
        self.get_message(&id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("message not found after insert"))
    }

    /// Create a message and increment the session's message_count atomically.
    /// Prefer this over calling `create_message` + `increment_message_count` separately.
    pub async fn create_message_and_increment_count(
        &self,
        session_id: &str,
        role: &str,
        content: &str,
        status: &str,
    ) -> Result<MessageRow> {
        let id = Uuid::new_v4().to_string();
        let now = Utc::now().to_rfc3339();
        let token_count = crate::intelligence::context::estimate_tokens(content) as i64;
        let mut tx = self.pool.begin().await?;
        sqlx::query(
            "INSERT INTO messages (id, session_id, role, content, status, created_at, token_count)
             VALUES (?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(&id)
        .bind(session_id)
        .bind(role)
        .bind(content)
        .bind(status)
        .bind(&now)
        .bind(token_count)
        .execute(&mut *tx)
        .await?;
        sqlx::query(
            "UPDATE sessions SET message_count = message_count + 1, updated_at = ? WHERE id = ?",
        )
        .bind(&now)
        .bind(session_id)
        .execute(&mut *tx)
        .await?;
        tx.commit().await?;
        self.get_message(&id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("message not found after insert"))
    }

    pub async fn get_message(&self, id: &str) -> Result<Option<MessageRow>> {
        Ok(sqlx::query_as("SELECT * FROM messages WHERE id = ?")
            .bind(id)
            .fetch_optional(&self.pool)
            .await?)
    }

    pub async fn update_message_content(
        &self,
        id: &str,
        content: &str,
        status: &str,
    ) -> Result<()> {
        sqlx::query("UPDATE messages SET content = ?, status = ? WHERE id = ?")
            .bind(content)
            .bind(status)
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    /// Pin a message so it is always included in the context window (SI.T04).
    pub async fn pin_message(&self, id: &str) -> Result<()> {
        sqlx::query("UPDATE messages SET pinned = 1 WHERE id = ?")
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    /// Unpin a message, allowing it to be dropped when the context window is full (SI.T04).
    pub async fn unpin_message(&self, id: &str) -> Result<()> {
        sqlx::query("UPDATE messages SET pinned = 0 WHERE id = ?")
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn list_messages(
        &self,
        session_id: &str,
        limit: i64,
        before: Option<&str>,
    ) -> Result<Vec<MessageRow>> {
        // The `before` parameter is a message *ID*.  We use a composite
        // (created_at, id) cursor to guarantee stable pagination even when
        // multiple messages share the same timestamp.  Results are always
        // returned in chronological order (oldest first) for the chat UI.
        let rows = if let Some(msg_id) = before {
            // Get the last `limit` messages strictly before the cursor,
            // using (created_at DESC, id DESC) ordering for the inner query
            // so ties are broken deterministically, then flip to ASC for display.
            sqlx::query_as(
                "SELECT * FROM (
                     SELECT * FROM messages
                     WHERE session_id = ?
                       AND (
                           created_at < (SELECT created_at FROM messages WHERE id = ? AND session_id = ?)
                           OR (
                               created_at = (SELECT created_at FROM messages WHERE id = ? AND session_id = ?)
                               AND id < ?
                           )
                       )
                     ORDER BY created_at DESC, id DESC LIMIT ?
                 ) ORDER BY created_at ASC, id ASC",
            )
            .bind(session_id)
            .bind(msg_id).bind(session_id)
            .bind(msg_id).bind(session_id)
            .bind(msg_id)
            .bind(limit)
            .fetch_all(&self.pool)
            .await?
        } else {
            // Initial load: return the *last* `limit` messages in chronological
            // order so the chat view shows the most recent conversation.
            sqlx::query_as(
                "SELECT * FROM (
                     SELECT * FROM messages WHERE session_id = ?
                     ORDER BY created_at DESC, id DESC LIMIT ?
                 ) ORDER BY created_at ASC, id ASC",
            )
            .bind(session_id)
            .bind(limit)
            .fetch_all(&self.pool)
            .await?
        };
        Ok(rows)
    }

    // ─── Tool Calls ─────────────────────────────────────────────────────────

    pub async fn create_tool_call(
        &self,
        session_id: &str,
        message_id: &str,
        name: &str,
        input: &str,
    ) -> Result<ToolCallRow> {
        let id = Uuid::new_v4().to_string();
        let now = Utc::now().to_rfc3339();
        sqlx::query(
            "INSERT INTO tool_calls (id, message_id, session_id, name, input, status, created_at)
             VALUES (?, ?, ?, ?, ?, 'pending', ?)",
        )
        .bind(&id)
        .bind(message_id)
        .bind(session_id)
        .bind(name)
        .bind(input)
        .bind(&now)
        .execute(&self.pool)
        .await?;
        self.get_tool_call(&id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("tool_call not found after insert"))
    }

    pub async fn get_tool_call(&self, id: &str) -> Result<Option<ToolCallRow>> {
        Ok(sqlx::query_as("SELECT * FROM tool_calls WHERE id = ?")
            .bind(id)
            .fetch_optional(&self.pool)
            .await?)
    }

    pub async fn complete_tool_call(
        &self,
        id: &str,
        output: Option<&str>,
        status: &str,
    ) -> Result<()> {
        let now = Utc::now().to_rfc3339();
        sqlx::query("UPDATE tool_calls SET output = ?, status = ?, completed_at = ? WHERE id = ?")
            .bind(output)
            .bind(status)
            .bind(&now)
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn list_tool_calls_for_session(&self, session_id: &str) -> Result<Vec<ToolCallRow>> {
        Ok(
            sqlx::query_as("SELECT * FROM tool_calls WHERE session_id = ? ORDER BY created_at ASC")
                .bind(session_id)
                .fetch_all(&self.pool)
                .await?,
        )
    }

    // ─── Startup recovery ───────────────────────────────────────────────────

    /// On daemon startup, any session left in a transient state from a
    /// previous (crashed/killed) process is recovered:
    ///
    /// - 'running' / 'waiting' → 'error'  (turn was in progress; lost)
    /// - 'paused' → 'idle'                (runner is gone; allow new message)
    ///
    /// Returns the total number of sessions recovered.
    pub async fn recover_stale_sessions(&self) -> Result<u64> {
        with_timeout(async {
            let now = Utc::now().to_rfc3339();
            let crashed = sqlx::query(
                "UPDATE sessions SET status = 'error', updated_at = ?
                 WHERE status IN ('running', 'waiting')",
            )
            .bind(&now)
            .execute(&self.pool)
            .await?
            .rows_affected();

            let paused = sqlx::query(
                "UPDATE sessions SET status = 'idle', updated_at = ?
                 WHERE status = 'paused'",
            )
            .bind(&now)
            .execute(&self.pool)
            .await?
            .rows_affected();

            Ok(crashed + paused)
        })
        .await
    }

    // ─── Maintenance ────────────────────────────────────────────────────────

    /// Delete idle/error sessions older than `days` days and return the count.
    /// Pass `0` to skip pruning.
    pub async fn prune_old_sessions(&self, days: u32) -> Result<u64> {
        if days == 0 {
            return Ok(0);
        }
        with_timeout(async {
            // Safe: `days` is u32 (max ~4.3 billion) and i64 can hold any u32 value without overflow.
            let cutoff = (chrono::Utc::now() - chrono::Duration::days(days as i64)).to_rfc3339();
            let n = sqlx::query(
                "DELETE FROM sessions WHERE status IN ('idle','error') AND updated_at < ?",
            )
            .bind(&cutoff)
            .execute(&self.pool)
            .await?
            .rows_affected();
            Ok(n)
        })
        .await
    }

    /// Run SQLite VACUUM to reclaim disk space after pruning.
    pub async fn vacuum(&self) -> Result<()> {
        sqlx::query("VACUUM").execute(&self.pool).await?;
        Ok(())
    }

    // ─── Settings ───────────────────────────────────────────────────────────

    pub async fn get_setting(&self, key: &str) -> Result<Option<String>> {
        let row: Option<(String,)> = sqlx::query_as("SELECT value FROM settings WHERE key = ?")
            .bind(key)
            .fetch_optional(&self.pool)
            .await?;
        Ok(row.map(|(v,)| v))
    }

    pub async fn set_setting(&self, key: &str, value: &str) -> Result<()> {
        sqlx::query(
            "INSERT INTO settings (key, value) VALUES (?, ?)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
        )
        .bind(key)
        .bind(value)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    // ─── License cache ───────────────────────────────────────────────────────

    pub async fn get_license_cache(&self) -> Result<Option<LicenseCacheRow>> {
        Ok(sqlx::query_as("SELECT * FROM license_cache WHERE id = 1")
            .fetch_optional(&self.pool)
            .await?)
    }

    pub async fn set_license_cache(
        &self,
        tier: &str,
        features: &str,
        cached_at: &str,
        valid_until: &str,
        hmac: Option<&str>,
    ) -> Result<()> {
        sqlx::query(
            "INSERT INTO license_cache (id, tier, features, cached_at, valid_until, hmac)
             VALUES (1, ?, ?, ?, ?, ?)
             ON CONFLICT(id) DO UPDATE SET
               tier = excluded.tier,
               features = excluded.features,
               cached_at = excluded.cached_at,
               valid_until = excluded.valid_until,
               hmac = excluded.hmac",
        )
        .bind(tier)
        .bind(features)
        .bind(cached_at)
        .bind(valid_until)
        .bind(hmac)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    // ─── Accounts ────────────────────────────────────────────────────────────

    pub async fn list_accounts(&self) -> Result<Vec<AccountRow>> {
        Ok(
            sqlx::query_as("SELECT * FROM accounts ORDER BY priority ASC")
                .fetch_all(&self.pool)
                .await?,
        )
    }

    pub async fn create_account(
        &self,
        name: &str,
        provider: &str,
        credentials_path: &str,
        priority: i64,
    ) -> Result<AccountRow> {
        let id = uuid::Uuid::new_v4().to_string();
        sqlx::query(
            "INSERT INTO accounts (id, name, provider, credentials_path, priority)
             VALUES (?, ?, ?, ?, ?)",
        )
        .bind(&id)
        .bind(name)
        .bind(provider)
        .bind(credentials_path)
        .bind(priority)
        .execute(&self.pool)
        .await?;
        self.get_account(&id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("account not found after insert"))
    }

    pub async fn get_account(&self, id: &str) -> Result<Option<AccountRow>> {
        Ok(sqlx::query_as("SELECT * FROM accounts WHERE id = ?")
            .bind(id)
            .fetch_optional(&self.pool)
            .await?)
    }

    pub async fn delete_account(&self, id: &str) -> Result<()> {
        sqlx::query("DELETE FROM accounts WHERE id = ?")
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn set_account_limited(&self, id: &str, limited_until: Option<&str>) -> Result<()> {
        sqlx::query("UPDATE accounts SET limited_until = ? WHERE id = ?")
            .bind(limited_until)
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn update_account_priority(&self, id: &str, priority: i64) -> Result<()> {
        sqlx::query("UPDATE accounts SET priority = ? WHERE id = ?")
            .bind(priority)
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn log_account_event(
        &self,
        account_id: &str,
        event_type: &str,
        metadata: Option<&str>,
    ) -> Result<()> {
        let id = uuid::Uuid::new_v4().to_string();
        sqlx::query(
            "INSERT INTO account_events (id, account_id, event_type, metadata) VALUES (?, ?, ?, ?)",
        )
        .bind(&id)
        .bind(account_id)
        .bind(event_type)
        .bind(metadata)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    pub async fn list_account_events(
        &self,
        account_id: Option<&str>,
        limit: i64,
    ) -> Result<Vec<AccountEventRow>> {
        if let Some(aid) = account_id {
            Ok(sqlx::query_as(
                "SELECT * FROM account_events WHERE account_id = ? ORDER BY created_at DESC LIMIT ?",
            )
            .bind(aid)
            .bind(limit)
            .fetch_all(&self.pool)
            .await?)
        } else {
            Ok(
                sqlx::query_as("SELECT * FROM account_events ORDER BY created_at DESC LIMIT ?")
                    .bind(limit)
                    .fetch_all(&self.pool)
                    .await?,
            )
        }
    }

    // ─── Worktrees ───────────────────────────────────────────────────────────

    /// Insert a new worktree record.
    pub async fn create_worktree(
        &self,
        task_id: &str,
        worktree_path: &str,
        branch: &str,
        repo_path: &str,
    ) -> Result<WorktreeRow> {
        let now = Utc::now().to_rfc3339();
        sqlx::query(
            "INSERT INTO worktrees (task_id, worktree_path, branch, repo_path, created_at, updated_at)
             VALUES (?, ?, ?, ?, ?, ?)",
        )
        .bind(task_id)
        .bind(worktree_path)
        .bind(branch)
        .bind(repo_path)
        .bind(&now)
        .bind(&now)
        .execute(&self.pool)
        .await?;
        self.get_worktree(task_id)
            .await?
            .ok_or_else(|| anyhow::anyhow!("worktree not found after insert"))
    }

    pub async fn get_worktree(&self, task_id: &str) -> Result<Option<WorktreeRow>> {
        Ok(sqlx::query_as("SELECT * FROM worktrees WHERE task_id = ?")
            .bind(task_id)
            .fetch_optional(&self.pool)
            .await?)
    }

    pub async fn list_worktrees(&self, status_filter: Option<&str>) -> Result<Vec<WorktreeRow>> {
        if let Some(status) = status_filter {
            Ok(
                sqlx::query_as("SELECT * FROM worktrees WHERE status = ? ORDER BY created_at DESC")
                    .bind(status)
                    .fetch_all(&self.pool)
                    .await?,
            )
        } else {
            Ok(
                sqlx::query_as("SELECT * FROM worktrees ORDER BY created_at DESC")
                    .fetch_all(&self.pool)
                    .await?,
            )
        }
    }

    pub async fn set_worktree_status(&self, task_id: &str, status: &str) -> Result<()> {
        let now = Utc::now().to_rfc3339();
        sqlx::query("UPDATE worktrees SET status = ?, updated_at = ? WHERE task_id = ?")
            .bind(status)
            .bind(&now)
            .bind(task_id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn delete_worktree(&self, task_id: &str) -> Result<()> {
        sqlx::query("DELETE FROM worktrees WHERE task_id = ?")
            .bind(task_id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    /// Load all non-abandoned worktrees into memory on startup.
    pub async fn load_worktrees(&self) -> Result<Vec<WorktreeRow>> {
        Ok(
            sqlx::query_as(
                "SELECT * FROM worktrees WHERE status NOT IN ('abandoned', 'merged') ORDER BY created_at",
            )
            .fetch_all(&self.pool)
            .await?,
        )
    }

    // ─── Tool call audit log (DC.T43) ─────────────────────────────────────────

    /// Insert a tool call audit event.
    ///
    /// `approved_by`: `"auto"` | `"user"` | `"rejected"`
    pub async fn create_tool_call_event(
        &self,
        session_id: &str,
        tool_name: &str,
        sanitized_input: Option<&str>,
        approved_by: &str,
        rejection_reason: Option<&str>,
    ) -> Result<ToolCallEventRow> {
        let id = Uuid::new_v4().to_string();
        let now = Utc::now().to_rfc3339();
        sqlx::query(
            "INSERT INTO tool_call_events
             (id, session_id, tool_name, sanitized_input, approved_by, rejection_reason, created_at)
             VALUES (?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(&id)
        .bind(session_id)
        .bind(tool_name)
        .bind(sanitized_input)
        .bind(approved_by)
        .bind(rejection_reason)
        .bind(&now)
        .execute(&self.pool)
        .await?;

        Ok(ToolCallEventRow {
            id,
            session_id: session_id.to_string(),
            tool_name: tool_name.to_string(),
            sanitized_input: sanitized_input.map(|s| s.to_string()),
            approved_by: approved_by.to_string(),
            rejection_reason: rejection_reason.map(|s| s.to_string()),
            created_at: now,
        })
    }

    /// Paginated list of tool call audit events newest-first.
    pub async fn list_tool_call_events(
        &self,
        session_id: Option<&str>,
        limit: i64,
        before: Option<&str>,
    ) -> Result<Vec<ToolCallEventRow>> {
        match (session_id, before) {
            (Some(sid), Some(cursor)) => Ok(sqlx::query_as(
                "SELECT * FROM tool_call_events
                 WHERE session_id = ? AND id < ?
                 ORDER BY created_at DESC LIMIT ?",
            )
            .bind(sid)
            .bind(cursor)
            .bind(limit)
            .fetch_all(&self.pool)
            .await?),
            (Some(sid), None) => Ok(sqlx::query_as(
                "SELECT * FROM tool_call_events
                 WHERE session_id = ? ORDER BY created_at DESC LIMIT ?",
            )
            .bind(sid)
            .bind(limit)
            .fetch_all(&self.pool)
            .await?),
            (None, Some(cursor)) => Ok(sqlx::query_as(
                "SELECT * FROM tool_call_events
                 WHERE id < ? ORDER BY created_at DESC LIMIT ?",
            )
            .bind(cursor)
            .bind(limit)
            .fetch_all(&self.pool)
            .await?),
            (None, None) => Ok(sqlx::query_as(
                "SELECT * FROM tool_call_events ORDER BY created_at DESC LIMIT ?",
            )
            .bind(limit)
            .fetch_all(&self.pool)
            .await?),
        }
    }

    /// Delete tool call events older than `days` days (daily background pruning).
    pub async fn prune_tool_call_events(&self, days: u32) -> Result<u64> {
        if days == 0 {
            return Ok(0);
        }
        let result =
            sqlx::query("DELETE FROM tool_call_events WHERE created_at < datetime('now', ?)")
                .bind(format!("-{days} days"))
                .execute(&self.pool)
                .await?;
        Ok(result.rows_affected())
    }

    // ─── Model Intelligence (Sprint H) ──────────────────────────────────────

    /// Set (or clear) the model override for a session (MI.T12).
    ///
    /// `model = None` restores auto-routing; `model = Some(id)` pins a specific model.
    pub async fn set_model_override(&self, session_id: &str, model: Option<&str>) -> Result<()> {
        let now = Utc::now().to_rfc3339();
        sqlx::query("UPDATE sessions SET model_override = ?, updated_at = ? WHERE id = ?")
            .bind(model)
            .bind(&now)
            .bind(session_id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    /// Add a path to the session's repo-context registry (MI.T11).
    ///
    /// Returns the new row.  Duplicate paths are silently updated (priority refreshed).
    pub async fn add_repo_context(
        &self,
        session_id: &str,
        path: &str,
        priority: i64,
    ) -> Result<SessionContextRow> {
        let id = Uuid::new_v4().to_string();
        let now = Utc::now().to_rfc3339();
        sqlx::query(
            "INSERT INTO session_contexts (id, session_id, path, priority, added_at)
             VALUES (?, ?, ?, ?, ?)
             ON CONFLICT(session_id, path) DO UPDATE SET
               priority = excluded.priority,
               added_at = excluded.added_at",
        )
        .bind(&id)
        .bind(session_id)
        .bind(path)
        .bind(priority)
        .bind(&now)
        .execute(&self.pool)
        .await?;
        // Fetch by (session_id, path) in case the ON CONFLICT branch ran.
        Ok(
            sqlx::query_as("SELECT * FROM session_contexts WHERE session_id = ? AND path = ?")
                .bind(session_id)
                .bind(path)
                .fetch_one(&self.pool)
                .await?,
        )
    }

    /// List all context entries for a session, highest priority first (MI.T11).
    pub async fn list_repo_contexts(&self, session_id: &str) -> Result<Vec<SessionContextRow>> {
        Ok(sqlx::query_as(
            "SELECT * FROM session_contexts WHERE session_id = ? ORDER BY priority DESC, added_at ASC",
        )
        .bind(session_id)
        .fetch_all(&self.pool)
        .await?)
    }

    /// Remove a single context entry by its ID (MI.T11).
    pub async fn remove_repo_context(&self, id: &str) -> Result<()> {
        sqlx::query("DELETE FROM session_contexts WHERE id = ?")
            .bind(id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    // ─── Push tokens (Sprint RR PN.3) ────────────────────────────────────────

    pub async fn upsert_push_token(
        &self,
        device_id: &str,
        token: &str,
        platform: &str,
    ) -> Result<()> {
        sqlx::query(
            "INSERT INTO push_tokens (device_id, token, platform, updated_at)
             VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
             ON CONFLICT(device_id) DO UPDATE SET
               token = excluded.token,
               platform = excluded.platform,
               updated_at = excluded.updated_at",
        )
        .bind(device_id)
        .bind(token)
        .bind(platform)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    pub async fn delete_push_token(&self, device_id: &str) -> Result<()> {
        sqlx::query("DELETE FROM push_tokens WHERE device_id = ?")
            .bind(device_id)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn list_push_tokens(&self) -> Result<Vec<(String, String, String)>> {
        let rows = sqlx::query_as::<_, (String, String, String)>(
            "SELECT device_id, token, platform FROM push_tokens ORDER BY registered_at",
        )
        .fetch_all(&self.pool)
        .await?;
        Ok(rows)
    }
}

// Kept in its own file: this module is already long, and the tests are a
// self-contained block that does not need to be read alongside the queries.
#[cfg(test)]
mod tests;
