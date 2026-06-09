//! `clawd start` / daemon server command handler.
//!
//! Purpose: Bootstrap all daemon subsystems and enter the IPC event loop.
//! Inputs:  Override flags for port, data dir, log level, session cap, bind address,
//!          and `--no-migrate` (recovery mode without schema migrations).
//! Outputs: Runs until SIGINT/SIGTERM or fatal error; WAL-checkpoints on clean shutdown.
//! Constraints: Async; must be the last thing called from `main()` — this function never
//!              returns on normal operation.

use crate::crash::{check_crash_log, install_panic_hook};
use anyhow::Result;
use clawd::{
    account::AccountRegistry, auth, config::DaemonConfig, identity,
    intelligence::token_tracker::TokenTracker, ipc::event::EventBroadcaster, license, mdns, relay,
    repo::RepoRegistry, session::SessionManager, storage::Storage, tasks::TaskStorage, telemetry,
    update, AppContext,
};
use std::sync::Arc;
use tracing::{info, warn};

/// Bootstrap all daemon subsystems and run the IPC server.
///
/// Purpose: Orchestrates startup: config, storage migration, identity, license,
///          background tasks (pruning, telemetry, tasks heartbeat, drift scanner),
///          mDNS advertising, relay, and finally `clawd::ipc::run`.
/// Inputs:  Optional overrides for port, data dir, log level, session cap, bind
///          address, and `no_migrate` (skip DB migrations for emergency recovery).
/// Outputs: Returns `Ok(())` on clean shutdown; propagates any fatal I/O error.
/// Constraints: Tokio async runtime required; exits 1 on auth-token failure.
pub async fn run_server(
    port: Option<u16>,
    data_dir: Option<std::path::PathBuf>,
    log: Option<String>,
    max_sessions: Option<usize>,
    bind_address: Option<String>,
    no_migrate: bool,
) -> Result<()> {
    // Warn when a non-default port is used (dev-only scenario per F55.5.01).
    if let Some(p) = port {
        if p != 4300 {
            eprintln!(
                "warning: non-default port {p}. \n  This is for development only. \
                \n  Two daemons in production mode are unsupported."
            );
        }
    }
    info!(version = env!("CARGO_PKG_VERSION"), "clawd starting");

    let config = Arc::new(DaemonConfig::new(
        port,
        data_dir,
        log,
        max_sessions,
        bind_address,
    ));
    info!(
        data_dir = %config.data_dir.display(),
        port = config.port,
        max_sessions = config.max_sessions,
        "config loaded"
    );

    // ── Panic hook: write crash.log on panic (DC.T51) ────────────────────────
    install_panic_hook(config.data_dir.clone());
    // If previous run panicked, log the crash report and delete it.
    check_crash_log(&config.data_dir);

    // ── Rollback-on-crash detection (DC.T29) ─────────────────────────────────
    // If the previous binary crashed immediately after applying an update, restore
    // the backup automatically before proceeding.
    if update::check_and_rollback(&config.data_dir) {
        warn!("previous update was rolled back — running on restored binary");
    }
    // Delete the sentinel so a clean shutdown doesn't trigger rollback next time.
    update::delete_rollback_sentinel(&config.data_dir);

    // ── Provider CLI availability check ──────────────────────────────────────
    for binary in &["claude", "codex"] {
        let available = std::process::Command::new(binary)
            .arg("--version")
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status()
            .is_ok();
        if available {
            info!(binary = %binary, "provider CLI found");
        } else {
            warn!(
                binary = %binary,
                "provider CLI not found on PATH — sessions using this provider will fail"
            );
        }
    }

    let storage = Arc::new(if no_migrate {
        clawd::storage::Storage::new_no_migrate(&config.data_dir).await?
    } else {
        Storage::new_with_slow_query(
            &config.data_dir,
            config.observability.slow_query_threshold_ms,
        )
        .await?
    });

    // ── Apply SQLite WAL tuning (Sprint Z — Z.3) ─────────────────────────────
    if let Err(e) = clawd::perf::wal_tuning::apply_wal_tuning(storage.pool()).await {
        warn!(err = %e, "SQLite WAL tuning failed (non-fatal)");
    }

    let daemon_id = match identity::get_or_create(&storage).await {
        Ok(id) => {
            info!(daemon_id = %id, "daemon identity ready");
            id
        }
        Err(e) => {
            warn!("failed to get daemon_id: {e:#}; proceeding without identity");
            String::new()
        }
    };

    let broadcaster = Arc::new(EventBroadcaster::new());
    let repo_registry = Arc::new(RepoRegistry::new(broadcaster.clone()));
    let session_manager = Arc::new(SessionManager::new(
        storage.clone(),
        broadcaster.clone(),
        config.data_dir.clone(),
    ));

    let recovered = storage.recover_stale_sessions().await.unwrap_or(0);
    if recovered > 0 {
        info!(
            count = recovered,
            "recovered stale sessions from previous run"
        );
    }

    let license_info = license::verify_and_cache(&storage, &config, &daemon_id).await;
    let tier = license_info.tier.clone();
    let license = Arc::new(tokio::sync::RwLock::new(license_info));

    {
        let storage = storage.clone();
        let config = config.clone();
        let daemon_id = daemon_id.clone();
        let license = license.clone();
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(std::time::Duration::from_secs(24 * 60 * 60));
            interval.tick().await;
            loop {
                interval.tick().await;
                let info = license::verify_and_cache(&storage, &config, &daemon_id).await;
                *license.write().await = info;
            }
        });
    }

    // ── DB pruning + vacuum (daily, offset 1 h to stagger with license check) ─
    {
        let storage = storage.clone();
        let prune_days = config.session_prune_days;
        tokio::spawn(async move {
            // First run after 1 hour, then every 24 hours
            tokio::time::sleep(std::time::Duration::from_secs(60 * 60)).await;
            let mut consecutive_prune_failures: u32 = 0;
            loop {
                match storage.prune_old_sessions(prune_days).await {
                    Ok(n) if n > 0 => {
                        consecutive_prune_failures = 0;
                        info!(pruned = n, days = prune_days, "pruned old sessions");
                    }
                    Ok(_) => {
                        consecutive_prune_failures = 0;
                    }
                    Err(e) => {
                        consecutive_prune_failures += 1;
                        if consecutive_prune_failures >= 3 {
                            warn!(
                                err = %e,
                                failures = consecutive_prune_failures,
                                "session pruning failing repeatedly"
                            );
                        } else {
                            warn!(err = %e, "session pruning failed");
                        }
                    }
                }
                if let Err(e) = storage.vacuum().await {
                    warn!(err = %e, "sqlite vacuum failed");
                }
                tokio::time::sleep(std::time::Duration::from_secs(24 * 60 * 60)).await;
            }
        });
    }

    let telemetry = Arc::new(telemetry::spawn(config.clone(), daemon_id.clone(), tier));

    let account_registry = Arc::new(AccountRegistry::new(storage.clone(), broadcaster.clone()));
    let updater = Arc::new(update::spawn(config.clone(), broadcaster.clone()));

    let auth_token = match auth::get_or_create_token(&config.data_dir) {
        Ok(t) => {
            info!("auth token ready");
            t
        }
        Err(e) => {
            // Auth token is required — running without it leaves the daemon fully open.
            // This is a startup configuration error, not a recoverable condition.
            eprintln!("FATAL: failed to generate auth token: {e:#}");
            std::process::exit(1);
        }
    };
    // Warn if auth_token file has incorrect permissions (DC.T42).
    auth::check_token_permissions(&config.data_dir);

    // ── Task storage (shared pool from main storage) ──────────────────────────
    let task_storage = Arc::new(TaskStorage::new(storage.clone_pool()));

    // ── Token tracker (Phase 61 MI.T05) ──────────────────────────────────────
    let token_tracker = TokenTracker::new(storage.clone());

    // ── Phase 43c/43m: worktree manager + scheduler components ───────────────
    let worktree_manager =
        std::sync::Arc::new(clawd::worktree::WorktreeManager::new(&config.data_dir));
    let account_pool = std::sync::Arc::new(clawd::scheduler::accounts::AccountPool::new());
    let rate_limit_tracker =
        std::sync::Arc::new(clawd::scheduler::rate_limits::RateLimitTracker::new());
    let fallback_engine = std::sync::Arc::new(clawd::scheduler::fallback::FallbackEngine::new(
        std::sync::Arc::clone(&account_pool),
        std::sync::Arc::clone(&rate_limit_tracker),
    ));
    let scheduler_queue = std::sync::Arc::new(clawd::scheduler::queue::SchedulerQueue::new());

    // ── Version bump watcher (D64.T16) ───────────────────────────────────────
    let version_watcher = std::sync::Arc::new(clawd::doctor::version_watcher::VersionWatcher::new(
        broadcaster.clone(),
    ));

    // Retain a handle for post-shutdown WAL checkpoint (Sprint Z).
    let storage_for_shutdown = storage.clone();

    // ── Stores for memory and metrics (Sprint OO/PP) ─────────────────────────
    let memory_store = clawd::memory::MemoryStore::new(storage.clone_pool());
    let metrics_store = clawd::metrics::MetricsStore::new(storage.clone_pool());
    if let Err(e) = memory_store.migrate().await {
        warn!(err = %e, "memory store migration failed");
    }
    if let Err(e) = metrics_store.migrate().await {
        warn!(err = %e, "metrics store migration failed");
    }

    // ── Connectivity (Sprint JJ) ──────────────────────────────────────────────
    let quality = clawd::connectivity::new_shared_quality();
    let peer_registry = clawd::connectivity::direct::new_registry();

    let ctx = Arc::new(AppContext {
        config: config.clone(),
        storage,
        broadcaster: broadcaster.clone(),
        repo_registry,
        session_manager,
        daemon_id: daemon_id.clone(),
        license: license.clone(),
        telemetry,
        account_registry,
        updater,
        auth_token,
        started_at: std::time::Instant::now(),
        task_storage: task_storage.clone(),
        worktree_manager,
        account_pool,
        rate_limit_tracker,
        fallback_engine,
        scheduler_queue,
        orchestrator: std::sync::Arc::new(clawd::agents::orchestrator::Orchestrator::new()),
        token_tracker,
        metrics: std::sync::Arc::new(clawd::metrics::DaemonMetrics::new()),
        version_watcher: version_watcher.clone(),
        ide_bridge: clawd::ide::new_shared_bridge(),
        provider_sessions: clawd::agents::provider_session::new_shared_registry(),
        recovery_mode: no_migrate,
        automation_engine: clawd::automations::engine::AutomationEngine::new(
            clawd::automations::builtins::all(),
        ),
        quality,
        peer_registry,
        memory_store,
        metrics_store,
    });

    // ── Spawn automation engine dispatcher (Sprint CC CA.1) ──────────────────
    {
        let engine = Arc::clone(&ctx.automation_engine);
        let ctx_for_auto = (*ctx).clone();
        clawd::automations::engine::AutomationEngine::start_dispatcher(engine, ctx_for_auto);
    }

    // ── Spawn version bump watcher (D64.T16) ─────────────────────────────────
    version_watcher.spawn();

    // ── Spawn task background jobs ────────────────────────────────────────────
    {
        let ts = task_storage.clone();
        let bc = broadcaster.clone();
        tokio::spawn(clawd::tasks::jobs::run_heartbeat_checker(ts, bc, 90));
    }
    {
        let ts = task_storage.clone();
        tokio::spawn(clawd::tasks::jobs::run_done_task_archiver(ts, 24));
    }
    {
        let ts = task_storage.clone();
        tokio::spawn(clawd::tasks::jobs::run_activity_log_pruner(ts, 30));
    }

    // ── Lease janitor — release expired task leases every 30s (LH.T03) ─────
    {
        let storage = ctx.storage.clone();
        tokio::spawn(clawd::tasks::janitor::run_lease_janitor(storage));
    }

    // ── Background drift scanner (V02.T25) ───────────────────────────────────
    clawd::drift::background::spawn(ctx.storage.clone(), broadcaster.clone());

    // ── mDNS advertisement ────────────────────────────────────────────────────
    // Non-blocking: if mDNS fails (e.g. system restriction), daemon continues.
    let _mdns_guard = mdns::advertise(&daemon_id, config.port);

    // ── .claw/ AFS structure validation (Phase 43i) ───────────────────────────
    // Validate the .claw/ directory structure in the daemon's data dir.
    // Missing items are warned but never fatal — the daemon starts regardless.
    {
        let missing = clawd::claw_init::validate_claw_dir(&ctx.config.data_dir).await;
        if !missing.is_empty() {
            warn!(
                missing = ?missing,
                "missing .claw/ structure — run `clawd init-claw` to fix"
            );
        }
    }

    // Spawn relay AFTER ctx is built so it can dispatch inbound RPC frames
    // through the full IPC handler and forward push events to remote clients.
    {
        let lic = license.read().await;
        relay::spawn_if_enabled(config, &lic, daemon_id, ctx.clone()).await;
    }

    let run_result = clawd::ipc::run(ctx).await;

    // ── WAL checkpoint on clean shutdown (Sprint Z — Z.3) ────────────────────
    if let Err(e) =
        clawd::perf::wal_tuning::checkpoint_wal(storage_for_shutdown.pool(), "TRUNCATE").await
    {
        tracing::warn!(err = %e, "WAL checkpoint on shutdown failed (non-fatal)");
    }

    run_result
}
