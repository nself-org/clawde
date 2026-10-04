//! Mutation-gap tests for the [`SessionManager`] lifecycle (P7-HYG-37).
//!
//! Each test names the surviving mutants of scheduled run 36402849254 it kills.
//! The manager's methods mostly return `Result<()>`, so a `-> Ok(())` mutant is
//! only killed by observing a side effect: the stored status, the runner call
//! or the broadcast event. A [`FakeRunner`] stands in for a provider CLI so no
//! subprocess is spawned, and it is installed through the private `handles`
//! map, which a child module can reach.
//!
//! The `cursor` provider is used wherever a session must be created through
//! the manager, because `check_provider_ready` only probes the `claude` and
//! `codex` CLIs.

use super::*;
use async_trait::async_trait;
use std::sync::Mutex;

/// Records every call so tests can assert the manager forwarded it.
#[derive(Default)]
struct FakeRunner {
    calls: Mutex<Vec<&'static str>>,
}

impl FakeRunner {
    fn calls(&self) -> Vec<&'static str> {
        self.calls.lock().unwrap().clone()
    }
}

#[async_trait]
impl Runner for FakeRunner {
    async fn run_turn(&self, _content: &str) -> Result<()> {
        self.calls.lock().unwrap().push("run_turn");
        Ok(())
    }
    async fn send(&self, _content: &str) -> Result<()> {
        self.calls.lock().unwrap().push("send");
        Ok(())
    }
    async fn pause(&self) -> Result<()> {
        self.calls.lock().unwrap().push("pause");
        Ok(())
    }
    async fn resume(&self) -> Result<()> {
        self.calls.lock().unwrap().push("resume");
        Ok(())
    }
    async fn stop(&self) -> Result<()> {
        self.calls.lock().unwrap().push("stop");
        Ok(())
    }
}

struct Fixture {
    manager: SessionManager,
    storage: Arc<Storage>,
    events: tokio::sync::broadcast::Receiver<String>,
    dir: tempfile::TempDir,
}

async fn fixture() -> Fixture {
    let dir = tempfile::tempdir().expect("create tempdir");
    let storage = Arc::new(Storage::new(dir.path()).await.expect("open storage"));
    let broadcaster = Arc::new(EventBroadcaster::new());
    let events = broadcaster.subscribe();
    let manager = SessionManager::new(Arc::clone(&storage), broadcaster, dir.path().join("data"));
    Fixture {
        manager,
        storage,
        events,
        dir,
    }
}

impl Fixture {
    /// Install a [`FakeRunner`] as the live runner for `session_id`.
    async fn attach_runner(&self, session_id: &str) -> Arc<FakeRunner> {
        let runner = Arc::new(FakeRunner::default());
        let dyn_runner: Arc<dyn Runner> = runner.clone();
        self.manager.handles.write().await.insert(
            session_id.to_string(),
            Arc::new(SessionHandle { runner: dyn_runner }),
        );
        runner
    }

    async fn session(&self, provider: &str) -> crate::storage::SessionRow {
        self.storage
            .create_session(provider, "/repo", "t", None)
            .await
            .expect("create session")
    }

    async fn status(&self, id: &str) -> String {
        self.storage
            .get_session(id)
            .await
            .expect("get session")
            .expect("session exists")
            .status
    }

    /// Drain buffered broadcasts into `(method, params)` pairs.
    fn drain_events(&mut self) -> Vec<(String, serde_json::Value)> {
        let mut out = Vec::new();
        while let Ok(raw) = self.events.try_recv() {
            let v: serde_json::Value = serde_json::from_str(&raw).expect("event json");
            out.push((
                v["method"].as_str().unwrap().to_string(),
                v["params"].clone(),
            ));
        }
        out
    }
}

// ─── active_count ───────────────────────────────────────────────────────────

#[tokio::test]
async fn active_count_equals_the_number_of_live_runners() {
    // Kills: SessionManager::active_count -> 0
    let f = fixture().await;
    let a = f.session("cursor").await;
    let b = f.session("cursor").await;
    f.attach_runner(&a.id).await;
    assert_eq!(f.manager.active_count().await, 1);
    f.attach_runner(&b.id).await;
    assert_eq!(f.manager.active_count().await, 2);
}

// ─── create ─────────────────────────────────────────────────────────────────

#[tokio::test]
async fn create_with_an_explicit_provider_is_stored_unrouted_and_broadcast() {
    // Kills: create `provider == "auto"` -> `!=` (an explicit provider would be
    // auto-routed and gain a routed_provider) and `delete match arm
    // "claude" | "codex" | "cursor"` (cursor would be rejected).
    let mut f = fixture().await;
    let repo = f.dir.path().join("repo");
    std::fs::create_dir_all(&repo).unwrap();

    let view = f
        .manager
        .create(
            "cursor",
            repo.to_str().unwrap(),
            "My title",
            0,
            Some(vec!["file_read".to_string()]),
            None,
        )
        .await
        .expect("create cursor session");

    assert_eq!(view.provider, "cursor");
    assert_eq!(view.routed_provider, None);
    assert_eq!(view.title, "My title");
    assert_eq!(view.repo_path, repo.to_str().unwrap());
    assert_eq!(view.permissions, Some(vec!["file_read".to_string()]));
    assert_eq!(view.status, "idle");
    // The row really exists.
    assert!(f.storage.get_session(&view.id).await.unwrap().is_some());
    let events = f.drain_events();
    assert!(
        events.iter().any(|(m, p)| m == "session.statusChanged"
            && p["sessionId"] == view.id.as_str()
            && p["status"] == "idle"),
        "create must announce the new session, got {events:?}"
    );
}

#[tokio::test]
async fn create_rejects_an_unknown_provider() {
    let f = fixture().await;
    let err = f
        .manager
        .create("gemini", "/repo", "t", 0, None, None)
        .await
        .expect_err("unknown provider must be rejected");
    assert!(err.to_string().contains("PROVIDER_NOT_AVAILABLE"), "{err}");
    assert_eq!(f.storage.count_sessions().await.unwrap(), 0);
}

#[tokio::test]
async fn create_enforces_the_session_limit_exactly() {
    // Kills: `max_sessions > 0` -> `<` / `==` (limit never enforced);
    // `count >= max` -> `<` (limit inverted).
    let f = fixture().await;
    f.manager
        .create("cursor", "/r", "1", 2, None, None)
        .await
        .expect("first fits");
    f.manager
        .create("cursor", "/r", "2", 2, None, None)
        .await
        .expect("second fits");

    let err = f
        .manager
        .create("cursor", "/r", "3", 2, None, None)
        .await
        .expect_err("third exceeds the limit of 2");
    assert!(
        err.to_string().contains("session limit reached (2 max)"),
        "{err}"
    );
    assert_eq!(f.storage.count_sessions().await.unwrap(), 2);
}

#[tokio::test]
async fn create_with_a_zero_limit_means_unlimited() {
    // Kills: `max_sessions > 0` -> `>=` (zero would reject every create).
    let f = fixture().await;
    for i in 0..3 {
        f.manager
            .create("cursor", "/r", &format!("s{i}"), 0, None, None)
            .await
            .expect("max_sessions = 0 disables the limit");
    }
    assert_eq!(f.storage.count_sessions().await.unwrap(), 3);
}

// ─── list / get / get_messages ──────────────────────────────────────────────

#[tokio::test]
async fn list_returns_every_session_newest_first() {
    // Kills: SessionManager::list -> Ok(vec![])
    let f = fixture().await;
    let old = f.session("claude").await;
    let new = f.session("codex").await;
    for (id, ts) in [
        (&old.id, "2020-01-01T00:00:00Z"),
        (&new.id, "2021-01-01T00:00:00Z"),
    ] {
        sqlx::query("UPDATE sessions SET created_at = ? WHERE id = ?")
            .bind(ts)
            .bind(id)
            .execute(f.storage.pool())
            .await
            .unwrap();
    }

    let views = f.manager.list().await.unwrap();
    let ids: Vec<&str> = views.iter().map(|v| v.id.as_str()).collect();
    assert_eq!(ids, vec![new.id.as_str(), old.id.as_str()]);
    assert_eq!(views[0].provider, "codex");
}

#[tokio::test]
async fn get_messages_returns_the_stored_messages_oldest_first() {
    // Kills: SessionManager::get_messages -> Ok(vec![])
    let f = fixture().await;
    let s = f.session("claude").await;
    f.storage
        .create_message(&s.id, "user", "first", "done")
        .await
        .unwrap();
    f.storage
        .create_message(&s.id, "assistant", "second", "done")
        .await
        .unwrap();

    let msgs = f.manager.get_messages(&s.id, 10, None).await.unwrap();
    let got: Vec<(&str, &str)> = msgs
        .iter()
        .map(|m| (m.role.as_str(), m.content.as_str()))
        .collect();
    assert_eq!(got, vec![("user", "first"), ("assistant", "second")]);

    let err = f
        .manager
        .get_messages("nope", 10, None)
        .await
        .expect_err("unknown session");
    assert!(err.to_string().contains("SESSION_NOT_FOUND"), "{err}");
}

// ─── delete / pause / resume / cancel ───────────────────────────────────────

#[tokio::test]
async fn delete_removes_the_row_and_stops_the_live_runner() {
    // Kills: SessionManager::delete -> Ok(())
    let f = fixture().await;
    let s = f.session("claude").await;
    let keep = f.session("claude").await;
    let runner = f.attach_runner(&s.id).await;

    f.manager.delete(&s.id).await.expect("delete");

    assert!(f.storage.get_session(&s.id).await.unwrap().is_none());
    assert!(f.storage.get_session(&keep.id).await.unwrap().is_some());
    assert_eq!(f.manager.active_count().await, 0);
    assert_eq!(runner.calls(), vec!["stop"]);

    let err = f.manager.delete("nope").await.expect_err("unknown session");
    assert!(err.to_string().contains("SESSION_NOT_FOUND"), "{err}");
}

#[tokio::test]
async fn pause_marks_the_session_paused_forwards_to_the_runner_and_broadcasts() {
    // Kills: SessionManager::pause -> Ok(())
    let mut f = fixture().await;
    let s = f.session("claude").await;
    let runner = f.attach_runner(&s.id).await;

    f.manager.pause(&s.id).await.expect("pause");

    assert_eq!(f.status(&s.id).await, "paused");
    assert_eq!(runner.calls(), vec!["pause"]);
    assert!(f
        .drain_events()
        .iter()
        .any(|(m, p)| m == "session.statusChanged"
            && p["sessionId"] == s.id.as_str()
            && p["status"] == "paused"));
}

#[tokio::test]
async fn resume_returns_to_running_only_when_paused_with_a_live_runner() {
    // Kills: SessionManager::resume -> Ok(()); `session.status == "paused"`
    // -> `!=`; `&&` -> `||`.
    let mut f = fixture().await;

    // paused + runner -> running
    let a = f.session("claude").await;
    f.storage
        .update_session_status(&a.id, "paused")
        .await
        .unwrap();
    let runner = f.attach_runner(&a.id).await;
    f.manager.resume(&a.id).await.expect("resume");
    assert_eq!(f.status(&a.id).await, "running");
    assert_eq!(runner.calls(), vec!["resume"]);
    assert!(f
        .drain_events()
        .iter()
        .any(|(m, p)| m == "session.statusChanged"
            && p["sessionId"] == a.id.as_str()
            && p["status"] == "running"));

    // paused, no runner -> idle
    let b = f.session("claude").await;
    f.storage
        .update_session_status(&b.id, "paused")
        .await
        .unwrap();
    f.manager.resume(&b.id).await.expect("resume");
    assert_eq!(f.status(&b.id).await, "idle");

    // idle + runner -> idle (not paused, so never "running")
    let c = f.session("claude").await;
    let runner_c = f.attach_runner(&c.id).await;
    f.manager.resume(&c.id).await.expect("resume");
    assert_eq!(f.status(&c.id).await, "idle");
    assert_eq!(runner_c.calls(), vec!["resume"]);

    let err = f.manager.resume("nope").await.expect_err("unknown session");
    assert!(err.to_string().contains("SESSION_NOT_FOUND"), "{err}");
}

#[tokio::test]
async fn cancel_idles_the_session_drops_the_runner_and_broadcasts() {
    // Kills: SessionManager::cancel -> Ok(())
    let mut f = fixture().await;
    let s = f.session("claude").await;
    assert!(f.storage.claim_session_for_run(&s.id).await.unwrap());
    assert_eq!(f.status(&s.id).await, "running");
    let runner = f.attach_runner(&s.id).await;

    f.manager.cancel(&s.id).await.expect("cancel");

    assert_eq!(f.status(&s.id).await, "idle");
    assert_eq!(f.manager.active_count().await, 0);
    assert_eq!(runner.calls(), vec!["stop"]);
    assert!(f
        .drain_events()
        .iter()
        .any(|(m, p)| m == "session.statusChanged"
            && p["sessionId"] == s.id.as_str()
            && p["status"] == "idle"));
}

// ─── set_provider ───────────────────────────────────────────────────────────

#[tokio::test]
async fn set_provider_accepts_each_known_provider_and_persists_it() {
    // Kills: SessionManager::set_provider -> Ok(()); `delete match arm
    // "claude" | "codex" | "cursor"`; `session.status == "running"` -> `!=`.
    let mut f = fixture().await;
    for provider in ["claude", "codex", "cursor"] {
        let s = f.session("claude").await;
        let runner = f.attach_runner(&s.id).await;

        f.manager
            .set_provider(&s.id, provider)
            .await
            .unwrap_or_else(|e| panic!("{provider} must be accepted for an idle session: {e}"));

        let row = f.storage.get_session(&s.id).await.unwrap().unwrap();
        assert_eq!(row.routed_provider.as_deref(), Some(provider));
        assert_eq!(
            f.manager.active_count().await,
            0,
            "the old runner must be dropped"
        );
        assert!(
            runner.calls().is_empty(),
            "dropping a handle does not stop it"
        );
        assert!(f
            .drain_events()
            .iter()
            .any(|(m, p)| m == "session.statusChanged"
                && p["sessionId"] == s.id.as_str()
                && p["provider"] == provider));
    }
}

#[tokio::test]
async fn set_provider_rejects_unknown_providers_and_running_sessions() {
    let f = fixture().await;
    let s = f.session("claude").await;

    let err = f
        .manager
        .set_provider(&s.id, "auto")
        .await
        .expect_err("auto is not explicit");
    assert!(err.to_string().contains("unknown provider"), "{err}");

    assert!(f.storage.claim_session_for_run(&s.id).await.unwrap());
    let err = f
        .manager
        .set_provider(&s.id, "codex")
        .await
        .expect_err("a running session cannot switch provider");
    assert!(
        err.to_string().contains("while session is running"),
        "{err}"
    );
    let row = f.storage.get_session(&s.id).await.unwrap().unwrap();
    assert_eq!(
        row.routed_provider, None,
        "a rejected switch must not persist"
    );
}

// ─── drain ──────────────────────────────────────────────────────────────────

#[tokio::test]
async fn drain_stops_every_runner_and_idles_its_session() {
    // Kills: SessionManager::drain -> ()
    let f = fixture().await;
    let a = f.session("claude").await;
    let b = f.session("claude").await;
    for s in [&a, &b] {
        assert!(f.storage.claim_session_for_run(&s.id).await.unwrap());
    }
    let ra = f.attach_runner(&a.id).await;
    let rb = f.attach_runner(&b.id).await;

    f.manager.drain().await;

    assert_eq!(f.manager.active_count().await, 0);
    assert_eq!(ra.calls(), vec!["stop"]);
    assert_eq!(rb.calls(), vec!["stop"]);
    assert_eq!(f.status(&a.id).await, "idle");
    assert_eq!(f.status(&b.id).await, "idle");
}

// ─── approve_tool / reject_tool ─────────────────────────────────────────────

#[tokio::test]
async fn approve_and_reject_tool_record_the_decision_and_broadcast_it() {
    // Kills: SessionManager::approve_tool -> Ok(());
    //        SessionManager::reject_tool -> Ok(())
    let mut f = fixture().await;
    let s = f.session("claude").await;
    let msg = f
        .storage
        .create_message(&s.id, "assistant", "x", "done")
        .await
        .unwrap();
    let yes = f
        .storage
        .create_tool_call(&s.id, &msg.id, "Bash", "{}")
        .await
        .unwrap();
    let no = f
        .storage
        .create_tool_call(&s.id, &msg.id, "Write", "{}")
        .await
        .unwrap();

    f.manager
        .approve_tool(&s.id, &yes.id)
        .await
        .expect("approve");
    f.manager.reject_tool(&s.id, &no.id).await.expect("reject");

    let yes_row = f.storage.get_tool_call(&yes.id).await.unwrap().unwrap();
    let no_row = f.storage.get_tool_call(&no.id).await.unwrap().unwrap();
    assert_eq!(yes_row.status, "approved");
    assert_eq!(no_row.status, "rejected");

    let events = f.drain_events();
    assert!(events.iter().any(|(m, p)| m == "session.toolCallUpdated"
        && p["toolCallId"] == yes.id.as_str()
        && p["status"] == "approved"));
    assert!(events.iter().any(|(m, p)| m == "session.toolCallUpdated"
        && p["toolCallId"] == no.id.as_str()
        && p["status"] == "rejected"));
}
