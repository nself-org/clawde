//! Behavioural tests for the session permission gate and scope mapping.
//!
//! Same discipline as `storage::tests`: these are written to kill mutants, not
//! to move a coverage number. `tool_name_to_scope` is a chain of `||` branches
//! returning string literals, so the mutants that matter are (a) swapping `||`
//! for `&&`, (b) returning a different literal, and (c) dropping a branch so an
//! earlier or later arm wins. Every keyword is therefore asserted to its exact
//! scope, and the branch *order* is pinned with names that match two arms at
//! once.
//!
//! `check_tool_permission` is a security gate, so each of its exits is covered
//! in both the allow and the deny direction.

use super::*;
use crate::ipc::event::EventBroadcaster;
use crate::storage::Storage;

/// A `SessionManager` backed by a real temp database.
///
/// The `TempDir` must outlive the manager — dropping it removes the database.
async fn test_manager() -> (SessionManager, Arc<Storage>, tempfile::TempDir) {
    let dir = tempfile::tempdir().expect("create tempdir");
    let storage = Arc::new(Storage::new(dir.path()).await.expect("open storage"));
    let broadcaster = Arc::new(EventBroadcaster::new());
    let manager = SessionManager::new(Arc::clone(&storage), broadcaster, dir.path().to_path_buf());
    (manager, storage, dir)
}

// ─── tool_name_to_scope ─────────────────────────────────────────────────────

#[test]
fn tool_name_to_scope_maps_every_read_keyword_to_file_read() {
    // Each keyword is asserted on its own. A `||` → `&&` mutant would require a
    // name containing *all* of them, so every one of these fails it.
    for name in ["Read", "Glob", "Grep", "WebFetch", "WebSearch"] {
        assert_eq!(
            tool_name_to_scope(name),
            "file_read",
            "{name} should map to file_read"
        );
    }
}

#[test]
fn tool_name_to_scope_maps_every_write_keyword_to_file_write() {
    for name in ["Write", "Edit", "NotebookEdit"] {
        assert_eq!(
            tool_name_to_scope(name),
            "file_write",
            "{name} should map to file_write"
        );
    }
}

#[test]
fn tool_name_to_scope_maps_git_and_every_shell_keyword() {
    assert_eq!(tool_name_to_scope("GitStatus"), "git");

    for name in ["Bash", "Shell", "Exec", "Run", "Command"] {
        assert_eq!(
            tool_name_to_scope(name),
            "shell_exec",
            "{name} should map to shell_exec"
        );
    }
}

#[test]
fn tool_name_to_scope_denies_unrecognised_tools_via_the_unknown_scope() {
    // Least privilege: anything that matches no arm must not fall through to a
    // permissive scope.
    for name in ["Telemetry", "", "xyzzy", "Sleep"] {
        assert_eq!(
            tool_name_to_scope(name),
            "unknown",
            "{name:?} should map to unknown"
        );
    }
}

#[test]
fn tool_name_to_scope_is_case_insensitive() {
    // Kills a mutant that drops the `to_lowercase()` call.
    assert_eq!(tool_name_to_scope("READ"), "file_read");
    assert_eq!(tool_name_to_scope("read"), "file_read");
    assert_eq!(tool_name_to_scope("ReAd"), "file_read");
    assert_eq!(tool_name_to_scope("BASH"), "shell_exec");
}

#[test]
fn tool_name_to_scope_resolves_multi_match_names_by_branch_order() {
    // These names match more than one arm. The earliest arm must win, which is
    // what dies if a branch is reordered or removed.
    //
    // "read" is checked before "write":
    assert_eq!(tool_name_to_scope("ReadWrite"), "file_read");
    // "write"/"edit" are checked before "git":
    assert_eq!(tool_name_to_scope("GitEdit"), "file_write");
    // "read" is checked before the shell keywords:
    assert_eq!(tool_name_to_scope("ReadCommand"), "file_read");
    // "git" is checked before the shell keywords:
    assert_eq!(tool_name_to_scope("GitBash"), "git");
}

// ─── check_tool_permission ──────────────────────────────────────────────────

#[tokio::test]
async fn check_tool_permission_errors_when_the_session_does_not_exist() {
    let (manager, _storage, _dir) = test_manager().await;

    let err = manager
        .check_tool_permission("no-such-session", "Read")
        .await
        .expect_err("an unknown session must not be permitted");
    assert!(
        err.to_string().contains("SESSION_NOT_FOUND"),
        "expected SESSION_NOT_FOUND, got: {err}"
    );
}

#[tokio::test]
async fn check_tool_permission_allows_everything_when_permissions_are_null() {
    let (manager, storage, _dir) = test_manager().await;
    let s = storage
        .create_session("claude", "/r", "unrestricted", None)
        .await
        .expect("create session");

    // A NULL permissions column means "no restrictions configured".
    for tool in ["Read", "Write", "Bash", "GitStatus", "xyzzy"] {
        manager
            .check_tool_permission(&s.id, tool)
            .await
            .unwrap_or_else(|e| panic!("{tool} should be permitted with null permissions: {e}"));
    }
}

#[tokio::test]
async fn check_tool_permission_allows_everything_when_the_scope_list_is_empty() {
    let (manager, storage, _dir) = test_manager().await;
    let s = storage
        .create_session("claude", "/r", "empty scopes", Some("[]"))
        .await
        .expect("create session");

    // An empty array is treated as "all permissions", not "no permissions".
    manager
        .check_tool_permission(&s.id, "Bash")
        .await
        .expect("an empty scope list permits everything");
}

#[tokio::test]
async fn check_tool_permission_admits_only_the_scopes_in_the_session() {
    let (manager, storage, _dir) = test_manager().await;
    let s = storage
        .create_session("claude", "/r", "read only", Some(r#"["file_read"]"#))
        .await
        .expect("create session");

    // In scope — every tool that maps to file_read is allowed.
    manager
        .check_tool_permission(&s.id, "Read")
        .await
        .expect("Read maps to file_read, which is granted");
    manager
        .check_tool_permission(&s.id, "Grep")
        .await
        .expect("Grep maps to file_read, which is granted");

    // Out of scope — both directions are asserted, so neither an always-Ok nor
    // an always-Err mutant survives.
    let err = manager
        .check_tool_permission(&s.id, "Bash")
        .await
        .expect_err("Bash maps to shell_exec, which is not granted");
    let msg = err.to_string();
    assert!(
        msg.contains("shell_exec"),
        "the error should name the required scope, got: {msg}"
    );
    assert!(
        msg.contains("Bash"),
        "the error should name the tool, got: {msg}"
    );

    manager
        .check_tool_permission(&s.id, "Write")
        .await
        .expect_err("Write maps to file_write, which is not granted");
    manager
        .check_tool_permission(&s.id, "xyzzy")
        .await
        .expect_err("an unrecognised tool maps to `unknown`, which is not granted");
}

#[tokio::test]
async fn check_tool_permission_honours_multiple_granted_scopes() {
    let (manager, storage, _dir) = test_manager().await;
    let s = storage
        .create_session(
            "claude",
            "/r",
            "read and shell",
            Some(r#"["file_read","shell_exec"]"#),
        )
        .await
        .expect("create session");

    manager
        .check_tool_permission(&s.id, "Read")
        .await
        .expect("file_read is granted");
    manager
        .check_tool_permission(&s.id, "Bash")
        .await
        .expect("shell_exec is granted");
    manager
        .check_tool_permission(&s.id, "Write")
        .await
        .expect_err("file_write was not granted");
}

#[tokio::test]
async fn check_tool_permission_fails_open_on_malformed_permissions_json() {
    let (manager, storage, _dir) = test_manager().await;
    let s = storage
        .create_session("claude", "/r", "corrupt scopes", Some("not json at all"))
        .await
        .expect("create session");

    // This pins CURRENT behaviour, and it is deliberately fail-OPEN:
    // `serde_json::from_str(..).unwrap_or_default()` yields an empty Vec on a
    // parse error, and an empty Vec is treated above as "all permissions". So a
    // corrupted permissions column grants every tool rather than denying them.
    //
    // The test exists so that behaviour cannot change silently in either
    // direction. Whether fail-open is the right policy for a permission gate is
    // a separate question and is flagged rather than changed here.
    manager
        .check_tool_permission(&s.id, "Bash")
        .await
        .expect("malformed permissions currently fail open — see the comment above");
}

// ─── active_count ───────────────────────────────────────────────────────────

#[tokio::test]
async fn active_count_is_zero_before_any_runner_starts() {
    let (manager, storage, _dir) = test_manager().await;

    assert_eq!(manager.active_count().await, 0);

    // Rows in the database are not running processes — creating a session row
    // must not move the live-runner count.
    storage
        .create_session("claude", "/r", "not running", None)
        .await
        .expect("create session");
    assert_eq!(
        manager.active_count().await,
        0,
        "a stored session with no runner must not count as active"
    );
}
