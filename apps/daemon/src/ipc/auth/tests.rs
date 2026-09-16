//! Tests for the IPC auth helpers.
//!
//! The permission checks are written against the surviving-mutant list from
//! the mutation gate. Every mutant in the old `check_token_permissions`
//! survived because its only output was a `tracing::warn!`; splitting the
//! decision into `token_permissions` is what makes them reachable.

use super::*;

#[cfg(unix)]
fn write_token(dir: &std::path::Path, mode: u32) -> std::path::PathBuf {
    use std::os::unix::fs::PermissionsExt;
    let path = dir.join("auth_token");
    std::fs::write(&path, "secret").unwrap();
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(mode)).unwrap();
    path
}

// ─── token_permissions ───────────────────────────────────────────────────────

/// Kills the `delete !` mutation on `!path.exists()`.
///
/// With the `!` dropped, an absent file falls through to the metadata read,
/// which fails, and the answer becomes `Unknown` instead of `Absent` — the
/// daemon would stop distinguishing "no token yet" from "cannot tell".
#[test]
fn an_absent_token_file_reports_absent() {
    let dir = tempfile::tempdir().unwrap();
    assert_eq!(token_permissions(dir.path()), TokenPermissions::Absent);
}

/// Kills `mode == SECURE_TOKEN_MODE` -> `!=`.
///
/// The one mode that must be accepted is 0600. Inverting the comparison
/// reports the single correct mode as insecure and every wrong one as fine,
/// which is the worst possible direction for this check.
#[cfg(unix)]
#[test]
fn a_token_file_at_0600_is_secure() {
    let dir = tempfile::tempdir().unwrap();
    write_token(dir.path(), 0o600);
    assert_eq!(token_permissions(dir.path()), TokenPermissions::Secure);
}

/// Kills `& 0o777` -> `^ 0o777` and `| 0o777`, by asserting the exact mode
/// carried back rather than merely that the file was judged insecure.
///
/// A file created 0644 has a raw mode of 0o100644 including the file-type
/// bits. Masking gives 0o644; xor gives 0o100133 and or gives 0o100777. All
/// three are "insecure", so a test that only checked the variant would miss
/// both mutations — it is the mode value that separates them.
#[cfg(unix)]
#[test]
fn an_insecure_token_file_reports_its_masked_mode() {
    let dir = tempfile::tempdir().unwrap();
    write_token(dir.path(), 0o644);
    assert_eq!(
        token_permissions(dir.path()),
        TokenPermissions::Insecure { mode: 0o644 }
    );
}

/// Every mode that is not exactly 0600 must be refused, including ones that
/// are *more* restrictive — the check is an equality, not an upper bound.
#[cfg(unix)]
#[test]
fn every_mode_other_than_0600_is_insecure() {
    for mode in [0o400, 0o604, 0o640, 0o660, 0o666, 0o700, 0o777] {
        let dir = tempfile::tempdir().unwrap();
        write_token(dir.path(), mode);
        assert_eq!(
            token_permissions(dir.path()),
            TokenPermissions::Insecure { mode },
            "mode {mode:04o} should be reported insecure, carrying its own value"
        );
    }
}

/// The warning wrapper must not panic on any of the four outcomes. It has no
/// return value to assert, so this pins only that it stays total.
#[test]
fn the_warning_wrapper_handles_every_outcome() {
    let dir = tempfile::tempdir().unwrap();
    check_token_permissions(dir.path()); // Absent
    #[cfg(unix)]
    {
        write_token(dir.path(), 0o600);
        check_token_permissions(dir.path()); // Secure
        write_token(dir.path(), 0o644);
        check_token_permissions(dir.path()); // Insecure
    }
}

// ─── validate_bearer ─────────────────────────────────────────────────────────

/// The prefix must match exactly, including its trailing space, and the token
/// comparison must be an equality over the whole remainder.
#[test]
fn only_an_exact_bearer_token_validates() {
    assert!(validate_bearer("Bearer secret123", "secret123"));

    // Wrong token, prefix of the token, and superstring of the token.
    assert!(!validate_bearer("Bearer secret124", "secret123"));
    assert!(!validate_bearer("Bearer secret12", "secret123"));
    assert!(!validate_bearer("Bearer secret1234", "secret123"));

    // Missing, mis-cased, or malformed prefix.
    assert!(!validate_bearer("secret123", "secret123"));
    assert!(!validate_bearer("bearer secret123", "secret123"));
    assert!(!validate_bearer("Bearer  secret123", "secret123"));
    assert!(!validate_bearer("Bearersecret123", "secret123"));
    assert!(!validate_bearer("", "secret123"));
}

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

use tempfile::TempDir;

#[test]
fn test_validate_bearer_valid() {
    assert!(validate_bearer("Bearer secret123", "secret123"));
}

#[test]
fn test_validate_bearer_invalid() {
    assert!(!validate_bearer("Bearer wrong", "secret123"));
    assert!(!validate_bearer("secret123", "secret123"));
    assert!(!validate_bearer("", "secret123"));
}

#[test]
fn test_get_or_create_token_creates_file() {
    let dir = TempDir::new().unwrap();
    let token = get_or_create_token(dir.path()).unwrap();
    assert_eq!(token.len(), 32, "token should be 32 hex chars");
    assert!(dir.path().join("auth_token").exists());
}

#[test]
fn test_get_or_create_token_idempotent() {
    let dir = TempDir::new().unwrap();
    let t1 = get_or_create_token(dir.path()).unwrap();
    let t2 = get_or_create_token(dir.path()).unwrap();
    assert_eq!(t1, t2, "second call should return same token");
}

#[cfg(unix)]
#[test]
fn test_auth_token_created_with_0600_permissions() {
    use std::os::unix::fs::PermissionsExt;
    let dir = TempDir::new().unwrap();
    get_or_create_token(dir.path()).unwrap();
    let meta = std::fs::metadata(dir.path().join("auth_token")).unwrap();
    let mode = meta.permissions().mode() & 0o777;
    assert_eq!(
        mode, 0o600,
        "auth_token must have mode 0600, got {mode:04o}"
    );
}
