//! Behavioural tests for [`Storage`].
//!
//! These exist to kill surviving mutants, so they are written to a specific
//! discipline rather than for line coverage:
//!
//! * cargo-mutants replaces an `async fn … -> Result<()>` body with `Ok(())`.
//!   A test that only asserts the call returned `Ok` therefore kills nothing —
//!   the no-op mutant returns `Ok` too. **Every write is read back** and the
//!   observed value asserted.
//! * For functions returning a count, the seeded fixture is chosen so the
//!   correct answer differs from what the obvious arithmetic mutants produce
//!   (`a + b` → `a - b`, `a * b`, `0`, `1`).
//! * Booleans are pinned in both directions, so `Ok(true)` and `Ok(false)`
//!   mutants both die.
//!
//! Each test gets its own temporary database via [`test_storage`], so they are
//! order-independent and safe to run in parallel.

use super::*;

/// A real `Storage` on a fresh temp dir, with the production migrations applied.
///
/// The returned `TempDir` must be held for the lifetime of the test — dropping
/// it deletes the database out from under the pool.
async fn test_storage() -> (Storage, tempfile::TempDir) {
    let dir = tempfile::tempdir().expect("create tempdir");
    let storage = Storage::new(dir.path()).await.expect("open storage");
    (storage, dir)
}

/// Force a session's `updated_at` to `value`, bypassing the setters (which all
/// stamp "now"). Used to age rows for the prune tests.
async fn backdate_session(storage: &Storage, id: &str, value: &str) {
    sqlx::query("UPDATE sessions SET updated_at = ? WHERE id = ?")
        .bind(value)
        .bind(id)
        .execute(storage.pool())
        .await
        .expect("backdate session");
}

#[tokio::test]
async fn create_session_persists_every_field_with_idle_defaults() {
    let (storage, _dir) = test_storage().await;

    let row = storage
        .create_session("claude", "/repo/path", "My title", Some("allow:read"))
        .await
        .expect("create session");

    // The returned row and the stored row must agree — create_session re-reads
    // after insert, so a mutant that drops the INSERT cannot fake this.
    assert_eq!(row.provider, "claude");
    assert_eq!(row.repo_path, "/repo/path");
    assert_eq!(row.title, "My title");
    assert_eq!(row.permissions.as_deref(), Some("allow:read"));
    assert_eq!(row.status, "idle", "new sessions must start idle");
    assert_eq!(
        row.message_count, 0,
        "new sessions must start at zero messages"
    );

    let fetched = storage
        .get_session(&row.id)
        .await
        .expect("get session")
        .expect("session should exist after insert");
    assert_eq!(fetched.id, row.id);
    assert_eq!(fetched.title, "My title");
    assert_eq!(fetched.status, "idle");

    // A NULL permissions column must round-trip as None, not as Some("").
    let none_perms = storage
        .create_session("codex", "/other", "No perms", None)
        .await
        .expect("create session without permissions");
    assert_eq!(none_perms.permissions, None);
}

#[tokio::test]
async fn get_session_returns_none_for_an_unknown_id() {
    let (storage, _dir) = test_storage().await;
    let got = storage
        .get_session("no-such-id")
        .await
        .expect("get session");
    assert!(got.is_none(), "unknown id must not resolve to a session");
}

#[tokio::test]
async fn count_sessions_returns_the_exact_row_count() {
    let (storage, _dir) = test_storage().await;

    // Zero distinguishes the real implementation from an `Ok(1)` mutant.
    assert_eq!(storage.count_sessions().await.expect("count"), 0);

    for i in 0..3 {
        storage
            .create_session("claude", "/r", &format!("s{i}"), None)
            .await
            .expect("create session");
    }

    // Three distinguishes it from both `Ok(0)` and `Ok(1)`.
    assert_eq!(storage.count_sessions().await.expect("count"), 3);
}

#[tokio::test]
async fn claim_session_for_run_is_exclusive_and_gated_on_status() {
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "claimable", None)
        .await
        .expect("create session");

    // A fresh (idle) session can be claimed exactly once.
    assert!(
        storage.claim_session_for_run(&s.id).await.expect("claim"),
        "an idle session must be claimable"
    );
    assert_eq!(
        storage
            .get_session(&s.id)
            .await
            .expect("get")
            .expect("exists")
            .status,
        "running",
        "a successful claim must leave the session running"
    );

    // The second claim must fail: it is now running. This is what kills the
    // `rows_affected() >= 0` mutant, which would report success every time.
    assert!(
        !storage.claim_session_for_run(&s.id).await.expect("claim"),
        "a running session must not be claimable a second time"
    );

    // Paused is likewise not claimable.
    storage
        .update_session_status(&s.id, "paused")
        .await
        .expect("set paused");
    assert!(
        !storage.claim_session_for_run(&s.id).await.expect("claim"),
        "a paused session must not be claimable"
    );

    // Error is claimable — it is a retry, not a live turn.
    storage
        .update_session_status(&s.id, "error")
        .await
        .expect("set error");
    assert!(
        storage.claim_session_for_run(&s.id).await.expect("claim"),
        "an errored session must be reclaimable"
    );

    // An id that does not exist affects no rows.
    assert!(
        !storage
            .claim_session_for_run("no-such-id")
            .await
            .expect("claim"),
        "claiming an unknown session must fail"
    );
}

#[tokio::test]
async fn update_session_status_writes_the_value_back() {
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .expect("create session");

    storage
        .update_session_status(&s.id, "waiting")
        .await
        .expect("update status");

    // Reading back is what kills the `Ok(())` no-op mutant.
    assert_eq!(
        storage
            .get_session(&s.id)
            .await
            .expect("get")
            .expect("exists")
            .status,
        "waiting"
    );
}

#[tokio::test]
async fn increment_message_count_advances_by_exactly_one() {
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .expect("create session");
    assert_eq!(s.message_count, 0);

    storage
        .increment_message_count(&s.id)
        .await
        .expect("increment");
    assert_eq!(
        storage
            .get_session(&s.id)
            .await
            .expect("get")
            .expect("exists")
            .message_count,
        1,
        "one increment must land exactly one message"
    );

    storage
        .increment_message_count(&s.id)
        .await
        .expect("increment");
    assert_eq!(
        storage
            .get_session(&s.id)
            .await
            .expect("get")
            .expect("exists")
            .message_count,
        2,
        "increments must accumulate, not overwrite"
    );
}

#[tokio::test]
async fn delete_session_removes_only_the_named_session() {
    let (storage, _dir) = test_storage().await;
    let keep = storage
        .create_session("claude", "/r", "keep", None)
        .await
        .expect("create");
    let drop = storage
        .create_session("claude", "/r", "drop", None)
        .await
        .expect("create");

    storage.delete_session(&drop.id).await.expect("delete");

    assert!(
        storage.get_session(&drop.id).await.expect("get").is_none(),
        "the deleted session must be gone"
    );
    assert!(
        storage.get_session(&keep.id).await.expect("get").is_some(),
        "an unrelated session must survive"
    );
    assert_eq!(storage.count_sessions().await.expect("count"), 1);
}

#[tokio::test]
async fn recover_stale_sessions_counts_crashed_plus_paused_and_rewrites_status() {
    let (storage, _dir) = test_storage().await;

    // Fixture chosen so the correct answer (2 + 1 = 3) differs from every
    // obvious arithmetic mutant: 2 - 1 = 1, 2 * 1 = 2, and the constants 0/1.
    let running = storage
        .create_session("claude", "/r", "running", None)
        .await
        .expect("create");
    let waiting = storage
        .create_session("claude", "/r", "waiting", None)
        .await
        .expect("create");
    let paused = storage
        .create_session("claude", "/r", "paused", None)
        .await
        .expect("create");
    let untouched = storage
        .create_session("claude", "/r", "idle", None)
        .await
        .expect("create");

    for (id, status) in [
        (&running.id, "running"),
        (&waiting.id, "waiting"),
        (&paused.id, "paused"),
    ] {
        storage
            .update_session_status(id, status)
            .await
            .expect("seed status");
    }

    let recovered = storage
        .recover_stale_sessions()
        .await
        .expect("recover stale sessions");
    assert_eq!(
        recovered, 3,
        "two crashed (running + waiting) plus one paused must total three"
    );

    // Crashed sessions become errors; a paused session resumes as idle.
    let status_of = |id: String| {
        let storage = storage.clone();
        async move {
            storage
                .get_session(&id)
                .await
                .expect("get")
                .expect("exists")
                .status
        }
    };
    assert_eq!(status_of(running.id.clone()).await, "error");
    assert_eq!(status_of(waiting.id.clone()).await, "error");
    assert_eq!(status_of(paused.id.clone()).await, "idle");
    assert_eq!(
        status_of(untouched.id.clone()).await,
        "idle",
        "an already-idle session must be left alone"
    );
}

#[tokio::test]
async fn prune_old_sessions_with_zero_days_is_a_no_op() {
    let (storage, _dir) = test_storage().await;
    let ancient = storage
        .create_session("claude", "/r", "ancient", None)
        .await
        .expect("create");
    backdate_session(&storage, &ancient.id, "2000-01-01T00:00:00+00:00").await;

    // `0` means "never prune". A mutant flipping `days == 0` to `days != 0`
    // would fall through and delete this row.
    assert_eq!(storage.prune_old_sessions(0).await.expect("prune"), 0);
    assert!(
        storage
            .get_session(&ancient.id)
            .await
            .expect("get")
            .is_some(),
        "prune(0) must not delete anything, however old"
    );
}

#[tokio::test]
async fn prune_old_sessions_deletes_only_aged_idle_and_error_rows() {
    let (storage, _dir) = test_storage().await;

    let old_idle = storage
        .create_session("claude", "/r", "old idle", None)
        .await
        .expect("create");
    let old_error = storage
        .create_session("claude", "/r", "old error", None)
        .await
        .expect("create");
    let old_running = storage
        .create_session("claude", "/r", "old running", None)
        .await
        .expect("create");
    let recent_idle = storage
        .create_session("claude", "/r", "recent idle", None)
        .await
        .expect("create");

    storage
        .update_session_status(&old_error.id, "error")
        .await
        .expect("seed error");
    storage
        .update_session_status(&old_running.id, "running")
        .await
        .expect("seed running");

    // Age the three "old" rows well past the cutoff. This happens after the
    // status writes, which would otherwise re-stamp updated_at to now.
    for id in [&old_idle.id, &old_error.id, &old_running.id] {
        backdate_session(&storage, id, "2000-01-01T00:00:00+00:00").await;
    }

    // Exactly two: the aged idle and the aged error. The aged *running* row is
    // excluded by status, the recent idle row by age — so a mutant that drops
    // either predicate returns 3 rather than 2.
    assert_eq!(storage.prune_old_sessions(30).await.expect("prune"), 2);

    assert!(storage
        .get_session(&old_idle.id)
        .await
        .expect("get")
        .is_none());
    assert!(storage
        .get_session(&old_error.id)
        .await
        .expect("get")
        .is_none());
    assert!(
        storage
            .get_session(&old_running.id)
            .await
            .expect("get")
            .is_some(),
        "a running session must never be pruned, however old"
    );
    assert!(
        storage
            .get_session(&recent_idle.id)
            .await
            .expect("get")
            .is_some(),
        "a session inside the retention window must survive"
    );
}

#[tokio::test]
async fn settings_round_trip_and_upsert_overwrites_in_place() {
    let (storage, _dir) = test_storage().await;

    assert_eq!(
        storage.get_setting("theme").await.expect("get"),
        None,
        "an unset key must read as None"
    );

    storage.set_setting("theme", "dark").await.expect("set");
    assert_eq!(
        storage.get_setting("theme").await.expect("get"),
        Some("dark".to_string())
    );

    // ON CONFLICT DO UPDATE: the second write replaces rather than duplicating
    // or being rejected.
    storage.set_setting("theme", "light").await.expect("set");
    assert_eq!(
        storage.get_setting("theme").await.expect("get"),
        Some("light".to_string()),
        "re-setting a key must overwrite the previous value"
    );

    // An unrelated key is untouched by the upsert.
    storage.set_setting("locale", "en").await.expect("set");
    assert_eq!(
        storage.get_setting("theme").await.expect("get"),
        Some("light".to_string())
    );
    assert_eq!(
        storage.get_setting("locale").await.expect("get"),
        Some("en".to_string())
    );
}

#[tokio::test]
async fn license_cache_round_trips_every_field_and_upserts_the_single_row() {
    let (storage, _dir) = test_storage().await;

    assert!(
        storage.get_license_cache().await.expect("get").is_none(),
        "no license should be cached on a fresh database"
    );

    storage
        .set_license_cache(
            "pro",
            r#"{"relay":true}"#,
            "2026-03-01T00:00:00+00:00",
            "2026-04-01T00:00:00+00:00",
            Some("deadbeef"),
        )
        .await
        .expect("set license cache");

    let row = storage
        .get_license_cache()
        .await
        .expect("get")
        .expect("license should be cached");
    assert_eq!(row.id, 1, "the cache is a single pinned row");
    assert_eq!(row.tier, "pro");
    assert_eq!(row.features, r#"{"relay":true}"#);
    assert_eq!(row.cached_at, "2026-03-01T00:00:00+00:00");
    assert_eq!(row.valid_until, "2026-04-01T00:00:00+00:00");
    assert_eq!(row.hmac.as_deref(), Some("deadbeef"));

    // A second write must update row 1 in place, including clearing the hmac
    // back to NULL rather than leaving the old digest behind.
    storage
        .set_license_cache(
            "free",
            r#"{"relay":false}"#,
            "2026-05-01T00:00:00+00:00",
            "2026-06-01T00:00:00+00:00",
            None,
        )
        .await
        .expect("overwrite license cache");

    let row = storage
        .get_license_cache()
        .await
        .expect("get")
        .expect("license should still be cached");
    assert_eq!(row.id, 1);
    assert_eq!(row.tier, "free", "tier must be overwritten");
    assert_eq!(row.features, r#"{"relay":false}"#);
    assert_eq!(
        row.hmac, None,
        "a None hmac must clear the column, not retain the stale digest"
    );
}

#[tokio::test]
async fn create_message_persists_fields_and_estimates_tokens() {
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .expect("create session");

    let content = "hello world, this is a message with some length to it";
    let msg = storage
        .create_message(&s.id, "user", content, "complete")
        .await
        .expect("create message");

    assert_eq!(msg.session_id, s.id);
    assert_eq!(msg.role, "user");
    assert_eq!(msg.content, content);
    assert_eq!(msg.status, "complete");
    assert!(!msg.pinned, "messages must not start pinned");

    // Pinning the exact estimate kills a mutant that stores a constant 0.
    assert_eq!(
        msg.token_count,
        crate::intelligence::context::estimate_tokens(content) as i64
    );
    assert!(
        msg.token_count > 0,
        "a non-empty message must estimate above zero tokens"
    );

    let fetched = storage
        .get_message(&msg.id)
        .await
        .expect("get message")
        .expect("message should exist after insert");
    assert_eq!(fetched.content, content);
}

#[tokio::test]
async fn pin_and_unpin_message_toggle_the_flag_in_both_directions() {
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .expect("create session");
    let msg = storage
        .create_message(&s.id, "user", "pin me", "complete")
        .await
        .expect("create message");
    assert!(!msg.pinned);

    storage.pin_message(&msg.id).await.expect("pin");
    assert!(
        storage
            .get_message(&msg.id)
            .await
            .expect("get")
            .expect("exists")
            .pinned,
        "pin_message must set the flag"
    );

    // Both directions are asserted, so neither a stuck-on nor a stuck-off
    // mutant survives.
    storage.unpin_message(&msg.id).await.expect("unpin");
    assert!(
        !storage
            .get_message(&msg.id)
            .await
            .expect("get")
            .expect("exists")
            .pinned,
        "unpin_message must clear the flag"
    );
}

#[tokio::test]
async fn list_messages_returns_the_last_limit_in_chronological_order() {
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .expect("create session");

    // Distinct timestamps make the ordering deterministic rather than relying
    // on how the (created_at, id) tie-break happens to fall for random UUIDs.
    let mut ids = Vec::new();
    for i in 0..5 {
        let m = storage
            .create_message(&s.id, "user", &format!("m{i}"), "complete")
            .await
            .expect("create message");
        ids.push(m.id);
        tokio::time::sleep(std::time::Duration::from_millis(5)).await;
    }

    let page = storage
        .list_messages(&s.id, 3, None)
        .await
        .expect("list messages");

    // The *last* three, oldest-first — not the first three, and not all five.
    let contents: Vec<&str> = page.iter().map(|m| m.content.as_str()).collect();
    assert_eq!(contents, vec!["m2", "m3", "m4"]);
}

#[tokio::test]
async fn list_messages_before_a_cursor_returns_only_strictly_earlier_rows() {
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .expect("create session");

    let mut ids = Vec::new();
    for i in 0..5 {
        let m = storage
            .create_message(&s.id, "user", &format!("m{i}"), "complete")
            .await
            .expect("create message");
        ids.push(m.id);
        tokio::time::sleep(std::time::Duration::from_millis(5)).await;
    }

    // Everything strictly before m2 — the cursor row itself must be excluded.
    let page = storage
        .list_messages(&s.id, 10, Some(&ids[2]))
        .await
        .expect("list messages before cursor");
    let contents: Vec<&str> = page.iter().map(|m| m.content.as_str()).collect();
    assert_eq!(contents, vec!["m0", "m1"]);
}

#[tokio::test]
async fn list_messages_is_scoped_to_its_own_session() {
    let (storage, _dir) = test_storage().await;
    let a = storage
        .create_session("claude", "/r", "a", None)
        .await
        .expect("create session");
    let b = storage
        .create_session("claude", "/r", "b", None)
        .await
        .expect("create session");

    storage
        .create_message(&a.id, "user", "in a", "complete")
        .await
        .expect("create message");
    storage
        .create_message(&b.id, "user", "in b", "complete")
        .await
        .expect("create message");

    let page = storage
        .list_messages(&a.id, 10, None)
        .await
        .expect("list messages");
    let contents: Vec<&str> = page.iter().map(|m| m.content.as_str()).collect();
    assert_eq!(
        contents,
        vec!["in a"],
        "a session's history must not leak another session's messages"
    );
}
// ─── Tool calls ──────────────────────────────────────────────────────────────

/// Every column written by `create_tool_call` is asserted, including the ones
/// the function supplies rather than takes: the generated id, the 'pending'
/// status literal and the created_at timestamp. A row-count assertion would
/// pass even if the wrong value reached the wrong column.
#[tokio::test]
async fn create_tool_call_persists_every_field_and_starts_pending() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "tool calls", None)
        .await
        .unwrap();
    let message = storage
        .create_message(&session.id, "assistant", "calling a tool", "complete")
        .await
        .unwrap();

    let call = storage
        .create_tool_call(&session.id, &message.id, "Bash", r#"{"command":"ls"}"#)
        .await
        .unwrap();

    assert!(!call.id.is_empty());
    assert_eq!(call.session_id, session.id);
    assert_eq!(call.message_id, message.id);
    assert_eq!(call.name, "Bash");
    assert_eq!(call.input, r#"{"command":"ls"}"#);
    assert_eq!(call.status, "pending");
    assert_eq!(call.output, None);
    assert_eq!(call.completed_at, None);
    assert!(!call.created_at.is_empty());

    // Read it back rather than trusting the returned struct: the insert and
    // the value handed to the caller are two different things.
    let stored = storage.get_tool_call(&call.id).await.unwrap().unwrap();
    assert_eq!(stored.name, "Bash");
    assert_eq!(stored.status, "pending");
    assert_eq!(stored.output, None);
}

#[tokio::test]
async fn get_tool_call_returns_none_for_an_unknown_id() {
    let (storage, _dir) = test_storage().await;
    assert!(storage
        .get_tool_call("no-such-call")
        .await
        .unwrap()
        .is_none());
}

/// `complete_tool_call` must write all three of output, status and
/// completed_at. Asserting only the status would let a mutant drop the output
/// binding and still pass.
#[tokio::test]
async fn complete_tool_call_writes_output_status_and_completion_time() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "tool calls", None)
        .await
        .unwrap();
    let message = storage
        .create_message(&session.id, "assistant", "x", "complete")
        .await
        .unwrap();
    let call = storage
        .create_tool_call(&session.id, &message.id, "Bash", "{}")
        .await
        .unwrap();

    storage
        .complete_tool_call(&call.id, Some("total 0\n"), "success")
        .await
        .unwrap();

    let done = storage.get_tool_call(&call.id).await.unwrap().unwrap();
    assert_eq!(done.output.as_deref(), Some("total 0\n"));
    assert_eq!(done.status, "success");
    assert!(done.completed_at.is_some());
    // The fields that identify the call must not have been disturbed.
    assert_eq!(done.name, "Bash");
    assert_eq!(done.session_id, session.id);
}

/// A failed call carries no output but must still be marked completed — pins
/// that `output` is bound as a nullable value rather than skipped.
#[tokio::test]
async fn a_failed_tool_call_records_its_status_without_output() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "tool calls", None)
        .await
        .unwrap();
    let message = storage
        .create_message(&session.id, "assistant", "x", "complete")
        .await
        .unwrap();
    let call = storage
        .create_tool_call(&session.id, &message.id, "Bash", "{}")
        .await
        .unwrap();

    storage
        .complete_tool_call(&call.id, None, "error")
        .await
        .unwrap();

    let done = storage.get_tool_call(&call.id).await.unwrap().unwrap();
    assert_eq!(done.status, "error");
    assert_eq!(done.output, None);
    assert!(done.completed_at.is_some());
}

/// Completing one call must not touch its siblings — pins the `WHERE id = ?`.
#[tokio::test]
async fn completing_one_tool_call_leaves_the_others_pending() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "tool calls", None)
        .await
        .unwrap();
    let message = storage
        .create_message(&session.id, "assistant", "x", "complete")
        .await
        .unwrap();
    let first = storage
        .create_tool_call(&session.id, &message.id, "Read", "{}")
        .await
        .unwrap();
    let second = storage
        .create_tool_call(&session.id, &message.id, "Write", "{}")
        .await
        .unwrap();

    storage
        .complete_tool_call(&first.id, Some("ok"), "success")
        .await
        .unwrap();

    assert_eq!(
        storage
            .get_tool_call(&second.id)
            .await
            .unwrap()
            .unwrap()
            .status,
        "pending"
    );
}

/// Listing is scoped to one session and ordered oldest-first. Both halves
/// matter: the scoping pins `WHERE session_id = ?` and the ordering pins
/// `ORDER BY created_at ASC` against its DESC mutation.
#[tokio::test]
async fn tool_calls_are_listed_per_session_in_creation_order() {
    let (storage, _dir) = test_storage().await;
    let mine = storage
        .create_session("claude", "/repo", "tool calls", None)
        .await
        .unwrap();
    let theirs = storage
        .create_session("claude", "/other", "other session", None)
        .await
        .unwrap();
    let my_msg = storage
        .create_message(&mine.id, "assistant", "x", "complete")
        .await
        .unwrap();
    let their_msg = storage
        .create_message(&theirs.id, "assistant", "x", "complete")
        .await
        .unwrap();

    for name in ["first", "second", "third"] {
        storage
            .create_tool_call(&mine.id, &my_msg.id, name, "{}")
            .await
            .unwrap();
        // Distinct created_at values, so the ordering is actually decidable.
        tokio::time::sleep(std::time::Duration::from_millis(5)).await;
    }
    storage
        .create_tool_call(&theirs.id, &their_msg.id, "not-mine", "{}")
        .await
        .unwrap();

    let listed = storage.list_tool_calls_for_session(&mine.id).await.unwrap();
    let names: Vec<&str> = listed.iter().map(|c| c.name.as_str()).collect();
    assert_eq!(names, vec!["first", "second", "third"]);
}

#[tokio::test]
async fn listing_tool_calls_for_a_session_with_none_is_empty() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "tool calls", None)
        .await
        .unwrap();
    assert!(storage
        .list_tool_calls_for_session(&session.id)
        .await
        .unwrap()
        .is_empty());
}

// ─── Accounts ────────────────────────────────────────────────────────────────

#[tokio::test]
async fn create_account_persists_every_field_and_reads_back() {
    let (storage, _dir) = test_storage().await;

    let account = storage
        .create_account("work", "claude", "/creds/work.json", 5)
        .await
        .unwrap();

    assert!(!account.id.is_empty());
    assert_eq!(account.name, "work");
    assert_eq!(account.provider, "claude");
    assert_eq!(account.credentials_path, "/creds/work.json");
    assert_eq!(account.priority, 5);
    assert_eq!(account.limited_until, None);

    let stored = storage.get_account(&account.id).await.unwrap().unwrap();
    assert_eq!(stored.name, "work");
    assert_eq!(stored.priority, 5);
}

#[tokio::test]
async fn get_account_returns_none_for_an_unknown_id() {
    let (storage, _dir) = test_storage().await;
    assert!(storage
        .get_account("no-such-account")
        .await
        .unwrap()
        .is_none());
}

/// Accounts come back ordered by ascending priority, which is what the router
/// relies on to pick the preferred account first. Inserting them out of order
/// is what makes the ORDER BY observable.
#[tokio::test]
async fn accounts_are_listed_by_ascending_priority() {
    let (storage, _dir) = test_storage().await;
    storage
        .create_account("third", "claude", "/c", 30)
        .await
        .unwrap();
    storage
        .create_account("first", "claude", "/a", 10)
        .await
        .unwrap();
    storage
        .create_account("second", "claude", "/b", 20)
        .await
        .unwrap();

    let names: Vec<String> = storage
        .list_accounts()
        .await
        .unwrap()
        .into_iter()
        .map(|a| a.name)
        .collect();
    assert_eq!(names, vec!["first", "second", "third"]);
}

/// Pins `update_account_priority` in both directions: the named account moves
/// and the others do not, which is also visible as a change in list order.
#[tokio::test]
async fn updating_a_priority_reorders_only_that_account() {
    let (storage, _dir) = test_storage().await;
    let a = storage
        .create_account("a", "claude", "/a", 10)
        .await
        .unwrap();
    let b = storage
        .create_account("b", "claude", "/b", 20)
        .await
        .unwrap();

    storage.update_account_priority(&a.id, 99).await.unwrap();

    assert_eq!(
        storage.get_account(&a.id).await.unwrap().unwrap().priority,
        99
    );
    assert_eq!(
        storage.get_account(&b.id).await.unwrap().unwrap().priority,
        20
    );

    let names: Vec<String> = storage
        .list_accounts()
        .await
        .unwrap()
        .into_iter()
        .map(|x| x.name)
        .collect();
    assert_eq!(names, vec!["b", "a"], "a should now sort last");
}

/// `set_account_limited` must round-trip a value AND be able to clear it —
/// a test that only sets it would miss a mutant that ignores the None case.
#[tokio::test]
async fn an_account_rate_limit_can_be_set_and_cleared() {
    let (storage, _dir) = test_storage().await;
    let account = storage
        .create_account("a", "claude", "/a", 1)
        .await
        .unwrap();

    storage
        .set_account_limited(&account.id, Some("2026-01-01T00:00:00Z"))
        .await
        .unwrap();
    assert_eq!(
        storage
            .get_account(&account.id)
            .await
            .unwrap()
            .unwrap()
            .limited_until
            .as_deref(),
        Some("2026-01-01T00:00:00Z")
    );

    storage
        .set_account_limited(&account.id, None)
        .await
        .unwrap();
    assert_eq!(
        storage
            .get_account(&account.id)
            .await
            .unwrap()
            .unwrap()
            .limited_until,
        None
    );
}

/// Deleting one account must leave the rest — pins the `WHERE id = ?` against
/// a mutant that drops it and empties the table.
#[tokio::test]
async fn delete_account_removes_only_the_named_account() {
    let (storage, _dir) = test_storage().await;
    let doomed = storage
        .create_account("doomed", "claude", "/a", 1)
        .await
        .unwrap();
    let kept = storage
        .create_account("kept", "claude", "/b", 2)
        .await
        .unwrap();

    storage.delete_account(&doomed.id).await.unwrap();

    assert!(storage.get_account(&doomed.id).await.unwrap().is_none());
    assert!(storage.get_account(&kept.id).await.unwrap().is_some());
    assert_eq!(storage.list_accounts().await.unwrap().len(), 1);
}

/// Deleting an id that does not exist is a no-op, not an error.
#[tokio::test]
async fn deleting_an_unknown_account_is_harmless() {
    let (storage, _dir) = test_storage().await;
    storage
        .create_account("kept", "claude", "/b", 2)
        .await
        .unwrap();
    storage.delete_account("no-such-account").await.unwrap();
    assert_eq!(storage.list_accounts().await.unwrap().len(), 1);
}

#[tokio::test]
async fn account_events_are_recorded_against_their_account() {
    let (storage, _dir) = test_storage().await;
    let account = storage
        .create_account("a", "claude", "/a", 1)
        .await
        .unwrap();

    storage
        .log_account_event(&account.id, "rate_limited", Some(r#"{"retry_after":60}"#))
        .await
        .unwrap();
    storage
        .log_account_event(&account.id, "recovered", None)
        .await
        .unwrap();

    let rows: Vec<(String, Option<String>)> = sqlx::query_as(
        "SELECT event_type, metadata FROM account_events WHERE account_id = ? ORDER BY id",
    )
    .bind(&account.id)
    .fetch_all(storage.pool())
    .await
    .unwrap();

    assert_eq!(rows.len(), 2);
    let kinds: Vec<&str> = rows.iter().map(|r| r.0.as_str()).collect();
    assert!(kinds.contains(&"rate_limited"));
    assert!(kinds.contains(&"recovered"));
    // The nullable metadata must survive both present and absent.
    assert!(rows
        .iter()
        .any(|r| r.1.as_deref() == Some(r#"{"retry_after":60}"#)));
    assert!(rows.iter().any(|r| r.1.is_none()));
}

// ─── Tool-call events and account events ─────────────────────────────────────

#[tokio::test]
async fn create_tool_call_event_persists_every_field_including_the_nullable_ones() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "events", None)
        .await
        .unwrap();

    let approved = storage
        .create_tool_call_event(&session.id, "Bash", Some("ls -la"), "user", None)
        .await
        .unwrap();
    assert!(!approved.id.is_empty());
    assert_eq!(approved.session_id, session.id);
    assert_eq!(approved.tool_name, "Bash");
    assert_eq!(approved.sanitized_input.as_deref(), Some("ls -la"));
    assert_eq!(approved.approved_by, "user");
    assert_eq!(approved.rejection_reason, None);
    assert!(!approved.created_at.is_empty());

    let rejected = storage
        .create_tool_call_event(&session.id, "Bash", None, "policy", Some("denied by rule"))
        .await
        .unwrap();
    assert_eq!(rejected.sanitized_input, None);
    assert_eq!(rejected.rejection_reason.as_deref(), Some("denied by rule"));

    // Read back, so a mutant that returns a fabricated row cannot pass.
    let all = storage
        .list_tool_call_events(Some(&session.id), 10, None)
        .await
        .unwrap();
    assert_eq!(all.len(), 2);
}

/// Events are scoped to their session and returned newest-first, capped by
/// `limit`. The fixture uses four events and a limit of two, so the correct
/// answer differs from "all of them" and from any off-by-one.
#[tokio::test]
async fn tool_call_events_are_scoped_limited_and_newest_first() {
    let (storage, _dir) = test_storage().await;
    let mine = storage
        .create_session("claude", "/repo", "mine", None)
        .await
        .unwrap();
    let theirs = storage
        .create_session("claude", "/other", "theirs", None)
        .await
        .unwrap();

    for name in ["first", "second", "third", "fourth"] {
        storage
            .create_tool_call_event(&mine.id, name, None, "user", None)
            .await
            .unwrap();
        tokio::time::sleep(std::time::Duration::from_millis(5)).await;
    }
    storage
        .create_tool_call_event(&theirs.id, "not-mine", None, "user", None)
        .await
        .unwrap();

    let page = storage
        .list_tool_call_events(Some(&mine.id), 2, None)
        .await
        .unwrap();
    let names: Vec<&str> = page.iter().map(|e| e.tool_name.as_str()).collect();
    assert_eq!(names, vec!["fourth", "third"], "newest first, limited to 2");

    // Unscoped still sees the other session's event.
    let everything = storage.list_tool_call_events(None, 50, None).await.unwrap();
    assert_eq!(everything.len(), 5);
}

/// Kills `if days == 0` -> `!=`: a zero retention must delete nothing, and a
/// non-zero one must not be short-circuited into deleting nothing.
#[tokio::test]
async fn pruning_tool_call_events_with_zero_days_is_a_no_op() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "prune", None)
        .await
        .unwrap();
    storage
        .create_tool_call_event(&session.id, "Bash", None, "user", None)
        .await
        .unwrap();

    assert_eq!(storage.prune_tool_call_events(0).await.unwrap(), 0);
    assert_eq!(
        storage
            .list_tool_call_events(None, 50, None)
            .await
            .unwrap()
            .len(),
        1,
        "zero days must not delete anything"
    );

    // A real retention window leaves a fresh event alone too, but takes the
    // DELETE path rather than the early return.
    assert_eq!(storage.prune_tool_call_events(7).await.unwrap(), 0);
    assert_eq!(
        storage
            .list_tool_call_events(None, 50, None)
            .await
            .unwrap()
            .len(),
        1
    );
}

/// Account events are scoped and limited the same way.
#[tokio::test]
async fn account_events_are_scoped_and_limited() {
    let (storage, _dir) = test_storage().await;
    let mine = storage
        .create_account("mine", "claude", "/a", 1)
        .await
        .unwrap();
    let theirs = storage
        .create_account("theirs", "claude", "/b", 2)
        .await
        .unwrap();

    for kind in ["one", "two", "three"] {
        storage
            .log_account_event(&mine.id, kind, None)
            .await
            .unwrap();
        tokio::time::sleep(std::time::Duration::from_millis(5)).await;
    }
    storage
        .log_account_event(&theirs.id, "not-mine", None)
        .await
        .unwrap();

    let scoped = storage
        .list_account_events(Some(&mine.id), 2)
        .await
        .unwrap();
    assert_eq!(scoped.len(), 2, "limit must be applied");
    assert!(scoped.iter().all(|e| e.account_id == mine.id));

    let all = storage.list_account_events(None, 50).await.unwrap();
    assert_eq!(all.len(), 4);
}

/// Backdate a tool-call event's created_at to `days` ago, in the same RFC3339
/// format the writer uses.
#[cfg(test)]
async fn backdate_tool_call_event(storage: &Storage, id: &str, days: i64) {
    let when = (chrono::Utc::now() - chrono::Duration::days(days)).to_rfc3339();
    sqlx::query("UPDATE tool_call_events SET created_at = ? WHERE id = ?")
        .bind(&when)
        .bind(id)
        .execute(storage.pool())
        .await
        .unwrap();
}

#[tokio::test]
async fn prune_tool_call_events_deletes_rows_older_than_the_window() {
    let (storage, _dir) = test_storage().await;
    let session = storage
        .create_session("claude", "/repo", "prune", None)
        .await
        .unwrap();

    let old_a = storage
        .create_tool_call_event(&session.id, "old-a", None, "user", None)
        .await
        .unwrap();
    let old_b = storage
        .create_tool_call_event(&session.id, "old-b", None, "user", None)
        .await
        .unwrap();
    let fresh = storage
        .create_tool_call_event(&session.id, "fresh", None, "user", None)
        .await
        .unwrap();

    backdate_tool_call_event(&storage, &old_a.id, 30).await;
    backdate_tool_call_event(&storage, &old_b.id, 30).await;

    // Two of the three are older than the window, so the answer is 2 — which
    // differs from 0 (nothing pruned), 1, and 3 (everything pruned).
    let pruned = storage.prune_tool_call_events(7).await.unwrap();
    assert_eq!(pruned, 2, "both backdated events must be deleted");

    let left = storage.list_tool_call_events(None, 50, None).await.unwrap();
    assert_eq!(left.len(), 1);
    assert_eq!(left[0].tool_name, "fresh");
    let _ = fresh;
}

// ─── Worktrees ───────────────────────────────────────────────────────────────

/// Every column is asserted, including the ones the function does not take:
/// the 'active' default from the schema and both timestamps.
#[tokio::test]
async fn create_worktree_persists_every_field_and_defaults_to_active() {
    let (storage, _dir) = test_storage().await;

    let wt = storage
        .create_worktree("task-1", "/tmp/wt/task-1", "feature/one", "/repo")
        .await
        .unwrap();

    assert_eq!(wt.task_id, "task-1");
    assert_eq!(wt.worktree_path, "/tmp/wt/task-1");
    assert_eq!(wt.branch, "feature/one");
    assert_eq!(wt.repo_path, "/repo");
    assert_eq!(wt.status, "active");
    assert!(!wt.created_at.is_empty());
    assert!(!wt.updated_at.is_empty());

    // Read back rather than trusting the returned struct.
    let stored = storage.get_worktree("task-1").await.unwrap().unwrap();
    assert_eq!(stored.branch, "feature/one");
    assert_eq!(stored.status, "active");
}

#[tokio::test]
async fn get_worktree_returns_none_for_an_unknown_task() {
    let (storage, _dir) = test_storage().await;
    assert!(storage
        .get_worktree("no-such-task")
        .await
        .unwrap()
        .is_none());
}

/// `set_worktree_status` must move the named worktree and leave the others,
/// which pins both the SET and the WHERE.
#[tokio::test]
async fn setting_a_worktree_status_affects_only_that_worktree() {
    let (storage, _dir) = test_storage().await;
    storage
        .create_worktree("task-1", "/tmp/wt/1", "b1", "/repo")
        .await
        .unwrap();
    storage
        .create_worktree("task-2", "/tmp/wt/2", "b2", "/repo")
        .await
        .unwrap();

    storage.set_worktree_status("task-1", "done").await.unwrap();

    assert_eq!(
        storage
            .get_worktree("task-1")
            .await
            .unwrap()
            .unwrap()
            .status,
        "done"
    );
    assert_eq!(
        storage
            .get_worktree("task-2")
            .await
            .unwrap()
            .unwrap()
            .status,
        "active"
    );
}

/// The status filter is pinned in both directions: filtered returns only the
/// matches, unfiltered returns everything. Either alone would leave a mutant
/// that ignores the filter, or one that always applies it, alive.
#[tokio::test]
async fn listing_worktrees_honours_the_status_filter() {
    let (storage, _dir) = test_storage().await;
    for (task, status) in [("a", "active"), ("b", "done"), ("c", "active")] {
        storage
            .create_worktree(task, &format!("/tmp/wt/{task}"), "br", "/repo")
            .await
            .unwrap();
        storage.set_worktree_status(task, status).await.unwrap();
    }

    let active = storage.list_worktrees(Some("active")).await.unwrap();
    assert_eq!(active.len(), 2);
    assert!(active.iter().all(|w| w.status == "active"));

    let done = storage.list_worktrees(Some("done")).await.unwrap();
    assert_eq!(done.len(), 1);
    assert_eq!(done[0].task_id, "b");

    assert_eq!(storage.list_worktrees(None).await.unwrap().len(), 3);
}

/// `load_worktrees` is the startup query: it must return the worktrees still
/// in play and skip the two terminal states. The fixture puts one worktree in
/// each of the four statuses, so the correct answer (2) differs from "all"
/// and from either single exclusion.
#[tokio::test]
async fn load_worktrees_skips_abandoned_and_merged() {
    let (storage, _dir) = test_storage().await;
    for (task, status) in [
        ("keep-active", "active"),
        ("keep-done", "done"),
        ("drop-abandoned", "abandoned"),
        ("drop-merged", "merged"),
    ] {
        storage
            .create_worktree(task, &format!("/tmp/wt/{task}"), "br", "/repo")
            .await
            .unwrap();
        storage.set_worktree_status(task, status).await.unwrap();
    }

    let loaded = storage.load_worktrees().await.unwrap();
    let mut tasks: Vec<&str> = loaded.iter().map(|w| w.task_id.as_str()).collect();
    tasks.sort_unstable();
    assert_eq!(tasks, vec!["keep-active", "keep-done"]);
}

#[tokio::test]
async fn delete_worktree_removes_only_the_named_worktree() {
    let (storage, _dir) = test_storage().await;
    storage
        .create_worktree("doomed", "/tmp/wt/d", "b1", "/repo")
        .await
        .unwrap();
    storage
        .create_worktree("kept", "/tmp/wt/k", "b2", "/repo")
        .await
        .unwrap();

    storage.delete_worktree("doomed").await.unwrap();

    assert!(storage.get_worktree("doomed").await.unwrap().is_none());
    assert!(storage.get_worktree("kept").await.unwrap().is_some());
    assert_eq!(storage.list_worktrees(None).await.unwrap().len(), 1);
}

#[tokio::test]
async fn deleting_an_unknown_worktree_is_harmless() {
    let (storage, _dir) = test_storage().await;
    storage
        .create_worktree("kept", "/tmp/wt/k", "b", "/repo")
        .await
        .unwrap();
    storage.delete_worktree("no-such-task").await.unwrap();
    assert_eq!(storage.list_worktrees(None).await.unwrap().len(), 1);
}
