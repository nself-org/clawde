//! Behavioural tests for the agent-task store.
//!
//! Same discipline as the other mutation-coverage suites: every write is read
//! back, counts are seeded so the right answer differs from what the arithmetic
//! mutants produce, and booleans/error paths are pinned in both directions.
//!
//! Time is the tricky part here. `now_ts()` is wall-clock, so the tests set
//! `last_heartbeat` / `completed_at` directly with SQL rather than sleeping.
//! That is deliberate: a sleep-based test for a 90-second reclaim window would
//! either take 90 seconds or be a lie.

use super::*;

/// A `TaskStorage` on a real temp database with the production migrations.
///
/// The `TempDir` must outlive the storage — dropping it deletes the database.
async fn test_storage() -> (TaskStorage, tempfile::TempDir) {
    let dir = tempfile::tempdir().expect("tempdir");
    let storage = crate::storage::Storage::new(dir.path())
        .await
        .expect("open storage");
    (TaskStorage::new(storage.clone_pool()), dir)
}

/// Insert a minimal task and return its row.
async fn add(ts: &TaskStorage, id: &str) -> AgentTaskRow {
    ts.add_task(
        id, "title", None, None, None, None, None, None, None, None, None, None, "/repo",
    )
    .await
    .expect("add task")
}

/// Force a column to a value, bypassing the setters (which all stamp "now").
async fn set_col(ts: &TaskStorage, id: &str, col: &str, val: i64) {
    let sql = format!("UPDATE agent_tasks SET {col} = ? WHERE id = ?");
    sqlx::query(&sql)
        .bind(val)
        .bind(id)
        .execute(ts.pool())
        .await
        .unwrap_or_else(|e| panic!("set {col}: {e}"));
}

fn now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .expect("clock")
        .as_secs() as i64
}

// ─── add_task ───────────────────────────────────────────────────────────────

#[tokio::test]
async fn add_task_applies_its_documented_defaults() {
    let (ts, _d) = test_storage().await;
    let t = add(&ts, "t1").await;

    // Each default is a string literal a mutant can swap.
    assert_eq!(t.task_type.as_deref(), Some("code"), "default type");
    assert_eq!(t.severity.as_deref(), Some("medium"), "default severity");
    assert_eq!(t.status, "pending", "a new task starts pending");
    assert_eq!(t.title, "title");
    assert_eq!(t.repo_path, "/repo");
    assert_eq!(t.claimed_by, None, "a new task is unclaimed");

    // Read back independently of the returned value.
    let fetched = ts.get_task("t1").await.expect("get").expect("exists");
    assert_eq!(fetched.task_type.as_deref(), Some("code"));
    assert_eq!(fetched.severity.as_deref(), Some("medium"));
}

#[tokio::test]
async fn add_task_honours_explicit_values_over_the_defaults() {
    let (ts, _d) = test_storage().await;
    let t = ts
        .add_task(
            "t1",
            "title",
            Some("review"),
            Some("P6"),
            Some("g1"),
            None,
            Some("critical"),
            None,
            None,
            None,
            None,
            Some(42),
            "/repo",
        )
        .await
        .expect("add task");

    // Both directions pinned: a mutant that always uses the default dies here,
    // and one that never applies the default dies in the test above.
    assert_eq!(t.task_type.as_deref(), Some("review"));
    assert_eq!(t.severity.as_deref(), Some("critical"));
    assert_eq!(t.phase.as_deref(), Some("P6"));
    assert_eq!(t.estimated_minutes, Some(42));
}

#[tokio::test]
async fn get_task_returns_none_for_an_unknown_id() {
    let (ts, _d) = test_storage().await;
    assert!(ts.get_task("nope").await.expect("get").is_none());
}

// ─── claim_task ─────────────────────────────────────────────────────────────

#[tokio::test]
async fn claim_task_is_exclusive() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;

    let claimed = ts.claim_task("t1", "agent-a", None).await.expect("claim");
    assert_eq!(claimed.status, "in_progress");
    assert_eq!(claimed.claimed_by.as_deref(), Some("agent-a"));

    // A second agent must be refused — this is what kills a mutant that drops
    // the status predicate from the UPDATE.
    let err = ts
        .claim_task("t1", "agent-b", None)
        .await
        .expect_err("second claim must fail");
    assert!(
        err.to_string().contains("TASK_CODE:"),
        "expected a TASK_CODE error, got: {err}"
    );

    // And the first agent still owns it.
    let t = ts.get_task("t1").await.expect("get").expect("exists");
    assert_eq!(t.claimed_by.as_deref(), Some("agent-a"));
}

#[tokio::test]
async fn claim_task_allows_reclaim_of_a_recently_interrupted_task() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;
    ts.claim_task("t1", "agent-a", None).await.expect("claim");

    // Interrupted 10s ago, well inside the default 90s window (x2 = 180s).
    sqlx::query("UPDATE agent_tasks SET status = 'interrupted', last_heartbeat = ? WHERE id = ?")
        .bind(now() - 10)
        .bind("t1")
        .execute(ts.pool())
        .await
        .expect("seed interrupted");

    let t = ts
        .claim_task("t1", "agent-b", None)
        .await
        .expect("a recently interrupted task must be re-claimable");
    assert_eq!(t.claimed_by.as_deref(), Some("agent-b"));
    assert_eq!(t.status, "in_progress");
}

#[tokio::test]
async fn claim_task_rejects_an_interrupted_task_past_double_the_reclaim_window() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;
    ts.claim_task("t1", "agent-a", None).await.expect("claim");

    // Default window is 90s and the guard is `elapsed > window * 2`, so 400s is
    // outside and 100s (below) is inside. Choosing values on both sides of 180
    // is what kills `* 2` -> `* 1` and `> ` -> `>=`.
    sqlx::query("UPDATE agent_tasks SET status = 'interrupted', last_heartbeat = ? WHERE id = ?")
        .bind(now() - 400)
        .bind("t1")
        .execute(ts.pool())
        .await
        .expect("seed stale interrupted");

    let err = ts
        .claim_task("t1", "agent-b", None)
        .await
        .expect_err("a long-interrupted task must not be silently re-claimed");
    let msg = err.to_string();
    assert!(
        msg.contains("re-claim window"),
        "expected the re-claim-window error, got: {msg}"
    );

    // 100s elapsed is inside 180s and must still be allowed.
    sqlx::query("UPDATE agent_tasks SET status = 'interrupted', last_heartbeat = ? WHERE id = ?")
        .bind(now() - 100)
        .bind("t1")
        .execute(ts.pool())
        .await
        .expect("seed recent interrupted");
    ts.claim_task("t1", "agent-b", None)
        .await
        .expect("100s elapsed is inside the 180s window");
}

#[tokio::test]
async fn claim_task_reclaim_window_honours_an_explicit_timeout() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;
    ts.claim_task("t1", "agent-a", None).await.expect("claim");

    // With a 10s timeout the window is 20s, so 100s elapsed is now OUTSIDE it —
    // the same elapsed value that the default 90s accepted above. That pins the
    // parameter as actually used, killing `unwrap_or(90)` -> a constant.
    sqlx::query("UPDATE agent_tasks SET status = 'interrupted', last_heartbeat = ? WHERE id = ?")
        .bind(now() - 100)
        .bind("t1")
        .execute(ts.pool())
        .await
        .expect("seed");

    let err = ts
        .claim_task("t1", "agent-b", Some(10))
        .await
        .expect_err("100s must exceed a 10s timeout's 20s window");
    assert!(err.to_string().contains("re-claim window"), "got: {err}");
}

// ─── update_status ──────────────────────────────────────────────────────────

#[tokio::test]
async fn update_status_refuses_done_without_completion_notes() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;

    for notes in [None, Some("")] {
        let err = ts
            .update_status("t1", "done", notes, None)
            .await
            .expect_err("done without notes must be refused");
        assert!(
            err.to_string().contains("TASK_CODE:"),
            "expected a TASK_CODE error, got: {err}"
        );
    }

    // Still not done — the refusal must not have written anything.
    let t = ts.get_task("t1").await.expect("get").expect("exists");
    assert_ne!(t.status, "done");
    assert_eq!(t.completed_at, None);
}

#[tokio::test]
async fn update_status_accepts_done_with_notes_and_stamps_completion() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;
    ts.claim_task("t1", "agent-a", None).await.expect("claim");

    let t = ts
        .update_status("t1", "done", Some("shipped"), None)
        .await
        .expect("done with notes must be accepted");
    assert_eq!(t.status, "done");
    assert!(
        t.completed_at.is_some(),
        "completing must stamp completed_at"
    );
    assert_eq!(t.notes.as_deref(), Some("shipped"));
}

#[tokio::test]
async fn update_status_does_not_stamp_completed_at_for_a_non_done_status() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;

    let t = ts
        .update_status("t1", "blocked", Some("waiting"), Some("needs owner"))
        .await
        .expect("update");
    assert_eq!(t.status, "blocked");
    assert_eq!(
        t.completed_at, None,
        "only 'done' may set completed_at — both directions pinned"
    );
    assert_eq!(t.block_reason.as_deref(), Some("needs owner"));
}

#[tokio::test]
async fn update_status_computes_actual_minutes_from_started_at() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;
    ts.claim_task("t1", "agent-a", None).await.expect("claim");

    // Started 600s ago -> 10 minutes. An exact value, so `/ 60` -> `/ 1` or
    // `* 60` changes the observed number.
    set_col(&ts, "t1", "started_at", now() - 600).await;

    let t = ts
        .update_status("t1", "done", Some("n"), None)
        .await
        .expect("update");
    let mins = t.actual_minutes.expect("actual_minutes must be computed");
    assert!(
        (9..=11).contains(&mins),
        "600 seconds should be ~10 minutes, got {mins}"
    );
}

// ─── interrupt_stale_tasks ──────────────────────────────────────────────────

#[tokio::test]
async fn interrupt_stale_tasks_marks_only_the_tasks_past_the_timeout() {
    let (ts, _d) = test_storage().await;
    for id in ["stale1", "stale2", "fresh"] {
        add(&ts, id).await;
        ts.claim_task(id, "agent-a", None).await.expect("claim");
    }
    // A pending task with no heartbeat must never be swept.
    add(&ts, "untouched").await;

    set_col(&ts, "stale1", "last_heartbeat", now() - 500).await;
    set_col(&ts, "stale2", "last_heartbeat", now() - 500).await;
    set_col(&ts, "fresh", "last_heartbeat", now() - 5).await;

    let mut ids = ts
        .interrupt_stale_tasks(300)
        .await
        .expect("interrupt stale tasks");
    ids.sort();

    // Exactly two, named — not a count that a `0` or `1` mutant could match.
    assert_eq!(ids, vec!["stale1".to_string(), "stale2".to_string()]);

    for id in ["stale1", "stale2"] {
        assert_eq!(
            ts.get_task(id).await.expect("get").expect("exists").status,
            "interrupted",
            "{id} should have been interrupted"
        );
    }
    assert_eq!(
        ts.get_task("fresh")
            .await
            .expect("get")
            .expect("exists")
            .status,
        "in_progress",
        "a task inside the timeout must be left running"
    );
    assert_eq!(
        ts.get_task("untouched")
            .await
            .expect("get")
            .expect("exists")
            .status,
        "pending",
        "a task with no heartbeat must never be swept"
    );
}

#[tokio::test]
async fn interrupt_stale_tasks_returns_empty_when_nothing_is_stale() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;
    ts.claim_task("t1", "agent-a", None).await.expect("claim");

    let ids = ts.interrupt_stale_tasks(3600).await.expect("interrupt");
    assert!(ids.is_empty(), "nothing is stale, got {ids:?}");
    assert_eq!(
        ts.get_task("t1")
            .await
            .expect("get")
            .expect("exists")
            .status,
        "in_progress"
    );
}

// ─── archive_done_tasks ─────────────────────────────────────────────────────

#[tokio::test]
async fn archive_done_tasks_moves_only_old_done_tasks_and_never_interrupted_ones() {
    let (ts, _d) = test_storage().await;
    for id in ["old_done1", "old_done2", "recent_done", "old_interrupted"] {
        add(&ts, id).await;
        ts.claim_task(id, "agent-a", None).await.expect("claim");
    }

    for id in ["old_done1", "old_done2", "recent_done"] {
        ts.update_status(id, "done", Some("n"), None)
            .await
            .expect("done");
    }
    sqlx::query("UPDATE agent_tasks SET status = 'interrupted', completed_at = ? WHERE id = ?")
        .bind(now() - 90_000)
        .bind("old_interrupted")
        .execute(ts.pool())
        .await
        .expect("seed interrupted");

    // 25h ago is outside a 24h window; the recent one stays inside it.
    set_col(&ts, "old_done1", "completed_at", now() - 90_000).await;
    set_col(&ts, "old_done2", "completed_at", now() - 90_000).await;
    set_col(&ts, "recent_done", "completed_at", now() - 60).await;

    // Exactly two: 2 is distinct from 0, 1, and the 4 tasks present, so a
    // dropped status or age predicate changes the number.
    let n = ts.archive_done_tasks(24).await.expect("archive");
    assert_eq!(n, 2);

    for id in ["old_done1", "old_done2"] {
        assert!(
            ts.get_task(id).await.expect("get").is_none(),
            "{id} should have been removed from agent_tasks"
        );
    }
    assert!(
        ts.get_task("recent_done").await.expect("get").is_some(),
        "a task inside the visibility window must stay"
    );
    assert!(
        ts.get_task("old_interrupted").await.expect("get").is_some(),
        "interrupted tasks are NEVER archived, however old"
    );

    // The rows really landed in the archive rather than just being deleted.
    let archived: (i64,) = sqlx::query_as("SELECT COUNT(*) FROM agent_tasks_archive")
        .fetch_one(ts.pool())
        .await
        .expect("count archive");
    assert_eq!(archived.0, 2, "archiving must copy, not just delete");
}

#[tokio::test]
async fn archive_done_tasks_is_a_no_op_when_nothing_qualifies() {
    let (ts, _d) = test_storage().await;
    add(&ts, "t1").await;

    assert_eq!(ts.archive_done_tasks(24).await.expect("archive"), 0);
    assert!(ts.get_task("t1").await.expect("get").is_some());
}

// ─── prune_activity_log ─────────────────────────────────────────────────────

#[tokio::test]
async fn prune_activity_log_deletes_only_entries_older_than_the_retention() {
    let (ts, _d) = test_storage().await;

    // The fixture has to straddle BOTH cutoffs or it proves nothing. With
    // retention=2 the correct cutoff is 2 days ago; a `* 86400` -> `* 3600`
    // mutant makes it 2 HOURS ago. Entries at 3 days and 1 hour are classified
    // identically by both, so `mid` — 1 day old — is the discriminator: it
    // survives the correct cutoff and is deleted by the mutant's.
    //
    // (Found the hard way: without `mid` this test passed while killing
    // nothing.)
    for (id, ts_val) in [
        ("old1", now() - 3 * 86_400),
        ("old2", now() - 3 * 86_400),
        ("mid", now() - 86_400),
        ("new1", now() - 3_600),
    ] {
        sqlx::query(
            "INSERT INTO agent_activity_log (id, ts, agent, action, repo_path)
             VALUES (?, ?, ?, ?, ?)",
        )
        .bind(id)
        .bind(ts_val)
        .bind("agent-a")
        .bind("note")
        .bind("/repo")
        .execute(ts.pool())
        .await
        .expect("seed activity");
    }

    let removed = ts.prune_activity_log(2).await.expect("prune");
    assert_eq!(removed, 2, "exactly the two entries older than 2 DAYS");

    // Name the survivors, not just the count: the 1-day-old entry surviving is
    // the whole point, and a bare count of 2 would also match a 2-hour cutoff.
    let mut left: Vec<String> = sqlx::query_scalar("SELECT id FROM agent_activity_log ORDER BY id")
        .fetch_all(ts.pool())
        .await
        .expect("list survivors");
    left.sort();
    assert_eq!(
        left,
        vec!["mid".to_string(), "new1".to_string()],
        "everything inside the 2-day retention must survive"
    );
}
