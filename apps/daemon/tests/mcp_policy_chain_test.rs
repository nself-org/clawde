//! Integration tests for the ADR-003 policy chain on MCP `tools/call` (D-0108).
//!
//! Every denial test asserts the handler counter stayed at zero, and each has
//! an allowed twin so a gate that always denies or never checks would fail.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

use clawd::{
    account::AccountRegistry,
    agents::orchestrator::Orchestrator,
    config::DaemonConfig,
    intelligence::token_tracker::TokenTracker,
    ipc::event::EventBroadcaster,
    license::LicenseInfo,
    mcp::{McpDispatcher, PolicyGate, ServerIdentity},
    policy::mcp_trust::{McpTrustEntry, TrustDatabase, TrustLevel},
    policy::risk::RiskDatabase,
    policy::supply_chain::SupplyChainPolicy,
    policy::PolicyEngine,
    repo::RepoRegistry,
    scheduler::{
        accounts::AccountPool, fallback::FallbackEngine, queue::SchedulerQueue,
        rate_limits::RateLimitTracker,
    },
    session::SessionManager,
    storage::Storage,
    tasks::reducer::TaskState,
    tasks::storage::ActivityQueryParams,
    tasks::TaskStorage,
    telemetry, update,
    worktree::WorktreeManager,
    AppContext,
};
use serde_json::{json, Value};
use tempfile::TempDir;
use tokio::sync::RwLock;

const TRUSTED: &str = "trusted-client";
const SERVER: &str = "clawd-builtin";
const CMD: &str = "/usr/local/bin/clawd";

/// Obviously fake secret that still matches the `sk-` scanner pattern.
fn fake_secret() -> String {
    format!("sk-{}", "FAKE".repeat(9))
}

fn identity() -> ServerIdentity {
    ServerIdentity {
        name: SERVER.into(),
        command: CMD.into(),
        args: vec!["mcp".into()],
    }
}

fn engine(trusted: bool, registered: bool) -> Arc<PolicyEngine> {
    let mut trust = TrustDatabase::empty();
    if trusted {
        trust.upsert(McpTrustEntry {
            server_name: TRUSTED.into(),
            trust: TrustLevel::Trusted,
            command_hash: None,
            allowed_tools: vec![],
        });
    }
    let mut sc = SupplyChainPolicy::empty();
    if registered {
        sc.verify_or_register(SERVER, CMD, &["mcp"]).unwrap();
    }
    Arc::new(PolicyEngine::new(
        Arc::new(RwLock::new(RiskDatabase::default_rules())),
        Arc::new(RwLock::new(trust)),
        Arc::new(sc),
    ))
}

fn gate(trusted: bool, registered: bool) -> PolicyGate {
    PolicyGate::new(engine(trusted, registered), identity())
}

/// Run `guard` with a counting handler; returns (result, handler calls).
async fn run(
    g: &PolicyGate,
    caller: &str,
    tool: &str,
    args: Value,
    state: anyhow::Result<Option<TaskState>>,
    reply: Value,
) -> (anyhow::Result<Value>, usize) {
    let calls = Arc::new(AtomicUsize::new(0));
    let c = calls.clone();
    let res = g
        .guard(caller, tool, args, "agent-1", state, |_a| async move {
            c.fetch_add(1, Ordering::SeqCst);
            Ok(reply)
        })
        .await;
    (res, calls.load(Ordering::SeqCst))
}

fn err_text(r: &anyhow::Result<Value>) -> String {
    r.as_ref().expect_err("expected denial").to_string()
}

#[tokio::test]
async fn allowed_call_reaches_handler() {
    let g = gate(true, true);
    let (res, calls) = run(
        &g,
        TRUSTED,
        "log_event",
        json!({"event_type": "x"}),
        Ok(None),
        json!({"logged": true}),
    )
    .await;
    assert_eq!(res.unwrap(), json!({"logged": true}));
    assert_eq!(calls, 1);
}

#[tokio::test]
async fn untrusted_caller_never_reaches_handler() {
    let g = gate(false, true);
    let (res, calls) = run(&g, TRUSTED, "log_event", json!({}), Ok(None), json!({})).await;
    assert!(err_text(&res).starts_with("MCP_UNKNOWN_ENTITY:"));
    assert_eq!(calls, 0);
    let (res, calls) = run(
        &gate(true, true),
        "",
        "log_event",
        json!({}),
        Ok(None),
        json!({}),
    )
    .await;
    assert!(err_text(&res).starts_with("MCP_UNKNOWN_ENTITY:"));
    assert_eq!(calls, 0);
}

#[tokio::test]
async fn engine_deny_never_reaches_handler() {
    let g = gate(true, true);
    let (res, calls) = run(&g, TRUSTED, "run_tests", json!({}), Ok(None), json!({})).await;
    assert!(err_text(&res).contains("policy_denied"));
    assert_eq!(calls, 0);
    // Twin: same tool with an Active task passes the engine and the hook.
    let (res, calls) = run(
        &g,
        TRUSTED,
        "run_tests",
        json!({}),
        Ok(Some(TaskState::Active)),
        json!({"ok": 1}),
    )
    .await;
    assert!(res.is_ok());
    assert_eq!(calls, 1);
}

#[tokio::test]
async fn needs_approval_fails_closed() {
    let g = gate(true, true);
    let (res, calls) = run(
        &g,
        TRUSTED,
        "apply_patch",
        json!({}),
        Ok(Some(TaskState::Active)),
        json!({}),
    )
    .await;
    assert!(err_text(&res).contains("approval_required"));
    assert_eq!(calls, 0);
}

#[tokio::test]
async fn unregistered_or_changed_server_denied() {
    let (res, calls) = run(
        &gate(true, false),
        TRUSTED,
        "log_event",
        json!({}),
        Ok(None),
        json!({}),
    )
    .await;
    assert!(err_text(&res).starts_with("MCP_SUPPLY_CHAIN_DENIED:"));
    assert_eq!(calls, 0);

    // Registered under a different command: fingerprint mismatch.
    let mut sc = SupplyChainPolicy::empty();
    sc.verify_or_register(SERVER, "/tmp/other", &["mcp"])
        .unwrap();
    let mut trust = TrustDatabase::empty();
    trust.upsert(McpTrustEntry {
        server_name: TRUSTED.into(),
        trust: TrustLevel::Trusted,
        command_hash: None,
        allowed_tools: vec![],
    });
    let e = Arc::new(PolicyEngine::new(
        Arc::new(RwLock::new(RiskDatabase::default_rules())),
        Arc::new(RwLock::new(trust)),
        Arc::new(sc),
    ));
    let g = PolicyGate::new(e, identity());
    let (res, calls) = run(&g, TRUSTED, "log_event", json!({}), Ok(None), json!({})).await;
    assert!(err_text(&res).starts_with("MCP_SUPPLY_CHAIN_DENIED:"));
    assert_eq!(calls, 0);
}

#[tokio::test]
async fn secret_in_args_never_reaches_handler() {
    let g = gate(true, true);
    let bad = json!({"data": {"note": fake_secret()}});
    let (res, calls) = run(&g, TRUSTED, "log_event", bad, Ok(None), json!({})).await;
    assert!(err_text(&res).starts_with("MCP_POLICY_DENIED:"));
    assert_eq!(calls, 0);
}

#[tokio::test]
async fn chain_error_denies() {
    let g = gate(true, true);
    let (res, calls) = run(
        &g,
        TRUSTED,
        "log_event",
        json!({}),
        Err(anyhow::anyhow!("db down")),
        json!({}),
    )
    .await;
    let msg = err_text(&res);
    assert!(msg.contains("MCP_POLICY_DENIED") && msg.contains("chain"));
    assert!(
        !msg.contains("db down"),
        "real cause must not reach the caller"
    );
    assert_eq!(calls, 0);
}

#[tokio::test]
async fn step_order_untrusted_beats_chain_error() {
    let g = gate(false, true);
    let (res, calls) = run(
        &g,
        TRUSTED,
        "log_event",
        json!({}),
        Err(anyhow::anyhow!("db down")),
        json!({}),
    )
    .await;
    let msg = err_text(&res);
    assert!(msg.starts_with("MCP_UNKNOWN_ENTITY:"), "{msg}");
    assert!(!msg.contains("chain_error"));
    assert_eq!(calls, 0);
}

#[tokio::test]
async fn handler_output_is_redacted() {
    let g = gate(true, true);
    let secret = fake_secret();
    let pem = "-----BEGIN PRIVATE KEY-----\nMIIEFAKEBODYFAKEBODY\n-----END PRIVATE KEY-----";
    let reply = json!({
        "note": format!("key {secret}"),
        "password": "hunter2hunter2",
        "nested": [{"Authorization": "Bearer abcdefghijklmnopqrstuvwxyz"}],
        "line": "password=\"abcdefgh1234\"",
        "pem": pem,
        "keep": "hello",
        "count": 3
    });
    let (res, calls) = run(&g, TRUSTED, "log_event", json!({}), Ok(None), reply).await;
    assert_eq!(calls, 1);
    let out = res.unwrap();
    let text = out.to_string();
    for leak in [
        secret.as_str(),
        "hunter2hunter2",
        "abcdefgh1234",
        "MIIEFAKEBODY",
        "abcdefghijklmnopqrstuvwxyz",
    ] {
        assert!(!text.contains(leak), "leaked {leak}");
    }
    // Shape preserved, not wrapped.
    assert_eq!(out["password"], "[REDACTED]");
    assert_eq!(out["pem"], "[REDACTED: private key]");
    assert_eq!(out["keep"], "hello");
    assert_eq!(out["count"], 3);
    assert!(out.get("redacted").is_none());
}

#[tokio::test]
async fn handler_errors_propagate_unchanged() {
    let g = gate(true, true);
    let res = g
        .guard(TRUSTED, "log_event", json!({}), "a", Ok(None), |_a| async {
            Err::<Value, _>(anyhow::anyhow!("MCP_INVALID_PARAMS: boom"))
        })
        .await;
    assert_eq!(err_text(&res), "MCP_INVALID_PARAMS: boom");
}

#[test]
fn classify_error_maps_new_prefixes() {
    let e = anyhow::anyhow!("MCP_POLICY_DENIED: policy_denied: nope");
    let m = McpDispatcher::classify_error(&e);
    assert_eq!(
        (m.code, m.message.as_str()),
        (-32003, "policy_denied: nope")
    );
    let e = anyhow::anyhow!("MCP_UNKNOWN_ENTITY: caller 'x' not trusted");
    assert_eq!(McpDispatcher::classify_error(&e).code, -32002);
    let e = anyhow::anyhow!("MCP_SUPPLY_CHAIN_DENIED: bad");
    assert_eq!(McpDispatcher::classify_error(&e).code, -32004);
}

// ─── Real McpDispatcher end to end ───────────────────────────────────────────

/// Build a minimal AppContext for testing (copied from tests/health.rs).
async fn make_test_ctx(dir: &TempDir, port: u16) -> Arc<AppContext> {
    let data_dir = dir.path().to_path_buf();
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

    Arc::new(AppContext {
        config: config.clone(),
        storage: storage.clone(),
        broadcaster,
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
        auth_token: String::new(),
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
            Arc::new(clawd::ipc::event::EventBroadcaster::new()),
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
    })
}

async fn add_task(ctx: &AppContext, id: &str) {
    ctx.task_storage
        .add_task(
            id, "t", None, None, None, None, None, None, None, None, None, None, "/repo",
        )
        .await
        .unwrap();
}

fn code(e: &anyhow::Error) -> i32 {
    McpDispatcher::classify_error(e).code
}

#[tokio::test]
async fn real_dispatcher_denies_untrusted_and_allows_trusted() {
    let dir = TempDir::new().unwrap();
    let ctx = make_test_ctx(&dir, 0).await;
    add_task(&ctx, "T-1").await;
    let args = json!({"task_id": "T-1", "event_type": "note", "data": {"k": "v"}});

    let denied = McpDispatcher::new(ctx.clone(), Arc::new(gate(false, true)), "evil".into());
    let err = denied
        .dispatch("log_event", args.clone(), None)
        .await
        .unwrap_err();
    assert_eq!(code(&err), -32002);

    // Strict allow: the handler ran, returned Ok, and wrote its activity row.
    let ok = McpDispatcher::new(ctx.clone(), Arc::new(gate(true, true)), TRUSTED.into());
    let out = ok
        .dispatch("log_event", args, Some("agent-1".into()))
        .await
        .unwrap();
    assert_eq!(out, json!({"logged": true}));
    let rows = ctx
        .task_storage
        .query_activity(&ActivityQueryParams {
            repo_path: None,
            task_id: Some("T-1".into()),
            agent: None,
            phase: None,
            entry_type: None,
            action: Some("note".into()),
            since: None,
            limit: None,
            offset: None,
        })
        .await
        .unwrap();
    assert_eq!(rows.len(), 1);

    // Trusted but unregistered server: denied before the handler.
    let unreg = McpDispatcher::new(ctx, Arc::new(gate(true, false)), TRUSTED.into());
    let err = unreg
        .dispatch(
            "log_event",
            json!({"task_id": "T-1", "event_type": "n", "data": {}}),
            None,
        )
        .await
        .unwrap_err();
    assert_eq!(code(&err), -32004);
}

#[tokio::test]
async fn real_dispatcher_resolves_task_state_from_storage() {
    let dir = TempDir::new().unwrap();
    let ctx = make_test_ctx(&dir, 0).await;
    add_task(&ctx, "T-2").await; // status "pending"
    let d = McpDispatcher::new(ctx, Arc::new(gate(true, true)), TRUSTED.into());
    // Pending task: the engine denies (-32003), not verify_active_claimed (-32002).
    let err = d
        .dispatch(
            "run_tests",
            json!({"task_id": "T-2", "idempotency_key": "k"}),
            Some("agent-1".into()),
        )
        .await
        .unwrap_err();
    assert_eq!(code(&err), -32003, "{err}");
}

#[tokio::test]
async fn real_dispatcher_write_tool_requires_agent_identity() {
    let dir = TempDir::new().unwrap();
    let ctx = make_test_ctx(&dir, 0).await;
    add_task(&ctx, "T-3").await;
    ctx.task_storage
        .claim_task("T-3", "agent-1", None)
        .await
        .unwrap();
    let d = McpDispatcher::new(ctx, Arc::new(gate(true, true)), TRUSTED.into());
    let args = json!({"task_id": "T-3", "idempotency_key": "k"});

    // Task is Active, so the chain passes; with no agent id the handler is not reached.
    let err = d
        .dispatch("run_tests", args.clone(), None)
        .await
        .unwrap_err();
    assert_eq!(code(&err), -32003, "{err}");
    assert!(err.to_string().contains("agent identity required"));

    // Control: a different agent passes the identity check and is stopped later
    // by the ownership check (-32002), proving the first denial was the identity rule.
    let err = d
        .dispatch("run_tests", args, Some("other-agent".into()))
        .await
        .unwrap_err();
    assert_eq!(code(&err), -32002, "{err}");
}
