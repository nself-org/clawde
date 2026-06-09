//! `clawd update` command handler.
//!
//! Purpose: Check for and apply binary self-updates, routing through the daemon
//!          when it is running or falling back to in-process update logic.
//! Inputs:  `check_only` flag, `apply_only` flag, quiet flag, optional data dir.
//! Outputs: Prints status messages; may exec-replace the running process.
//! Constraints: Async; requires network access for the check step.

use anyhow::Result;
use clawd::cli::client::{read_auth_token, DaemonClient};
use clawd::{config::DaemonConfig, ipc::event::EventBroadcaster, update};
use std::sync::Arc;

pub async fn run_update(
    check_only: bool,
    apply_only: bool,
    quiet: bool,
    data_dir: Option<std::path::PathBuf>,
) -> Result<()> {
    let config = Arc::new(DaemonConfig::new(
        None,
        data_dir,
        Some("error".to_string()),
        None,
        None,
    ));

    // If daemon is running, route through it so it controls the update lifecycle.
    if !apply_only {
        if let Ok(token) = read_auth_token(&config.data_dir) {
            let client = DaemonClient::new(config.port, token);
            if client.is_reachable().await {
                if !quiet {
                    println!("Checking for updates...");
                }
                match client
                    .call_once("daemon.checkUpdate", serde_json::json!({}))
                    .await
                {
                    Ok(result) => {
                        let available = result["available"].as_bool().unwrap_or(false);
                        let latest = result["latest"].as_str().unwrap_or("?");
                        let current = result["current"].as_str().unwrap_or("?");
                        if !available {
                            if !quiet {
                                println!("clawd {current} is up to date (latest: {latest}).");
                            }
                            return Ok(());
                        }
                        if !quiet {
                            println!("Update available: {current} -> {latest}");
                        }
                        if check_only {
                            return Ok(());
                        }
                        if !quiet {
                            println!("Applying update via daemon...");
                        }
                        let _ = client
                            .call_once("daemon.applyUpdate", serde_json::json!({}))
                            .await;
                        if !quiet {
                            println!("Update initiated — daemon will restart when complete.");
                        }
                        return Ok(());
                    }
                    Err(_) => {
                        // daemon doesn't support checkUpdate RPC — fall through to in-process
                    }
                }
            }
        }
    }

    // In-process path: daemon not running or token not found
    let broadcaster = Arc::new(EventBroadcaster::new());
    let updater = update::Updater::new(config, broadcaster);

    if apply_only {
        match updater.apply_if_ready().await? {
            true => {
                if !quiet {
                    println!("Update applied — restarting.");
                }
            }
            false => {
                if !quiet {
                    println!("No pending update to apply.");
                }
            }
        }
        return Ok(());
    }

    if !quiet {
        println!("Checking for updates...");
    }
    let (current, latest, available) = updater.check().await?;
    if !available {
        if !quiet {
            println!("clawd {current} is up to date (latest: {latest}).");
        }
        return Ok(());
    }

    if !quiet {
        println!("Update available: {current} -> {latest}");
    }

    if check_only {
        return Ok(());
    }

    if !quiet {
        println!("Downloading...");
    }
    updater.check_and_download().await?;
    if !quiet {
        println!("Download complete. Applying update...");
    }
    match updater.apply_if_ready().await? {
        true => {
            if !quiet {
                println!("Update applied — restarting.");
            }
        }
        false => {
            if !quiet {
                println!("Update downloaded but could not be applied yet.");
            }
        }
    }

    Ok(())
}
