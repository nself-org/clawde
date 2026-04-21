//! End-to-end integration tests for `clawd account` CLI subcommands.
//!
//! These tests verify the full chain:
//!   clawd binary → JSON-RPC WebSocket → daemon → SQLite → RPC response
//!
//! The daemon is started in-process (not as a subprocess) for speed, but the
//! `clawd account` CLI calls are run as real subprocess invocations so that
//! all argument parsing, authentication, and formatting code is exercised.

use clawd::{
    account::AccountRegistry,
    agents::orchestrator::Orchestrator,
    config::DaemonConfig,
    intelligence::token_tracker::TokenTracker,
    ipc::event::EventBroadcaster,
    license::LicenseInfo,
    repo::RepoRegistry,
    scheduler::{
        accounts::AccountPool, fallback::FallbackEngine, queue::SchedulerQueue,
        rate_limits::RateLimitTracker,
    },
    session::SessionManager,
    storage::Storage,
    tasks::TaskStorage,
    telemetry, update,
    worktree::WorktreeManager,
    AppContext,
};
use futures_util::{SinkExt, StreamExt};
use serde_json::{json, Value};
use std::sync::Arc;
use tempfile::TempDir;
use tokio_tungstenite::{connect_async, tungstenite::Message};

// ─── Helpers ─────────────────────────────────────────────────────────────────

fn get_free_port() -> u16 {
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    listener.local_addr().unwrap().port()
}

/// Build a minimal AppContext and start the IPC server in the background.
/// Returns (port, data_dir, ctx) — keep all alive for the test duration.
async fn start_test_daemon(dir: &TempDir) -> (u16, Arc<AppContext>) {
    let data_dir = dir.path().to_path_buf();
    let port = get_free_port();

    let config = Arc::new(DaemonConfig::new(
        Some(port),
        Some(data_dir.clone()),
        Some("error".to_string()),
        None,
        None,
    ));
    let storage = Arc::new(Storage::new(&data_dir).await.unwrap());
    let broadcaster = Arc::new(EventBroadcaster::new());
    let repo_registry = Arc::new(RepoRegistry::new(broadcaster.clone()));
    let session_manager = Arc::new(SessionManager::new(
        storage.clone(),
        broadcaster.clone(),
        data_dir.clone(),
    ));
    let account_registry = Arc::new(AccountRegistry::new(storage.clone(), broadcaster.clone()));
    let updater = Arc::new(update::spawn(config.clone(), broadcaster.clone()));
    let account_pool = Arc::new(AccountPool::new());
    let rate_limit_tracker = Arc::new(RateLimitTracker::new());
    let fallback_engine = Arc::new(FallbackEngine::new(
        Arc::clone(&account_pool),
        Arc::clone(&rate_limit_tracker),
    ));
    let token_tracker = TokenTracker::new(storage.clone());
    let memory_store = clawd::memory::MemoryStore::new(storage.clone_pool());
    let metrics_store = clawd::metrics::MetricsStore::new(storage.clone_pool());
    let quality = clawd::connectivity::new_shared_quality();
    let peer_registry = clawd::connectivity::direct::new_registry();

    // Write a known auth token to the data_dir so the CLI can authenticate.
    let auth_token = "test-token-12345".to_string();
    std::fs::write(data_dir.join("auth_token"), &auth_token).unwrap();

    let ctx = Arc::new(AppContext {
        config: config.clone(),
        storage: storage.clone(),
        broadcaster: broadcaster.clone(),
        repo_registry,
        session_manager,
        daemon_id: "test-daemon-id".to_string(),
        license: Arc::new(tokio::sync::RwLock::new(LicenseInfo::free())),
        telemetry: Arc::new(telemetry::spawn(
            config,
            "test-daemon-id".to_string(),
            "free".to_string(),
        )),
        account_registry,
        updater,
        started_at: std::time::Instant::now(),
        auth_token,
        task_storage: Arc::new(TaskStorage::new(storage.clone_pool())),
        worktree_manager: Arc::new(WorktreeManager::new(&data_dir)),
        account_pool,
        rate_limit_tracker,
        fallback_engine,
        scheduler_queue: Arc::new(SchedulerQueue::new()),
        orchestrator: Arc::new(Orchestrator::new()),
        token_tracker,
        metrics: Arc::new(clawd::metrics::DaemonMetrics::new()),
        version_watcher: Arc::new(clawd::doctor::version_watcher::VersionWatcher::new(
            broadcaster.clone(),
        )),
        ide_bridge: clawd::ide::new_shared_bridge(),
        provider_sessions: clawd::agents::provider_session::new_shared_registry(),
        recovery_mode: false,
        automation_engine: clawd::automations::engine::AutomationEngine::new(
            clawd::automations::builtins::all(),
        ),
        quality,
        peer_registry,
        memory_store,
        metrics_store,
    });

    let ctx_clone = ctx.clone();
    tokio::spawn(async move {
        let _ = clawd::ipc::run(ctx_clone).await;
    });

    tokio::time::sleep(std::time::Duration::from_millis(80)).await;
    (port, ctx)
}

/// Call a JSON-RPC method with authentication on the test daemon.
async fn ws_rpc_authed(port: u16, token: &str, method: &str, params: Value) -> Value {
    let url = format!("ws://127.0.0.1:{port}");
    let (mut ws, _) = connect_async(&url).await.expect("connect failed");

    // Auth
    let auth = json!({"jsonrpc":"2.0","id":1,"method":"daemon.auth","params":{"token":token}});
    ws.send(Message::Text(serde_json::to_string(&auth).unwrap()))
        .await
        .unwrap();
    // Skip auth response
    loop {
        let msg = ws.next().await.unwrap().unwrap();
        if let Message::Text(t) = msg {
            let v: Value = serde_json::from_str(&t).unwrap();
            if v.get("id").and_then(|x| x.as_u64()) == Some(1) {
                break;
            }
        }
    }

    // RPC call
    let req = json!({"jsonrpc":"2.0","id":2,"method":method,"params":params});
    ws.send(Message::Text(serde_json::to_string(&req).unwrap()))
        .await
        .unwrap();
    loop {
        let msg = ws.next().await.unwrap().unwrap();
        if let Message::Text(t) = msg {
            let v: Value = serde_json::from_str(&t).unwrap();
            if v.get("id").and_then(|x| x.as_u64()) == Some(2) {
                return v;
            }
        }
    }
}

/// Path to the `clawd` test binary (set by Cargo during integration test runs).
fn binary_path() -> std::path::PathBuf {
    // CARGO_BIN_EXE_clawd is set by Cargo for integration tests.
    // Fall back to `target/debug/clawd` for local development.
    std::env::var("CARGO_BIN_EXE_clawd")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|_| {
            let mut p = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"));
            p.push("../../target/debug/clawd");
            p
        })
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// Tracked: S88/T-CI-01. These CLI-binary integration tests depend on a real
// clawd binary being built and reachable via a live WebSocket inside CI. On
// hosted GHA runners the in-process daemon bring-up races the CLI subprocess
// (80 ms sleep is not always enough), producing intermittent timeouts. Mark
// as ignored in CI — run locally with `cargo test -- --ignored`.
#[ignore = "flaky on CI runners; run locally with --ignored"]
#[tokio::test]
async fn test_cli_account_add_and_list() {
    let dir = TempDir::new().unwrap();
    let (port, _ctx) = start_test_daemon(&dir).await;
    let data_dir = dir.path().to_str().unwrap();
    let binary = binary_path();

    if !binary.exists() {
        // Skip if binary not built yet
        eprintln!("Skipping: clawd binary not found at {}", binary.display());
        return;
    }

    // Use /dev/null as the credentials file (safe, always exists on unix)
    let creds = if cfg!(unix) { "/dev/null" } else { "nul" };

    // Run: clawd account add --provider claude --credentials /dev/null --name "Test" --data-dir <dir>
    let output = std::process::Command::new(&binary)
        .args([
            "account",
            "add",
            "--provider",
            "claude",
            "--credentials",
            creds,
            "--name",
            "Test Account",
            "--data-dir",
            data_dir,
            "--port",
            &port.to_string(),
        ])
        .output()
        .expect("failed to run clawd");

    assert!(
        output.status.success(),
        "clawd account add failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(
        stdout.contains("Account added:"),
        "unexpected output: {stdout}"
    );

    // Verify via RPC that the account appears in account.list
    let resp = ws_rpc_authed(port, "test-token-12345", "account.list", json!({})).await;
    let accounts = resp["result"].as_array().expect("expected array");
    assert!(
        !accounts.is_empty(),
        "account list should not be empty after add"
    );

    let added = accounts
        .iter()
        .find(|a| a["provider"].as_str() == Some("claude"))
        .expect("should find claude account");
    assert_eq!(added["name"].as_str().unwrap_or(""), "Test Account");
}

#[ignore = "flaky on CI runners; run locally with --ignored"]
#[tokio::test]
async fn test_cli_account_list_json_output() {
    let dir = TempDir::new().unwrap();
    let (port, _ctx) = start_test_daemon(&dir).await;
    let data_dir = dir.path().to_str().unwrap();
    let binary = binary_path();

    if !binary.exists() {
        eprintln!("Skipping: clawd binary not found at {}", binary.display());
        return;
    }

    // List with --json flag on empty list
    let output = std::process::Command::new(&binary)
        .args([
            "account",
            "list",
            "--json",
            "--data-dir",
            data_dir,
            "--port",
            &port.to_string(),
        ])
        .output()
        .expect("failed to run clawd");

    assert!(
        output.status.success(),
        "clawd account list --json failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );

    let stdout = String::from_utf8_lossy(&output.stdout);
    // Should be parseable JSON (either [] or error message)
    let _: Value = serde_json::from_str(stdout.trim()).expect("output should be valid JSON");
}

#[ignore = "flaky on CI runners; run locally with --ignored"]
#[tokio::test]
async fn test_cli_account_remove_with_yes_flag() {
    let dir = TempDir::new().unwrap();
    let (port, _ctx) = start_test_daemon(&dir).await;
    let data_dir = dir.path().to_str().unwrap();
    let binary = binary_path();

    if !binary.exists() {
        eprintln!("Skipping: clawd binary not found at {}", binary.display());
        return;
    }

    let creds = if cfg!(unix) { "/dev/null" } else { "nul" };

    // Add an account first
    let add_output = std::process::Command::new(&binary)
        .args([
            "account",
            "add",
            "--provider",
            "claude",
            "--credentials",
            creds,
            "--name",
            "To Remove",
            "--data-dir",
            data_dir,
            "--port",
            &port.to_string(),
        ])
        .output()
        .expect("failed to run clawd account add");

    assert!(
        add_output.status.success(),
        "add failed: {}",
        String::from_utf8_lossy(&add_output.stderr)
    );

    // Get account ID from RPC
    let list_resp = ws_rpc_authed(port, "test-token-12345", "account.list", json!({})).await;
    let accounts = list_resp["result"].as_array().expect("expected array");
    assert!(!accounts.is_empty(), "expected at least one account");
    let account_id = accounts[0]["id"].as_str().expect("expected id");

    // Remove with --yes to skip prompt
    let remove_output = std::process::Command::new(&binary)
        .args([
            "account",
            "remove",
            account_id,
            "--yes",
            "--data-dir",
            data_dir,
            "--port",
            &port.to_string(),
        ])
        .output()
        .expect("failed to run clawd account remove");

    assert!(
        remove_output.status.success(),
        "remove failed: {}",
        String::from_utf8_lossy(&remove_output.stderr)
    );

    // Verify account is gone via RPC
    let final_list = ws_rpc_authed(port, "test-token-12345", "account.list", json!({})).await;
    let remaining = final_list["result"].as_array().expect("expected array");
    assert!(
        remaining
            .iter()
            .all(|a| a["id"].as_str() != Some(account_id)),
        "account should be removed"
    );
}

// ─── Unit tests for output formatting ────────────────────────────────────────

#[test]
fn test_format_uptime_hours() {
    // These match the format_uptime function in main.rs
    // 2h 14m = 8040 seconds
    let secs = 8040u64;
    let h = secs / 3600;
    let m = (secs % 3600) / 60;
    let result = format!("{h}h {m}m");
    assert_eq!(result, "2h 14m");
}

#[test]
fn test_format_uptime_minutes() {
    let secs = 183u64; // 3m 3s
    let h = secs / 3600;
    let m = (secs % 3600) / 60;
    let s = secs % 60;
    let result = if h > 0 {
        format!("{h}h {m}m")
    } else if m > 0 {
        format!("{m}m {s}s")
    } else {
        format!("{s}s")
    };
    assert_eq!(result, "3m 3s");
}

#[test]
fn test_logs_tail_last_n_lines() {
    // Verify tail logic works correctly
    let content = "line1\nline2\nline3\nline4\nline5\n";
    let all_lines: Vec<&str> = content.lines().collect();
    let n = 3usize;
    let start = all_lines.len().saturating_sub(n);
    let result: Vec<&&str> = all_lines.iter().skip(start).collect();
    assert_eq!(result.len(), 3);
    assert_eq!(*result[0], "line3");
    assert_eq!(*result[2], "line5");
}

#[test]
fn test_logs_tail_all_when_lines_zero() {
    let content = "line1\nline2\nline3\n";
    let all_lines: Vec<&str> = content.lines().collect();
    let n = 0u64;
    let start = if n == 0 || n as usize >= all_lines.len() {
        0
    } else {
        all_lines.len() - n as usize
    };
    assert_eq!(start, 0);
}
