//! Codex (OpenAI) runner — spawns the `codex` CLI per conversation turn.
//!
//! Unlike Claude Code, Codex does not emit structured stream-json. We stream
//! its stdout line-by-line into a single growing assistant message, then
//! finalise it when the process exits.
//!
//! The `codex` binary is expected to be on `PATH`. Spawned with:
//!   `codex --approval-mode full-auto -q "<content>"`

use super::runner::Runner;
use super::stream_guards::{is_rate_limit_notice, would_exceed_cap};
use crate::{ipc::event::EventBroadcaster, storage::Storage};
use anyhow::{Context, Result};
use async_trait::async_trait;
use serde_json::json;
use std::sync::{
    atomic::{AtomicBool, AtomicU32, Ordering},
    Arc,
};
use tokio::{
    io::{AsyncBufReadExt, BufReader},
    process::{Child, Command},
    sync::Mutex,
};
use tracing::{debug, trace, warn};

pub struct CodexRunner {
    session_id: String,
    repo_path: String,
    storage: Arc<Storage>,
    broadcaster: Arc<EventBroadcaster>,
    child_pid: Arc<AtomicU32>,
    current_child: Arc<Mutex<Option<Child>>>,
    paused: Arc<AtomicBool>,
    /// Set by stop() so the stream_output safety net does not mark "error".
    cancelled: Arc<AtomicBool>,
    /// OpenAI Responses API chaining (Sprint BB PV.8).
    ///
    /// The `previous_response_id` returned by the last completed Codex turn
    /// is stored here and passed as `--previous-response-id` on the next
    /// turn. This enables server-side conversation caching: only the new
    /// user message is sent, not the full history.
    previous_response_id: Arc<Mutex<Option<String>>>,
}

impl CodexRunner {
    pub fn new(
        session_id: String,
        repo_path: String,
        storage: Arc<Storage>,
        broadcaster: Arc<EventBroadcaster>,
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
            previous_response_id: Arc::new(Mutex::new(None)),
        })
    }

    pub async fn run_turn(&self, content: &str) -> Result<()> {
        self.cancelled.store(false, Ordering::Release);

        // Build the base argument list. Pass --previous-response-id if we
        // have one from a prior turn to enable OpenAI Responses API chaining.
        let prev_id = self.previous_response_id.lock().await.clone();
        let mut args: Vec<String> = vec![
            "--approval-mode".to_string(),
            "full-auto".to_string(),
            "-q".to_string(),
            content.to_string(),
        ];
        if let Some(ref id) = prev_id {
            args.push("--previous-response-id".to_string());
            args.push(id.clone());
        }

        let mut child = Command::new("codex")
            .args(&args)
            .current_dir(&self.repo_path)
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .stdin(std::process::Stdio::null())
            .spawn()
            .context("failed to spawn `codex` — is it installed and on PATH?")?;

        let stdout = child.stdout.take().context("no stdout")?;
        let stderr = child.stderr.take().context("no stderr")?;

        // Drain stderr — log at debug level. Detect rate-limit signals.
        let session_id_err = self.session_id.clone();
        let broadcaster_err = self.broadcaster.clone();
        tokio::spawn(async move {
            let mut lines = BufReader::new(stderr).lines();
            while let Ok(Some(line)) = lines.next_line().await {
                debug!(target: "codex_stderr", "{}", line);
                if is_rate_limit_notice(&line) {
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

        // Apply a per-turn timeout (default 10 min) so a hung subprocess
        // cannot hold the session in "running" forever.
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

    /// Maximum size for accumulated stdout (1 MB). Prevents OOM on runaway output.
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
            trace!(session = %self.session_id, line = %line, "codex output");

            // Cap accumulated output at 1 MB to prevent OOM on runaway Codex output.
            if !truncated
                && would_exceed_cap(accumulated.len(), line.len(), Self::MAX_ACCUMULATED_BYTES)
            {
                warn!(
                    session = %self.session_id,
                    bytes = accumulated.len(),
                    "codex output exceeded 1 MB cap — truncating"
                );
                accumulated.push_str("\n\n[output truncated at 1 MB]");
                truncated = true;
            }
            if truncated {
                // Keep draining stdout so the process is not blocked, but stop accumulating.
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

        // Finalize the in-progress message.
        if let Some(ref mid) = message_id {
            let trimmed = accumulated.trim_end().to_string();
            self.storage
                .update_message_content(mid, &trimmed, "done")
                .await?;
            self.broadcaster.broadcast(
                "session.messageUpdated",
                json!({
                    "sessionId": self.session_id,
                    "messageId": mid,
                    "content": trimmed,
                    "status": "done"
                }),
            );
        }

        // Reap the child process.
        if let Some(mut child) = self.current_child.lock().await.take() {
            let _ = child.wait().await;
        }
        self.child_pid.store(0, Ordering::Relaxed);

        // Mark idle unless the turn was cancelled (stop() already handled that).
        if !self.cancelled.load(Ordering::Acquire) {
            self.storage
                .update_session_status(&self.session_id, "idle")
                .await?;
            self.broadcaster.broadcast(
                "session.statusChanged",
                json!({ "sessionId": self.session_id, "status": "idle" }),
            );
        }

        Ok(())
    }
}

#[async_trait]
impl Runner for CodexRunner {
    async fn run_turn(&self, content: &str) -> Result<()> {
        CodexRunner::run_turn(self, content).await
    }

    /// Codex does not support mid-session message injection.
    ///
    /// Unlike Claude Code (which accepts piped input on stdin), the `codex` CLI
    /// takes a single query via `-q` and runs to completion. There is no way to
    /// send additional content to a running Codex turn. Callers should start a
    /// new turn via `run_turn()` instead.
    ///
    /// Returns an error so the caller knows the operation was not performed.
    async fn send(&self, _content: &str) -> Result<()> {
        anyhow::bail!(
            "PROVIDER_NOT_AVAILABLE: codex does not support mid-session message injection — start a new turn instead"
        )
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
