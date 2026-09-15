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
