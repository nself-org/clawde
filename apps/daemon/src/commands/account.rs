//! `clawd account` command handler.
//!
//! Purpose: Add, list, and remove AI provider accounts via the daemon RPC.
//! Inputs:  Daemon config, `AccountCmd` variant from CLI argument parsing.
//! Outputs: Prints account table or confirmation messages; mutates daemon state.
//! Constraints: Async; requires the daemon to be running and reachable.

use crate::cli_args::AccountCmd;
use anyhow::{Context as _, Result};
use clawd::{
    cli::client::{read_auth_token, DaemonClient},
    config::DaemonConfig,
};

pub async fn run_account(config: &DaemonConfig, cmd: AccountCmd) -> Result<()> {
    let token = read_auth_token(&config.data_dir)?;
    let client = DaemonClient::new(config.port, token);

    match cmd {
        AccountCmd::Add {
            provider,
            credentials,
            name,
            priority,
        } => {
            // Validate credentials path
            if !credentials.exists() {
                anyhow::bail!("credentials file not found: {}", credentials.display());
            }
            let creds_path = credentials
                .canonicalize()
                .context("cannot resolve credentials path")?;

            let mut params = serde_json::json!({
                "provider": provider,
                "credentials_path": creds_path.to_string_lossy(),
            });
            if let Some(n) = name {
                params["name"] = serde_json::json!(n);
            }
            if let Some(p) = priority {
                params["priority"] = serde_json::json!(p);
            }

            let result = client.call_once("account.create", params).await?;
            let id = result["id"].as_str().unwrap_or("?");
            println!("Account added: {id}");
        }

        AccountCmd::List { json } => {
            let result = client
                .call_once("account.list", serde_json::json!({}))
                .await?;
            let accounts = result.as_array().cloned().unwrap_or_default();

            if json {
                println!("{}", serde_json::to_string(&accounts)?);
                return Ok(());
            }

            if accounts.is_empty() {
                println!("No accounts configured.");
                return Ok(());
            }

            // Plain ASCII table
            println!(
                "{:<36}  {:<20}  {:<12}  {:<8}  Status",
                "ID", "Name", "Provider", "Priority"
            );
            println!("{}", "-".repeat(90));
            for acc in &accounts {
                let id = acc["id"].as_str().unwrap_or("-");
                let name = acc["name"].as_str().unwrap_or("-");
                let provider = acc["provider"].as_str().unwrap_or("-");
                let priority = acc["priority"].as_i64().unwrap_or(0);
                let status = acc["status"].as_str().unwrap_or("-");
                println!("{id:<36}  {name:<20}  {provider:<12}  {priority:<8}  {status}");
            }
        }

        AccountCmd::Remove { id, yes } => {
            if !yes {
                use std::io::Write;
                print!("Remove account {id}? [y/N] ");
                std::io::stdout().flush().ok();
                let mut input = String::new();
                std::io::stdin().read_line(&mut input).ok();
                if !matches!(input.trim().to_ascii_lowercase().as_str(), "y" | "yes") {
                    println!("Aborted.");
                    return Ok(());
                }
            }
            client
                .call_once("account.delete", serde_json::json!({ "id": id }))
                .await?;
            println!("Account removed: {id}");
        }
    }

    Ok(())
}
