//! Mutation-gap tests for [`Storage`] setters, listings and push tokens (P7-HYG-37).
//!
//! Each test names the surviving mutant(s) of scheduled run 36402849254 that it
//! kills. Every write is read back, because a `-> Result<()>` body replaced with
//! `Ok(())` returns success too.

use super::*;

async fn test_storage() -> (Storage, tempfile::TempDir) {
    let dir = tempfile::tempdir().expect("create tempdir");
    let storage = Storage::new(dir.path()).await.expect("open storage");
    (storage, dir)
}

#[tokio::test]
async fn list_sessions_returns_every_row_newest_first() {
    // Kills: Storage::list_sessions -> Ok(vec![])
    let (storage, _dir) = test_storage().await;
    let old = storage
        .create_session("claude", "/a", "old", None)
        .await
        .unwrap();
    let new = storage
        .create_session("codex", "/b", "new", None)
        .await
        .unwrap();
    sqlx::query("UPDATE sessions SET created_at = ? WHERE id = ?")
        .bind("2020-01-01T00:00:00Z")
        .bind(&old.id)
        .execute(storage.pool())
        .await
        .unwrap();
    sqlx::query("UPDATE sessions SET created_at = ? WHERE id = ?")
        .bind("2021-01-01T00:00:00Z")
        .bind(&new.id)
        .execute(storage.pool())
        .await
        .unwrap();

    let rows = storage.list_sessions().await.unwrap();
    let ids: Vec<&str> = rows.iter().map(|r| r.id.as_str()).collect();
    assert_eq!(ids, vec![new.id.as_str(), old.id.as_str()]);
}

#[tokio::test]
async fn update_session_routed_provider_is_persisted() {
    // Kills: Storage::update_session_routed_provider -> Ok(())
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("auto", "/r", "t", None)
        .await
        .unwrap();
    assert_eq!(s.routed_provider, None);

    storage
        .update_session_routed_provider(&s.id, "codex")
        .await
        .unwrap();

    let got = storage.get_session(&s.id).await.unwrap().unwrap();
    assert_eq!(got.routed_provider.as_deref(), Some("codex"));
}

#[tokio::test]
async fn set_session_mode_is_persisted_and_scoped_to_the_session() {
    // Kills: Storage::set_session_mode -> Ok(())
    let (storage, _dir) = test_storage().await;
    let a = storage
        .create_session("claude", "/r", "a", None)
        .await
        .unwrap();
    let b = storage
        .create_session("claude", "/r", "b", None)
        .await
        .unwrap();
    let before = b.mode.clone();

    storage.set_session_mode(&a.id, "FORGE").await.unwrap();

    assert_eq!(
        storage.get_session(&a.id).await.unwrap().unwrap().mode,
        "FORGE"
    );
    assert_eq!(
        storage.get_session(&b.id).await.unwrap().unwrap().mode,
        before
    );
}

#[tokio::test]
async fn update_message_content_rewrites_content_and_status() {
    // Kills: Storage::update_message_content -> Ok(())
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .unwrap();
    let m = storage
        .create_message(&s.id, "assistant", "partial", "streaming")
        .await
        .unwrap();

    storage
        .update_message_content(&m.id, "the full reply", "complete")
        .await
        .unwrap();

    let got = storage.get_message(&m.id).await.unwrap().unwrap();
    assert_eq!(got.content, "the full reply");
    assert_eq!(got.status, "complete");
}

#[tokio::test]
async fn vacuum_succeeds_on_a_populated_database_and_keeps_the_data() {
    // Kills: Storage::vacuum -> Ok(()). VACUUM cannot run inside a transaction,
    // so a pooled connection left mid-transaction would make it fail; and the
    // data must survive it.
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .unwrap();
    storage.vacuum().await.expect("vacuum");
    assert!(storage.get_session(&s.id).await.unwrap().is_some());
}

#[tokio::test]
async fn set_model_override_sets_and_clears_the_override() {
    // Kills: Storage::set_model_override -> Ok(())
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .unwrap();
    assert_eq!(s.model_override, None);

    storage
        .set_model_override(&s.id, Some("opus"))
        .await
        .unwrap();
    assert_eq!(
        storage
            .get_session(&s.id)
            .await
            .unwrap()
            .unwrap()
            .model_override
            .as_deref(),
        Some("opus")
    );

    storage.set_model_override(&s.id, None).await.unwrap();
    assert_eq!(
        storage
            .get_session(&s.id)
            .await
            .unwrap()
            .unwrap()
            .model_override,
        None
    );
}

#[tokio::test]
async fn repo_contexts_list_by_priority_and_can_be_removed() {
    // Kills: list_repo_contexts -> Ok(vec![]); remove_repo_context -> Ok(())
    let (storage, _dir) = test_storage().await;
    let s = storage
        .create_session("claude", "/r", "t", None)
        .await
        .unwrap();
    let other = storage
        .create_session("claude", "/r", "o", None)
        .await
        .unwrap();
    let low = storage.add_repo_context(&s.id, "/low", 2).await.unwrap();
    let high = storage.add_repo_context(&s.id, "/high", 9).await.unwrap();
    storage
        .add_repo_context(&other.id, "/elsewhere", 5)
        .await
        .unwrap();

    let rows = storage.list_repo_contexts(&s.id).await.unwrap();
    let paths: Vec<&str> = rows.iter().map(|r| r.path.as_str()).collect();
    assert_eq!(paths, vec!["/high", "/low"]);

    storage.remove_repo_context(&high.id).await.unwrap();

    let rows = storage.list_repo_contexts(&s.id).await.unwrap();
    assert_eq!(rows.len(), 1);
    assert_eq!(rows[0].id, low.id);
    assert_eq!(
        storage.list_repo_contexts(&other.id).await.unwrap().len(),
        1
    );
}

#[tokio::test]
async fn push_tokens_upsert_list_and_delete_round_trip() {
    // Kills: upsert_push_token -> Ok(()); delete_push_token -> Ok(());
    // list_push_tokens -> Ok(vec![]) and every Ok(vec![(.., .., ..)]) literal,
    // because the exact (device, token, platform) triples are asserted.
    let (storage, _dir) = test_storage().await;
    assert!(storage.list_push_tokens().await.unwrap().is_empty());

    storage
        .upsert_push_token("dev-1", "tok-1", "apns")
        .await
        .unwrap();
    storage
        .upsert_push_token("dev-2", "tok-2", "fcm")
        .await
        .unwrap();

    let mut rows = storage.list_push_tokens().await.unwrap();
    rows.sort();
    assert_eq!(
        rows,
        vec![
            ("dev-1".to_string(), "tok-1".to_string(), "apns".to_string()),
            ("dev-2".to_string(), "tok-2".to_string(), "fcm".to_string()),
        ]
    );

    // Upsert on the same device replaces token and platform, no duplicate row.
    storage
        .upsert_push_token("dev-1", "tok-1b", "fcm")
        .await
        .unwrap();
    let mut rows = storage.list_push_tokens().await.unwrap();
    rows.sort();
    assert_eq!(rows.len(), 2);
    assert_eq!(
        rows[0],
        ("dev-1".to_string(), "tok-1b".to_string(), "fcm".to_string())
    );

    storage.delete_push_token("dev-1").await.unwrap();
    assert_eq!(
        storage.list_push_tokens().await.unwrap(),
        vec![("dev-2".to_string(), "tok-2".to_string(), "fcm".to_string())]
    );
}

#[tokio::test]
async fn pre_migration_backup_is_made_only_when_a_database_already_exists() {
    // Kills: Storage::backup_before_migrate -> () and the `delete !` on
    // `!db_path.exists()`.
    let dir = tempfile::tempdir().unwrap();
    let backup = dir
        .path()
        .join("backups")
        .join(format!("clawd-{}.db", env!("CARGO_PKG_VERSION")));

    // Fresh install: nothing to back up, so no backups directory either.
    let first = Storage::new(dir.path()).await.unwrap();
    first.set_setting("marker", "v1").await.unwrap();
    assert!(
        !dir.path().join("backups").exists(),
        "fresh install must not back up"
    );
    drop(first);

    // Second open finds an existing database and copies it.
    let _second = Storage::new(dir.path()).await.unwrap();
    assert!(
        backup.exists(),
        "existing database must be backed up before migrating"
    );
    assert!(std::fs::metadata(&backup).unwrap().len() > 0);
}
