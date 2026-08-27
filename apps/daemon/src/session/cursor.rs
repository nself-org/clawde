// SPDX-License-Identifier: MIT
//! Cursor runner (SI.T13) — spawns the `cursor` CLI for each conversation turn.
//!
//! ## CLI interface
//!
//! Cursor's headless chat invocation:
//!   `cursor --headless -p "<content>"`
//!
//! The `cursor` binary is expected to be on PATH.  If it is not present,
//! `run_turn` returns `PROVIDER_NOT_AVAILABLE` with a helpful install message.
//!
//! ## Output format
//!
//! Cursor does not emit structured JSON events like Claude Code.  We stream
//! stdout line-by-line and accumulate it into a single growing assistant
//! message, identical to the `CodexRunner` approach.
//!
//! ## Account management (SI.T14)
//!
//! Cursor authentication is read from:
//!   1. `CURSOR_TOKEN` environment variable (highest priority)
//!   2. `~/.cursor/auth.json` (`accessToken` field)
//!
//! If a `cursor_token` is present in the `AccountRow` record, it is injected
//! via the `CURSOR_TOKEN` env var when spawning the subprocess.

use super::runner::Runner;
use crate::{ipc::event::EventBroadcaster, storage::Storage};
use anyhow::{Context, Result};
use async_trait::async_trait;
use serde_json::json;
use std::{
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, AtomicU32, Ordering},
        Arc,
    },
};
use tokio::{
    io::{AsyncBufReadExt, BufReader},
    process::{Child, Command},
    sync::Mutex,
};
use tracing::{debug, trace, warn};

// ─── Auth helpers (SI.T14) ───────────────────────────────────────────────────

/// Attempt to read the Cursor access token from known locations.
///
/// Priority order:
///   1. `CURSOR_TOKEN` environment variable.
///   2. `~/.cursor/auth.json` — `accessToken` field.
///
/// Returns `None` when no token is found.
pub fn detect_cursor_token() -> Option<String> {
    // 1. Environment variable (highest priority, easily overridden in tests).
    if let Ok(tok) = std::env::var("CURSOR_TOKEN") {
        if !tok.trim().is_empty() {
            return Some(tok.trim().to_owned());
        }
    }

    // 2. ~/.cursor/auth.json
    let auth_path = home_dir().map(|h| h.join(".cursor").join("auth.json"))?;
    let contents = std::fs::read_to_string(&auth_path).ok()?;
    let val: serde_json::Value = serde_json::from_str(&contents).ok()?;
    let token = val.get("accessToken").and_then(|v| v.as_str())?;
    if token.is_empty() {
        return None;
    }
    Some(token.to_owned())
}

fn home_dir() -> Option<PathBuf> {
    std::env::var("HOME").ok().map(PathBuf::from)
}

// ─── CursorRunner ─────────────────────────────────────────────────────────────

pub struct CursorRunner {
    session_id: String,
    repo_path: String,
    storage: Arc<Storage>,
    broadcaster: Arc<EventBroadcaster>,
    child_pid: Arc<AtomicU32>,
    current_child: Arc<Mutex<Option<Child>>>,
    paused: Arc<AtomicBool>,
    /// Set by stop() so the stream_output safety net does not mark "error".
    cancelled: Arc<AtomicBool>,
    /// Optional per-account Cursor auth token.  Injected via `CURSOR_TOKEN`
    /// env var when present.  If `None`, the system default token is used.
    account_token: Option<String>,
}

impl CursorRunner {
    pub fn new(
        session_id: String,
        repo_path: String,
        storage: Arc<Storage>,
        broadcaster: Arc<EventBroadcaster>,
    ) -> Arc<Self> {
        Self::with_account_token(session_id, repo_path, storage, broadcaster, None)
    }

    /// Construct a runner using a specific per-account Cursor token (SI.T14).
    pub fn with_account_token(
        session_id: String,
        repo_path: String,
        storage: Arc<Storage>,
        broadcaster: Arc<EventBroadcaster>,
        account_token: Option<String>,
    ) -> Arc<Self> {
        Arc::new(Self {
            session_id,
            repo_path,
            storage,
            broadcaster,
            child_pid: Arc::new(AtomicU32::new(0)),
            current_child: Arc::new(Mutex::new(None)),
            paused: Arc::new(AtomicBool::new(false)),
            cancelled: Arc::new(AtomicBool::new(false)),
            account_token,
        })
    }

    /// Spawn the `cursor` CLI for one conversation turn.
    ///
    /// Returns `PROVIDER_NOT_AVAILABLE` if the binary cannot be found so the
    /// session layer can display a helpful "install Cursor" message.
    pub async fn run_turn(&self, content: &str) -> Result<()> {
        self.cancelled.store(false, Ordering::Release);

        let mut cmd = Command::new("cursor");
        cmd.args(["--headless", "-p", content]);

        // Inject per-account token if present; fall back to system default.
        if let Some(ref tok) = self.account_token {
            cmd.env("CURSOR_TOKEN", tok);
        } else if let Some(tok) = detect_cursor_token() {
            cmd.env("CURSOR_TOKEN", &tok);
        }

        let mut child = cmd
            .current_dir(&self.repo_path)
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .stdin(std::process::Stdio::null())
            .spawn()
            .map_err(|e| {
                if e.kind() == std::io::ErrorKind::NotFound {
                    anyhow::anyhow!(
                        "PROVIDER_NOT_AVAILABLE: `cursor` binary not found on PATH. \
                         Install Cursor from https://cursor.sh and ensure it is on PATH."
                    )
                } else {
                    anyhow::Error::from(e).context("failed to spawn `cursor`")
                }
            })?;

        let stdout = child.stdout.take().context("no stdout")?;
        let stderr = child.stderr.take().context("no stderr")?;

        // Drain stderr — log at debug level and detect rate-limit signals.
        let session_id_err = self.session_id.clone();
        let broadcaster_err = self.broadcaster.clone();
        tokio::spawn(async move {
            let mut lines = BufReader::new(stderr).lines();
            while let Ok(Some(line)) = lines.next_line().await {
                debug!(target: "cursor_stderr", "{}", line);
                let lower = line.to_ascii_lowercase();
                if lower.contains("rate limit")
                    || lower.contains("rate_limit")
                    || lower.contains("too many requests")
                    || lower.contains("429")
                {
                    broadcaster_err.broadcast(
                        "session.statusChanged",
                        json!({
                            "sessionId": session_id_err,
                            "status": "error",
                            "reason": "RATE_LIMITED"
                        }),
                    );
                }
            }
        });

        if let Some(pid) = child.id() {
            self.child_pid.store(pid, Ordering::Relaxed);
        }
        *self.current_child.lock().await = Some(child);

        let timeout_secs = std::env::var("CLAWD_TURN_TIMEOUT_SECS")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(600);

        match tokio::time::timeout(
            std::time::Duration::from_secs(timeout_secs),
            self.stream_output(stdout),
        )
        .await
        {
            Ok(result) => result,
            Err(_) => {
                if let Some(mut child) = self.current_child.lock().await.take() {
                    let _ = child.kill().await;
                    let _ = child.wait().await;
                }
                anyhow::bail!("turn timed out after {timeout_secs}s — session reset to idle")
            }
        }
    }

    /// Maximum bytes accumulated from stdout before truncating (1 MB).
    const MAX_ACCUMULATED_BYTES: usize = 1_048_576;

    async fn stream_output(&self, stdout: tokio::process::ChildStdout) -> Result<()> {
        let mut lines = BufReader::new(stdout).lines();
        let mut accumulated = String::new();
        let mut message_id: Option<String> = None;
        let mut truncated = false;

        self.storage
            .update_session_status(&self.session_id, "running")
            .await?;
        self.broadcaster.broadcast(
            "session.statusChanged",
            json!({ "sessionId": self.session_id, "status": "running" }),
        );

        while let Some(line) = lines.next_line().await? {
            if self.cancelled.load(Ordering::Acquire) {
                break;
            }
            trace!(session = %self.session_id, line = %line, "cursor output");

            // Cap accumulated output at 1 MB.
            if !truncated && accumulated.len() + line.len() + 1 > Self::MAX_ACCUMULATED_BYTES {
                warn!(
                    session = %self.session_id,
                    bytes = accumulated.len(),
                    "cursor output exceeded 1 MB cap — truncating"
                );
                accumulated.push_str("\n\n[output truncated at 1 MB]");
                truncated = true;
            }
            if truncated {
                continue;
            }

            accumulated.push_str(&line);
            accumulated.push('\n');

            if let Some(ref mid) = message_id {
                self.storage
                    .update_message_content(mid, &accumulated, "streaming")
                    .await?;
                self.broadcaster.broadcast(
                    "session.messageUpdated",
                    json!({
                        "sessionId": self.session_id,
                        "messageId": mid,
                        "content": accumulated,
                        "status": "streaming"
                    }),
                );
            } else {
                let msg = self
                    .storage
                    .create_message(&self.session_id, "assistant", &accumulated, "streaming")
                    .await?;
                self.storage
                    .increment_message_count(&self.session_id)
                    .await?;
                message_id = Some(msg.id.clone());
                self.broadcaster.broadcast(
                    "session.messageCreated",
                    json!({
                        "sessionId": self.session_id,
                        "message": {
                            "id": msg.id,
                            "sessionId": self.session_id,
                            "role": "assistant",
                            "content": accumulated,
                            "status": "streaming",
                            "createdAt": msg.created_at
                        }
                    }),
                );
            }
        }

        // Reap the child process.
        if let Some(mut child) = self.current_child.lock().await.take() {
            let _ = child.wait().await;
        }
        self.child_pid.store(0, Ordering::Relaxed);

        // Finalize the message.
        if let Some(ref mid) = message_id {
            self.storage
                .update_message_content(mid, &accumulated, "done")
                .await?;
            self.broadcaster.broadcast(
                "session.messageUpdated",
                json!({
                    "sessionId": self.session_id,
                    "messageId": mid,
                    "content": accumulated,
                    "status": "done"
                }),
            );
        }

        // Set session status unless cancelled (cancelled sessions are managed by SessionManager).
        if !self.cancelled.load(Ordering::Acquire) {
            let final_status = if accumulated.is_empty() {
                "error"
            } else {
                "idle"
            };
            self.storage
                .update_session_status(&self.session_id, final_status)
                .await?;
            self.broadcaster.broadcast(
                "session.statusChanged",
                json!({ "sessionId": self.session_id, "status": final_status }),
            );
        }

        Ok(())
    }
}

#[async_trait]
impl Runner for CursorRunner {
    async fn run_turn(&self, content: &str) -> Result<()> {
        CursorRunner::run_turn(self, content).await
    }

    async fn send(&self, _content: &str) -> Result<()> {
        Ok(())
    }

    async fn pause(&self) -> Result<()> {
        self.paused.store(true, Ordering::Relaxed);
        #[cfg(unix)]
        {
            let pid = self.child_pid.load(Ordering::Relaxed);
            if pid != 0 {
                unsafe {
                    libc::kill(pid as libc::pid_t, libc::SIGSTOP);
                }
            }
        }
        #[cfg(not(unix))]
        tracing::warn!(
            session = %self.session_id,
            "pause not supported on Windows: subprocess continues until turn finishes"
        );
        Ok(())
    }

    async fn resume(&self) -> Result<()> {
        self.paused.store(false, Ordering::Relaxed);
        #[cfg(unix)]
        {
            let pid = self.child_pid.load(Ordering::Relaxed);
            if pid != 0 {
                unsafe {
                    libc::kill(pid as libc::pid_t, libc::SIGCONT);
                }
            }
        }
        Ok(())
    }

    async fn stop(&self) -> Result<()> {
        self.cancelled.store(true, Ordering::Release);
        #[cfg(unix)]
        {
            let pid = self.child_pid.load(Ordering::Relaxed);
            if pid != 0 && self.paused.load(Ordering::Relaxed) {
                unsafe {
                    libc::kill(pid as libc::pid_t, libc::SIGCONT);
                }
            }
        }
        if let Some(mut child) = self.current_child.lock().await.take() {
            let _ = child.kill().await;
            let _ = child.wait().await;
        }
        self.child_pid.store(0, Ordering::Relaxed);
        Ok(())
    }
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    /// Serialises the tests that mutate CURSOR_TOKEN.
    ///
    /// `std::env::set_var` and `remove_var` act on process-global state, and
    /// Rust runs tests in parallel threads by default, so two tests touching
    /// the same variable race: one can clear or overwrite it while the other
    /// sits between its set and its read. That is exactly how
    /// test_detect_cursor_token_from_env failed in CI (655 passed, 1 failed)
    /// while passing locally and on main -- it depended on the interleaving.
    ///
    /// A lock is enough here and avoids pulling in a crate for two tests. The
    /// guard is taken for the whole set/read/remove sequence, never just the
    /// set.
    static CURSOR_TOKEN_ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    /// Takes the env lock, tolerating a poisoned mutex.
    ///
    /// A panic in one of these tests would otherwise poison the lock and turn
    /// a single failure into every sibling failing for an unrelated reason,
    /// which buries the real one.
    fn lock_cursor_token_env() -> std::sync::MutexGuard<'static, ()> {
        CURSOR_TOKEN_ENV_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    #[test]
    fn test_detect_cursor_token_from_env() {
        let _guard = lock_cursor_token_env();
        std::env::set_var("CURSOR_TOKEN", "test_tok_123");
        let tok = detect_cursor_token();
        std::env::remove_var("CURSOR_TOKEN");
        assert_eq!(tok.as_deref(), Some("test_tok_123"));
    }

    #[test]
    fn test_detect_cursor_token_empty_env_skipped() {
        let _guard = lock_cursor_token_env();
        std::env::set_var("CURSOR_TOKEN", "   ");
        let result = detect_cursor_token();
        std::env::remove_var("CURSOR_TOKEN");
        // Empty/whitespace env var should be ignored; falls through to auth.json check.
        // We can't assert the exact result without controlling the filesystem, but
        // we can assert the env var itself was not returned.
        if let Some(tok) = result {
            assert!(!tok.trim().is_empty());
        }
    }
}
