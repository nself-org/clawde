pub mod claude;
pub mod codex;
pub mod completion;
pub mod cursor;
pub mod events;
pub mod router;
pub mod runner;
pub mod stream_guards;
pub mod system_prompt;
pub mod telemetry;
pub mod worktree;

use crate::{ipc::event::EventBroadcaster, storage::Storage, AppContext};
use anyhow::{Context, Result};
use serde::Serialize;
use serde_json::json;
use std::{collections::HashMap, path::PathBuf, sync::Arc};
use tokio::sync::RwLock;
use tracing::{error, info};

use claude::ClaudeCodeRunner;
use codex::CodexRunner;
use cursor::CursorRunner;
use runner::Runner;

// ─── View types (matching @clawde/proto) ─────────────────────────────────────

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SessionView {
    pub id: String,
    pub provider: String,
    pub repo_path: String,
    pub title: String,
    pub status: String,
    pub created_at: String,
    pub updated_at: String,
    pub message_count: i64,
    /// Permission scopes for this session. `None` means all permissions.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub permissions: Option<Vec<String>>,
    /// Provider selected by auto-routing when `provider = "auto"` was requested.
    /// `None` when the provider was explicitly specified.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub routed_provider: Option<String>,
    /// GCI mode: NORMAL | LEARN | STORM | FORGE | CRUNCH
    pub mode: String,
    /// Resource tier: active | warm | cold
    pub tier: String,
    /// Pinned model ID, or `None` when auto-routing is active (MI.T12).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model_override: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MessageView {
    pub id: String,
    pub session_id: String,
    pub role: String,
    pub content: String,
    pub status: String,
    pub created_at: String,
}

// ─── In-memory session handle ─────────────────────────────────────────────────

struct SessionHandle {
    runner: Arc<dyn Runner>,
}

// ─── Manager ─────────────────────────────────────────────────────────────────

pub struct SessionManager {
    storage: Arc<Storage>,
    broadcaster: Arc<EventBroadcaster>,
    data_dir: PathBuf,
    /// In-memory runners for active sessions
    handles: RwLock<HashMap<String, Arc<SessionHandle>>>,
}

impl SessionManager {
    pub fn new(
        storage: Arc<Storage>,
        broadcaster: Arc<EventBroadcaster>,
        data_dir: PathBuf,
    ) -> Self {
        Self {
            storage,
            broadcaster,
            data_dir,
            handles: RwLock::new(HashMap::new()),
        }
    }

    pub async fn active_count(&self) -> usize {
        self.handles.read().await.len()
    }

    // ─── CRUD ────────────────────────────────────────────────────────────────

    pub async fn create(
        &self,
        provider: &str,
        repo_path: &str,
        title: &str,
        max_sessions: usize,
        permissions: Option<Vec<String>>,
        initial_message: Option<&str>,
    ) -> Result<SessionView> {
        // Resolve "auto" provider via intent classification.
        // The resolved provider is stored separately as `routed_provider`.
        let (effective_provider, routed_provider) = if provider == "auto" {
            let chosen = router::classify_intent(initial_message, &[]);
            (
                chosen.as_str().to_string(),
                Some(chosen.as_str().to_string()),
            )
        } else {
            // Validate explicit provider — must match ProviderType.name values from Dart
            match provider {
                "claude" | "codex" | "cursor" => {}
                _ => anyhow::bail!("PROVIDER_NOT_AVAILABLE: unknown provider: {}", provider),
            }
            (provider.to_string(), None)
        };

        // Check that the provider CLI is installed and authenticated.
        check_provider_ready(&effective_provider).await?;

        // Enforce session limit.  Checked here (inside the manager) rather than
        // in the handler so the check and creation are logically coupled; SQLite
        // serialises concurrent writes, so this is free of TOCTOU races.
        if max_sessions > 0 {
            let count = self.storage.count_sessions().await?;
            if count >= max_sessions as u64 {
                anyhow::bail!("session limit reached ({max_sessions} max)");
            }
        }

        let permissions_json = permissions
            .as_ref()
            .map(serde_json::to_string)
            .transpose()
            .context("failed to serialize permissions")?;
        let row = self
            .storage
            .create_session(
                &effective_provider,
                repo_path,
                title,
                permissions_json.as_deref(),
            )
            .await?;

        // If provider was auto-routed, persist the routing decision.
        if let Some(ref rp) = routed_provider {
            self.storage
                .update_session_routed_provider(&row.id, rp)
                .await?;
        }

        info!(
            id = %row.id,
            provider = %effective_provider,
            routed = ?routed_provider,
            "session created"
        );

        // Create an isolated git worktree for this session (non-fatal).
        // The runner will use the worktree path instead of the main repo,
        // preventing file-level conflicts between concurrent sessions.
        let wt_path = worktree::worktree_path(&self.data_dir, &row.id);
        worktree::try_create(std::path::Path::new(repo_path), &wt_path).await;

        let mut view = row_to_view(row);
        // Inject the routing decision into the view since the row was fetched
        // before update_session_routed_provider ran.
        if routed_provider.is_some() {
            view.routed_provider = routed_provider;
        }
        self.broadcaster.broadcast(
            "session.statusChanged",
            json!({ "sessionId": view.id, "status": view.status }),
        );
        Ok(view)
    }

    pub async fn list(&self) -> Result<Vec<SessionView>> {
        let rows = self.storage.list_sessions().await?;
        Ok(rows.into_iter().map(row_to_view).collect())
    }

    pub async fn get(&self, session_id: &str) -> Result<SessionView> {
        self.storage
            .get_session(session_id)
            .await?
            .map(row_to_view)
            .context("SESSION_NOT_FOUND")
    }

    pub async fn delete(&self, session_id: &str) -> Result<()> {
        // Validate session exists before deleting.
        let row = self
            .storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;
        // Stop runner if active
        if let Some(handle) = self.handles.write().await.remove(session_id) {
            let _ = handle.runner.stop().await;
        }
        // Clean up worktree if one was created for this session.
        // Errors are logged but do not prevent session deletion; the DB
        // record is authoritative and the worktree directory is best-effort.
        let wt_path = worktree::worktree_path(&self.data_dir, session_id);
        if wt_path.exists() {
            worktree::try_remove(std::path::Path::new(&row.repo_path), &wt_path).await;
            if wt_path.exists() {
                error!(
                    id = %session_id,
                    path = %wt_path.display(),
                    "worktree cleanup failed — directory still exists after removal attempt"
                );
            } else {
                info!(id = %session_id, "worktree cleaned up successfully");
            }
        }

        self.storage.delete_session(session_id).await?;
        info!(id = %session_id, "session deleted");
        Ok(())
    }

    pub async fn pause(&self, session_id: &str) -> Result<()> {
        // Validate session exists before modifying status.
        self.storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;
        self.storage
            .update_session_status(session_id, "paused")
            .await?;
        if let Some(handle) = self.handles.read().await.get(session_id) {
            handle.runner.pause().await?;
        }
        self.broadcaster.broadcast(
            "session.statusChanged",
            json!({ "sessionId": session_id, "status": "paused" }),
        );
        Ok(())
    }

    pub async fn resume(&self, session_id: &str) -> Result<()> {
        // If the session was paused mid-turn it returns to "running"; if it
        // was idle and resume is called defensively it stays "idle".
        let session = self
            .storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;
        // Only return to "running" if a subprocess is actually active.
        // A paused session with no live runner (e.g. paused then restarted)
        // goes back to "idle" so the client knows it can send a new message.
        let has_runner = self.handles.read().await.contains_key(session_id);
        let new_status = if session.status == "paused" && has_runner {
            "running"
        } else {
            "idle"
        };
        self.storage
            .update_session_status(session_id, new_status)
            .await?;
        if let Some(handle) = self.handles.read().await.get(session_id) {
            handle.runner.resume().await?;
        }
        self.broadcaster.broadcast(
            "session.statusChanged",
            json!({ "sessionId": session_id, "status": new_status }),
        );
        Ok(())
    }

    /// Cancel the currently running turn without deleting the session.
    ///
    /// Kills the in-flight `claude` subprocess (if any), marks the session
    /// as idle, and broadcasts `session.statusChanged`. The session and its
    /// message history are preserved so the user can continue later.
    pub async fn cancel(&self, session_id: &str) -> Result<()> {
        // Ensure session exists before doing anything
        self.storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;

        if let Some(handle) = self.handles.write().await.remove(session_id) {
            let _ = handle.runner.stop().await;
        }

        self.storage
            .update_session_status(session_id, "idle")
            .await?;
        self.broadcaster.broadcast(
            "session.statusChanged",
            json!({ "sessionId": session_id, "status": "idle" }),
        );
        info!(id = %session_id, "session turn cancelled");
        Ok(())
    }

    // ─── Provider management ──────────────────────────────────────────────────

    /// Override the provider for an existing session.
    ///
    /// Only valid when the session is "idle" or "paused" (not "running").
    /// Replaces the in-memory runner if one exists, so the next
    /// `session.sendMessage` uses the new provider.
    pub async fn set_provider(&self, session_id: &str, new_provider: &str) -> Result<()> {
        // Validate the target provider (must be explicit — not "auto").
        match new_provider {
            "claude" | "codex" | "cursor" => {}
            _ => anyhow::bail!("PROVIDER_NOT_AVAILABLE: unknown provider: {}", new_provider),
        }

        let session = self
            .storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;

        // Reject mid-turn provider switches.
        if session.status == "running" {
            anyhow::bail!(
                "PROVIDER_NOT_AVAILABLE: cannot change provider while session is running"
            );
        }

        // Drop any existing runner so the next turn creates a fresh one.
        self.handles.write().await.remove(session_id);

        // Persist the provider choice as routed_provider (the sessions.provider
        // column always holds the originally-requested provider).
        self.storage
            .update_session_routed_provider(session_id, new_provider)
            .await?;

        self.broadcaster.broadcast(
            "session.statusChanged",
            json!({ "sessionId": session_id, "status": session.status, "provider": new_provider }),
        );
        info!(id = %session_id, provider = %new_provider, "session provider updated");
        Ok(())
    }

    // ─── Messages ─────────────────────────────────────────────────────────────

    pub async fn send_message(
        &self,
        session_id: &str,
        content: &str,
        ctx: &AppContext,
    ) -> Result<MessageView> {
        // Ensure session exists; fetch for repo_path and paused status check.
        let session_row = self
            .storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;

        // Reject explicitly paused sessions before attempting the atomic claim.
        if session_row.status == "paused" {
            return Err(anyhow::anyhow!("SESSION_PAUSED"));
        }

        // Atomically claim the session for a new turn.  The DB UPDATE only
        // succeeds when status is 'idle' or 'error', eliminating the TOCTOU
        // race between reading the status and starting the runner.
        let claimed = self.storage.claim_session_for_run(session_id).await?;
        if !claimed {
            return Err(anyhow::anyhow!("SESSION_BUSY"));
        }

        // Persist user message and increment count atomically.
        let msg = self
            .storage
            .create_message_and_increment_count(session_id, "user", content, "done")
            .await?;

        let msg_view = msg_row_to_view(msg.clone());
        self.broadcaster.broadcast(
            "session.messageCreated",
            json!({
                "sessionId": session_id,
                "message": msg_view
            }),
        );

        // Use worktree path if one was created for this session, otherwise
        // fall back to the original repo path for isolation-free operation.
        let effective_path =
            worktree::effective_repo_path(&self.data_dir, session_id, &session_row.repo_path);

        // Get or create a runner for this session.
        // Prefer routed_provider (set via session.setProvider or auto-routing)
        // over the original provider column.
        let effective_provider = session_row
            .routed_provider
            .as_deref()
            .unwrap_or(session_row.provider.as_str());

        let runner: Arc<dyn Runner> = {
            let mut handles = self.handles.write().await;
            if let Some(h) = handles.get(session_id) {
                h.runner.clone()
            } else {
                let r: Arc<dyn Runner> = match effective_provider {
                    "codex" => CodexRunner::new(
                        session_id.to_string(),
                        effective_path,
                        self.storage.clone(),
                        self.broadcaster.clone(),
                    ),
                    "cursor" => CursorRunner::new(
                        session_id.to_string(),
                        effective_path,
                        self.storage.clone(),
                        self.broadcaster.clone(),
                    ),
                    _ => ClaudeCodeRunner::new(
                        session_id.to_string(),
                        effective_path,
                        self.storage.clone(),
                        self.data_dir.clone(),
                        self.broadcaster.clone(),
                        ctx.account_registry.clone(),
                        ctx.license.clone(),
                    ),
                };
                let handle = Arc::new(SessionHandle { runner: r.clone() });
                handles.insert(session_id.to_string(), handle);
                r
            }
        };

        // Spawn the turn in the background so the RPC returns immediately.
        // Events (messageCreated, messageUpdated, statusChanged) are pushed
        // via the broadcaster as the provider process runs.
        let content_owned = content.to_string();
        let session_id_owned = session_id.to_string();
        let storage_bg = self.storage.clone();
        let broadcaster_bg = self.broadcaster.clone();
        tokio::spawn(async move {
            if let Err(e) = runner.run_turn(&content_owned).await {
                error!(session = %session_id_owned, err = %e, "run_turn failed");
                let _ = storage_bg
                    .update_session_status(&session_id_owned, "error")
                    .await;
                broadcaster_bg.broadcast(
                    "session.statusChanged",
                    json!({ "sessionId": session_id_owned, "status": "error" }),
                );
            }
        });

        Ok(msg_view)
    }

    pub async fn get_messages(
        &self,
        session_id: &str,
        limit: i64,
        before: Option<&str>,
    ) -> Result<Vec<MessageView>> {
        // Validate session exists before querying messages.
        self.storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;
        let rows = self
            .storage
            .list_messages(session_id, limit, before)
            .await?;
        Ok(rows.into_iter().map(msg_row_to_view).collect())
    }

    // ─── Permission scopes ─────────────────────────────────────────────────────

    /// Check whether a tool call is permitted for the given session.
    ///
    /// Tool names are mapped to permission scopes:
    /// - `file_read` / `file_write` / `shell_exec` / `git`
    ///
    /// Returns `Ok(())` if permitted, or an error with code -32002 if denied.
    pub async fn check_tool_permission(&self, session_id: &str, tool_name: &str) -> Result<()> {
        let session_row = self
            .storage
            .get_session(session_id)
            .await?
            .context("SESSION_NOT_FOUND")?;

        let permissions_json = match &session_row.permissions {
            Some(p) => p,
            None => return Ok(()), // No restrictions
        };

        let permissions: Vec<String> = serde_json::from_str(permissions_json).unwrap_or_default();

        if permissions.is_empty() {
            return Ok(()); // Empty array also means all permissions
        }

        let required_scope = tool_name_to_scope(tool_name);
        if permissions.iter().any(|p| p == required_scope) {
            Ok(())
        } else {
            anyhow::bail!(
                "PROVIDER_NOT_AVAILABLE: tool '{}' requires '{}' permission which is not in session scope",
                tool_name,
                required_scope
            )
        }
    }

    // ─── Graceful shutdown ────────────────────────────────────────────────────

    /// Stop all active runners and mark their sessions idle.
    /// Called during graceful shutdown to prevent orphaned subprocesses.
    pub async fn drain(&self) {
        let handles: Vec<(String, Arc<SessionHandle>)> = {
            let mut map = self.handles.write().await;
            map.drain().collect()
        };
        for (session_id, handle) in handles {
            // Give each runner up to 5 seconds to stop cleanly; kill if it hangs.
            let stop_result =
                tokio::time::timeout(std::time::Duration::from_secs(5), handle.runner.stop()).await;
            if stop_result.is_err() {
                tracing::warn!(id = %session_id, "runner did not stop within 5s during drain");
            }
            let _ = self
                .storage
                .update_session_status(&session_id, "idle")
                .await;
        }
        info!("all active sessions drained");
    }

    // ─── Tool approval ────────────────────────────────────────────────────────

    pub async fn approve_tool(&self, session_id: &str, tool_call_id: &str) -> Result<()> {
        // Record user acknowledgement in DB and notify clients.
        // Tools run under --dangerously-skip-permissions so they execute before
        // the user taps Approve; this records the user's explicit sign-off.
        self.storage
            .complete_tool_call(tool_call_id, None, "approved")
            .await?;
        self.broadcaster.broadcast(
            "session.toolCallUpdated",
            serde_json::json!({
                "sessionId": session_id,
                "toolCallId": tool_call_id,
                "status": "approved",
            }),
        );
        Ok(())
    }

    pub async fn reject_tool(&self, session_id: &str, tool_call_id: &str) -> Result<()> {
        // Record user rejection in DB and notify clients.
        self.storage
            .complete_tool_call(tool_call_id, None, "rejected")
            .await?;
        self.broadcaster.broadcast(
            "session.toolCallUpdated",
            serde_json::json!({
                "sessionId": session_id,
                "toolCallId": tool_call_id,
                "status": "rejected",
            }),
        );
        Ok(())
    }
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/// Verify that the provider CLI is available and (for claude) authenticated.
/// Returns PROVIDER_NOT_AVAILABLE so the IPC layer maps it to -32002.
async fn check_provider_ready(provider: &str) -> Result<()> {
    match provider {
        "claude" => {
            // `claude auth status` exits 0 when authenticated.
            let status = tokio::process::Command::new("claude")
                .args(["auth", "status"])
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .status()
                .await;
            match status {
                Err(_) => anyhow::bail!(
                    "PROVIDER_NOT_AVAILABLE: claude CLI not found — install from claude.ai/download"
                ),
                Ok(s) if !s.success() => anyhow::bail!(
                    "PROVIDER_NOT_AVAILABLE: claude is not authenticated — run `claude auth login`"
                ),
                _ => Ok(()),
            }
        }
        "codex" => {
            let available = tokio::process::Command::new("codex")
                .arg("--version")
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .status()
                .await
                .is_ok();
            if !available {
                anyhow::bail!(
                    "PROVIDER_NOT_AVAILABLE: codex CLI not found — install via: npm install -g @openai/codex"
                );
            }
            Ok(())
        }
        _ => Ok(()),
    }
}

fn row_to_view(row: crate::storage::SessionRow) -> SessionView {
    let permissions = row
        .permissions
        .as_ref()
        .and_then(|json_str| serde_json::from_str::<Vec<String>>(json_str).ok());
    SessionView {
        id: row.id,
        provider: row.provider,
        repo_path: row.repo_path,
        title: row.title,
        status: row.status,
        created_at: row.created_at,
        updated_at: row.updated_at,
        message_count: row.message_count,
        permissions,
        routed_provider: row.routed_provider,
        mode: row.mode,
        tier: row.tier,
        model_override: row.model_override,
    }
}

/// Map a tool call name to the permission scope it requires.
///
/// Known tool patterns:
/// - Read / Glob / Grep / WebFetch → `file_read`
/// - Write / Edit / NotebookEdit → `file_write`
/// - Bash / shell commands → `shell_exec`
/// - Git operations → `git`
///
/// Unknown tools default to `"unknown"` (least-privilege scope) rather than
/// `"shell_exec"`, so unrecognized tool names are denied by default instead
/// of being granted shell execution rights.
fn tool_name_to_scope(tool_name: &str) -> &'static str {
    let lower = tool_name.to_lowercase();
    if lower.contains("read")
        || lower.contains("glob")
        || lower.contains("grep")
        || lower.contains("fetch")
        || lower.contains("search")
    {
        "file_read"
    } else if lower.contains("write") || lower.contains("edit") || lower.contains("notebook") {
        "file_write"
    } else if lower.contains("git") {
        "git"
    } else if lower.contains("bash")
        || lower.contains("shell")
        || lower.contains("exec")
        || lower.contains("run")
        || lower.contains("command")
    {
        "shell_exec"
    } else {
        // Unrecognized tool — deny via least-privilege "unknown" scope.
        "unknown"
    }
}

fn msg_row_to_view(row: crate::storage::MessageRow) -> MessageView {
    MessageView {
        id: row.id,
        session_id: row.session_id,
        role: row.role,
        content: row.content,
        status: row.status,
        created_at: row.created_at,
    }
}

// Kept in its own file: mod.rs is already long, and the tests do not need to be
// read alongside the manager implementation.
#[cfg(test)]
mod tests;
