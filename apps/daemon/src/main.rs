//! `clawd` daemon binary entry point.
//!
//! Purpose: Parse CLI arguments and dispatch to the appropriate command handler.
//! Inputs:  Command-line arguments (std::env::args).
//! Outputs: Delegates entirely to handler modules; returns Ok(()) on success.
//! Constraints: Async (Tokio); logging must be initialised before any `tracing::*` calls.

mod cli_args;
mod commands;
mod crash;
mod logging;

use anyhow::{Context as _, Result};
use clap::Parser;
use clawd::{config::DaemonConfig, service};
use cli_args::{Args, Command, InstructionsAction, PolicyAction, BenchAction, TokenCmd};
use commands::{
    account::run_account,
    init::run_init,
    logs::run_logs,
    server::run_server,
    status::run_status,
    tasks::run_tasks,
    token::{run_token_qr, run_token_show},
    update::run_update,
};

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();

    // ── Logging setup ────────────────────────────────────────────────────────
    // Init once — must happen before any tracing calls.
    let log_level = args.log.as_deref().unwrap_or("info").to_owned();
    let log_format = std::env::var("CLAWD_LOG_FORMAT").unwrap_or_else(|_| "pretty".to_string());
    let _file_guard = logging::setup_logging(&log_level, args.log_file.as_deref(), &log_format);

    let quiet = args.quiet;
    match args.command {
        Some(Command::Service { action }) => match action {
            cli_args::ServiceAction::Install => service::install()?,
            cli_args::ServiceAction::Uninstall => service::uninstall()?,
            cli_args::ServiceAction::Status => service::status()?,
        },
        Some(Command::Init { path, template }) => {
            let path = match path {
                Some(p) => p,
                None => std::env::current_dir().context("failed to determine current directory")?,
            };
            run_init(&path, template.as_deref(), quiet).await?;
        }
        Some(Command::Tasks { action }) => {
            run_tasks(action, args.data_dir, quiet).await?;
        }
        Some(Command::Update { check, apply }) => {
            run_update(check, apply, quiet, args.data_dir).await?;
        }
        Some(Command::Start) => service::start()?,
        Some(Command::Stop) => service::stop()?,
        Some(Command::Restart) => service::restart()?,
        Some(Command::Doctor) => {
            let results = clawd::doctor::run_doctor();
            clawd::doctor::print_doctor_results(&results);
            let failed = results.iter().filter(|r| !r.passed).count();
            std::process::exit(if failed == 0 { 0 } else { 1 });
        }
        Some(Command::Pair) => {
            println!("To pair a device, open the ClawDE desktop app and go to:");
            println!("  Settings > Remote Access > Add Device");
            println!();
            println!("Or run the daemon and use: clawd pair --daemon");
            println!("(Requires daemon to be running to generate a one-time PIN)");
        }
        Some(Command::Token { cmd }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            match cmd {
                TokenCmd::Show => run_token_show(&config)?,
                TokenCmd::Qr { relay } => run_token_qr(&config, relay)?,
            }
        }
        Some(Command::Project(cmd)) => {
            let _ = cmd; // suppress unused warning — full RPC wiring is a future task
            eprintln!("project commands require the daemon to be running.");
            eprintln!("Start the daemon with: clawd start");
            std::process::exit(1);
        }
        Some(Command::Status { json }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let exit_code = run_status(&config, json).await;
            std::process::exit(exit_code);
        }
        Some(Command::Logs {
            follow,
            lines,
            filter,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            run_logs(&config, follow, lines, filter.as_deref())?;
        }
        Some(Command::Account { cmd }) => {
            let config = DaemonConfig::new(
                args.port,
                args.data_dir,
                Some("error".to_string()),
                None,
                None,
            );
            run_account(&config, cmd).await?;
        }
        Some(Command::SignRun {
            task_id,
            sha,
            notes,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            clawd::cli::sign_run::run_sign_run_cli(&task_id, &sha, &notes, &config.data_dir)?;
        }
        Some(Command::Chat {
            resume,
            session_list,
            non_interactive,
            provider,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let opts = clawd::cli::chat::ChatOpts {
                resume,
                session_list,
                non_interactive,
                provider: Some(provider),
            };
            clawd::cli::chat::run_chat(opts, &config).await?;
        }
        Some(Command::Explain {
            file,
            line,
            lines,
            stdin,
            error,
            format,
            provider,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let fmt = if format == "json" {
                clawd::cli::explain::ExplainFormat::Json
            } else {
                clawd::cli::explain::ExplainFormat::Text
            };
            let opts = clawd::cli::explain::ExplainOpts {
                file,
                line,
                lines,
                stdin,
                error,
                format: fmt,
                provider: Some(provider),
            };
            clawd::cli::explain::run_explain(opts, &config).await?;
        }
        Some(Command::Instructions { action }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let port = config.port;
            let data_dir = config.data_dir.clone();
            match action {
                InstructionsAction::Compile {
                    target,
                    project,
                    dry_run,
                } => {
                    let opts = clawd::cli::instructions::CompileOpts {
                        target,
                        project,
                        dry_run,
                    };
                    clawd::cli::instructions::compile(opts, &data_dir, port).await?;
                }
                InstructionsAction::Explain { path } => {
                    clawd::cli::instructions::explain(path, &data_dir, port).await?;
                }
                InstructionsAction::Lint { project, ci } => {
                    clawd::cli::instructions::lint(project, ci, &data_dir, port).await?;
                }
                InstructionsAction::Import { project } => {
                    clawd::cli::instructions::import(project, &data_dir, port).await?;
                }
                InstructionsAction::Snapshot {
                    path,
                    output,
                    check,
                } => {
                    clawd::cli::instructions::snapshot(path, output, check, &data_dir, port)
                        .await?;
                }
                InstructionsAction::Doctor { project } => {
                    clawd::cli::instructions::doctor(project, &data_dir, port).await?;
                }
            }
        }
        Some(Command::Policy { action }) => {
            let config = DaemonConfig::new(
                args.port,
                args.data_dir,
                Some("error".to_string()),
                None,
                None,
            );
            let port = config.port;
            let data_dir = config.data_dir.clone();
            match action {
                PolicyAction::Test {
                    file,
                    project: _,
                    ci,
                } => {
                    clawd::cli::policy::test(
                        file.map(std::path::PathBuf::from),
                        ci,
                        &data_dir,
                        port,
                    )
                    .await?;
                }
                PolicyAction::Seed { project } => {
                    clawd::cli::policy::install_seed_tests(&project).await?;
                }
            }
        }
        Some(Command::Bench { action }) => {
            let config = DaemonConfig::new(
                args.port,
                args.data_dir,
                Some("error".to_string()),
                None,
                None,
            );
            let port = config.port;
            let data_dir = config.data_dir.clone();
            match action {
                BenchAction::Run { task, provider } => {
                    clawd::cli::bench::run(Some(task), Some(provider), &data_dir, port).await?;
                }
                BenchAction::Compare {
                    base_ref,
                    provider: _,
                } => {
                    let br = base_ref.unwrap_or_else(|| "HEAD~1".to_string());
                    clawd::cli::bench::compare(br, &data_dir, port).await?;
                }
                BenchAction::Seed => {
                    let token = clawd::cli::client::read_auth_token(&data_dir)?;
                    let client = clawd::cli::client::DaemonClient::new(port, token);
                    let res = client
                        .call_once("bench.seedTasks", serde_json::json!({}))
                        .await?;
                    let created = res["created"].as_u64().unwrap_or(0);
                    let skipped = res["skipped"].as_u64().unwrap_or(0);
                    println!("Seed complete: {created} tasks created, {skipped} already present.");
                }
            }
        }
        Some(Command::Observe { session }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            clawd::cli::observe::observe(session, &config.data_dir, config.port).await?;
        }
        Some(Command::Providers) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            clawd::cli::providers::list_capabilities(&config.data_dir, config.port).await?;
        }
        Some(Command::DiffRisk { path }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let worktree = path.map(|p| p.to_string_lossy().into_owned());
            clawd::cli::diff_risk::diff_risk_score(worktree, &config.data_dir, config.port).await?;
        }
        None | Some(Command::Serve) => {
            run_server(
                args.port,
                args.data_dir,
                args.log,
                args.max_sessions,
                args.bind_address,
                args.no_migrate,
            )
            .await?;
        }
    }

    Ok(())
}
