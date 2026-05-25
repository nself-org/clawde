//! `clawd status` command handler.
//!
//! Purpose: Query the running daemon for its health status and print a summary.
//! Inputs:  Daemon config (port, data dir), optional `--json` flag.
//! Outputs: Human-readable or JSON status line; exits 0 (healthy) or 1 (stopped).
//! Constraints: Async; requires the daemon to be reachable on the configured port.

use clawd::{cli::client::{read_auth_token, DaemonClient}, config::DaemonConfig};
use crate::logging::format_uptime;

/// Returns exit code: 0 = healthy, 1 = stopped/unresponsive.
pub async fn run_status(config: &DaemonConfig, json: bool) -> i32 {
    let token = match read_auth_token(&config.data_dir) {
        Ok(t) => t,
        Err(_) => {
            if json {
                println!(r#"{{"status":"not_installed"}}"#);
            } else {
                println!("clawd: not installed (run `clawd service install`)");
            }
            return 1;
        }
    };

    let client = DaemonClient::new(config.port, token);
    match client
        .call_once("daemon.status", serde_json::json!({}))
        .await
    {
        Ok(result) => {
            let version = result["version"].as_str().unwrap_or("?");
            let sessions = result["activeSessions"].as_u64().unwrap_or(0);
            let uptime_secs = result["uptime"].as_u64().unwrap_or(0);
            let uptime_str = format_uptime(uptime_secs);

            if json {
                println!("{}", serde_json::to_string(&result).unwrap_or_default());
            } else {
                println!(
                    "clawd {version} — Running ({sessions} active sessions, uptime {uptime_str})"
                );
            }
            0
        }
        Err(_) => {
            if json {
                println!(r#"{{"status":"not_running"}}"#);
            } else {
                println!("clawd: not running");
            }
            1
        }
    }
}
