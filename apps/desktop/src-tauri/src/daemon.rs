// daemon.rs — Clawd sidecar lifecycle management for Tauri 2.
//
// Purpose: Start/stop the bundled `clawd` sidecar binary, poll for the auth
//          token, and expose daemon state to Tauri commands.
// Inputs:  Tauri sidecar config (tauri.conf.json externalBin)
// Outputs: DaemonState shared state with token + running flag
// Constraints: port 4300 (WS), 4301 (REST); token file at platform data dir
// SPORT: T-E1-07 — Tauri 2 migration

use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::{AppHandle, Manager};
use tauri_plugin_shell::{process::CommandChild, ShellExt};
use tracing::{info, warn};

// ── Token file location ───────────────────────────────────────────────────────

fn token_file_path() -> Option<PathBuf> {
    let dir = if cfg!(target_os = "macos") {
        dirs_next::data_dir()?.join("com.clawde.app")
    } else if cfg!(target_os = "windows") {
        dirs_next::data_dir()?.join("ClawDE")
    } else {
        dirs_next::data_dir()?.join("clawde")
    };
    Some(dir.join("auth_token"))
}

fn read_token_sync() -> Option<String> {
    let path = token_file_path()?;
    let content = std::fs::read_to_string(path).ok()?;
    let trimmed = content.trim();
    if trimmed.is_empty() {
        None
    } else {
        Some(trimmed.to_string())
    }
}

// ── Shared state ──────────────────────────────────────────────────────────────

#[derive(Default)]
struct Inner {
    token: Option<String>,
    running: bool,
    child: Option<CommandChild>,
}

/// Managed state exposed to Tauri commands via `State<'_, DaemonState>`.
pub struct DaemonState(Arc<Mutex<Inner>>);

impl DaemonState {
    pub fn new() -> Self {
        Self(Arc::new(Mutex::new(Inner::default())))
    }

    pub fn token(&self) -> Option<String> {
        self.0.lock().unwrap().token.clone()
    }

    pub fn is_running(&self) -> bool {
        self.0.lock().unwrap().running
    }

    fn set_token(&self, token: String) {
        let mut inner = self.0.lock().unwrap();
        inner.token = Some(token);
        inner.running = true;
    }

    fn set_child(&self, child: CommandChild) {
        self.0.lock().unwrap().child = Some(child);
    }

    fn set_start_failed(&self) {
        let mut inner = self.0.lock().unwrap();
        inner.running = false;
        inner.token = None;
    }
}

// ── Daemon lifecycle ──────────────────────────────────────────────────────────

/// Check if the daemon is already listening on port 4300.
async fn ping_daemon() -> bool {
    tokio::net::TcpStream::connect("127.0.0.1:4300")
        .await
        .is_ok()
}

/// Poll the token file for up to `timeout` seconds, returning the token on success.
async fn poll_for_token(timeout: Duration) -> Option<String> {
    let deadline = tokio::time::Instant::now() + timeout;
    loop {
        if let Some(token) = read_token_sync() {
            return Some(token);
        }
        if tokio::time::Instant::now() >= deadline {
            return None;
        }
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
}

/// Ensure the clawd daemon is running.
/// - If already listening on 4300, reads the existing token and returns.
/// - Otherwise, spawns the bundled `clawd` sidecar and waits up to 5 s for the token.
pub async fn ensure_daemon_running(app: &AppHandle, state: &DaemonState) {
    if ping_daemon().await {
        info!("clawd already running on :4300");
        if let Some(token) = read_token_sync() {
            state.set_token(token);
        }
        return;
    }

    info!("spawning clawd sidecar");
    match app.shell().sidecar("clawd") {
        Err(e) => {
            warn!("failed to create clawd sidecar: {e}");
            state.set_start_failed();
        }
        Ok(cmd) => match cmd.args(["serve"]).spawn() {
            Err(e) => {
                warn!("failed to spawn clawd: {e}");
                state.set_start_failed();
            }
            Ok((mut rx, child)) => {
                state.set_child(child);
                // Drain sidecar stdout/stderr in background.
                tokio::spawn(async move {
                    while let Some(event) = rx.recv().await {
                        if let tauri_plugin_shell::process::CommandEvent::Stderr(line) = event {
                            tracing::debug!(
                                "clawd: {}",
                                String::from_utf8_lossy(&line)
                            );
                        }
                    }
                });

                match poll_for_token(Duration::from_secs(8)).await {
                    Some(token) => {
                        info!("clawd token ready");
                        state.set_token(token);
                    }
                    None => {
                        warn!("clawd token did not appear within 8 s");
                        state.set_start_failed();
                    }
                }
            }
        },
    }
}
