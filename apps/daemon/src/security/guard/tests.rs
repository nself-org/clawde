//! Tests for the security guards.
//!
//! The first group is written against the surviving-mutant list from the
//! mutation gate; each test names the mutation it kills.

use super::*;

/// 40 characters of base64 alphabet — the shortest run that must be redacted.
const TOKEN40: &str = "AKIAIOSFODNN7EXAMPLEwJalrXUtnFEMI1K7MDEN";
/// The same length, but exercising `+` and `/` rather than only alphanumerics.
const TOKEN40_B64: &str = "AKIAIOSFODNN7EXAM+LEwJalrXUtnFEM/1K7MDEN";

// ─── strip_null_bytes ────────────────────────────────────────────────────────

/// Kills the `String::new()` and `"xyzzy".into()` body replacements — the
/// first would silently discard every path, the second would replace it.
#[test]
fn null_bytes_are_removed_and_nothing_else_is() {
    assert_eq!(strip_null_bytes("a\0b"), "ab");
    assert_eq!(strip_null_bytes("/etc/passwd\0.png"), "/etc/passwd.png");
    assert_eq!(strip_null_bytes("/home/u/x.rs"), "/home/u/x.rs");
    assert_eq!(strip_null_bytes(""), "");
}

// ─── sanitize_tool_input ─────────────────────────────────────────────────────

#[test]
fn a_long_base64_run_is_redacted() {
    assert_eq!(TOKEN40.len(), MIN_REDACTED_RUN);
    assert_eq!(sanitize_tool_input(TOKEN40), "[REDACTED]");
}

/// `+` and `/` are part of the alphabet. If they stopped counting, a real
/// base64 key would split into short runs (17, 14 and 7 here), none of them
/// long enough to redact, and the whole key would land in the audit log.
#[test]
fn a_base64_run_containing_plus_and_slash_is_redacted() {
    assert_eq!(TOKEN40_B64.len(), MIN_REDACTED_RUN);
    assert_eq!(sanitize_tool_input(TOKEN40_B64), "[REDACTED]");
}

/// Ordinary prose longer than the threshold must survive untouched. Without
/// this, any rule that let spaces count towards a run would redact whole
/// sentences and make the audit log useless.
#[test]
fn ordinary_prose_is_never_redacted() {
    let prose = "the quick brown fox jumps over the lazy dog and keeps running";
    assert!(prose.len() > MIN_REDACTED_RUN);
    assert_eq!(sanitize_tool_input(prose), prose);
}

/// A long run of *non*-base64 characters is not credential material — pins the
/// class check that sits alongside the length check.
#[test]
fn a_long_run_of_punctuation_is_not_redacted() {
    let dots = ".".repeat(MIN_REDACTED_RUN + 10);
    assert_eq!(sanitize_tool_input(&dots), dots);
}

#[test]
fn text_around_a_redacted_run_is_preserved() {
    let input = format!("key={TOKEN40} done");
    assert_eq!(sanitize_tool_input(&input), "key=[REDACTED] done");
}

/// Pins the threshold from below: one character short must survive intact.
#[test]
fn a_run_just_under_the_threshold_is_kept() {
    let short = &TOKEN40[..MIN_REDACTED_RUN - 1];
    assert_eq!(short.len(), MIN_REDACTED_RUN - 1);
    assert_eq!(sanitize_tool_input(short), short);
}

#[test]
fn multiple_runs_are_each_redacted() {
    let input = format!("{TOKEN40} and {TOKEN40}");
    assert_eq!(sanitize_tool_input(&input), "[REDACTED] and [REDACTED]");
}

/// The scan must terminate on every input, including ones that previously
/// drove the hand-written cursor in circles. This is a regression guard for
/// the three mutants that hung the mutation gate for two hours: with grouping
/// there is no cursor arithmetic left to get stuck on.
#[test]
fn the_scan_terminates_on_adversarial_inputs() {
    for input in [
        "",
        "a",
        "\0",
        &"+".repeat(200),
        &"/".repeat(200),
        &format!("{TOKEN40}{TOKEN40}"),
        &format!("{}{TOKEN40}", " ".repeat(100)),
        &"a ".repeat(500),
    ] {
        // Reaching the assertion at all is the point.
        let _ = sanitize_tool_input(input);
    }
}

/// The grouping rewrite must be behaviour-identical to the cursor walk it
/// replaced, so the original is reproduced here and the two are compared
/// across inputs that exercise every branch of both.
#[test]
fn the_rewrite_matches_the_original_cursor_walk() {
    fn original(input: &str) -> String {
        let mut result = String::with_capacity(input.len());
        let chars: Vec<char> = input.chars().collect();
        let mut i = 0;
        while i < chars.len() {
            let mut run = 0;
            let mut j = i;
            while j < chars.len()
                && (chars[j].is_ascii_alphanumeric() || chars[j] == '+' || chars[j] == '/')
            {
                run += 1;
                j += 1;
            }
            if run >= 40 {
                result.push_str("[REDACTED]");
                i += run;
            } else {
                result.push(chars[i]);
                i += 1;
            }
        }
        result
    }

    let cases: Vec<String> = vec![
        String::new(),
        "a".to_string(),
        "hello world".to_string(),
        TOKEN40.to_string(),
        TOKEN40_B64.to_string(),
        TOKEN40[..39].to_string(),
        format!("key={TOKEN40} done"),
        format!("{TOKEN40} and {TOKEN40}"),
        format!("abc{TOKEN40}"),
        format!("{TOKEN40}xyz"),
        ".".repeat(50),
        "a ".repeat(60),
        format!("{}|{}", "x".repeat(41), "y".repeat(39)),
        "naïve café ☕ with unicode".to_string(),
    ];
    for c in &cases {
        assert_eq!(sanitize_tool_input(c), original(c), "diverged on {c:?}");
    }
}

// ─── expand_home ─────────────────────────────────────────────────────────────

/// Kills the `delete !` mutation on `!home.is_empty()`. With the `!` dropped
/// the expansion only happens when HOME is unset, so a real environment gets a
/// literal "~/" path that no filesystem call resolves.
#[test]
fn a_tilde_prefix_expands_to_the_home_directory() {
    let home = std::env::var("HOME").unwrap_or_default();
    assert!(
        !home.is_empty(),
        "this test needs HOME set to mean anything"
    );
    assert_eq!(expand_home("~/repos/x"), format!("{home}/repos/x"));
}

#[test]
fn paths_without_a_tilde_prefix_are_untouched() {
    assert_eq!(expand_home("/abs/path"), "/abs/path");
    assert_eq!(expand_home("relative/path"), "relative/path");
    // A bare "~" has no trailing slash, so it is not a prefix match.
    assert_eq!(expand_home("~"), "~");
    assert_eq!(expand_home("/a/~/b"), "/a/~/b");
}

// ─── check_repo_path_safety ──────────────────────────────────────────────────

/// Kills the `||` -> `&&` mutation on the overlap test.
///
/// The two `starts_with` calls cover opposite nestings and only one can be
/// true at a time, so `&&` is never satisfied and every overlapping path is
/// accepted. Both directions are asserted because either alone leaves the
/// mutation alive.
#[test]
fn a_repo_nested_inside_the_data_dir_is_rejected() {
    let root = tempfile::tempdir().unwrap();
    let data = root.path().join("data");
    let repo = data.join("repo");
    std::fs::create_dir_all(&repo).unwrap();
    assert!(check_repo_path_safety(&repo, &data).is_err());
}

#[test]
fn a_data_dir_nested_inside_the_repo_is_rejected() {
    let root = tempfile::tempdir().unwrap();
    let repo = root.path().join("repo");
    let data = repo.join("data");
    std::fs::create_dir_all(&data).unwrap();
    assert!(check_repo_path_safety(&repo, &data).is_err());
}

/// The negative direction, so the two checks above cannot be satisfied by
/// rejecting everything.
#[test]
fn disjoint_repo_and_data_directories_are_accepted() {
    let root = tempfile::tempdir().unwrap();
    let repo = root.path().join("repo");
    let data = root.path().join("data");
    std::fs::create_dir_all(&repo).unwrap();
    std::fs::create_dir_all(&data).unwrap();
    assert!(check_repo_path_safety(&repo, &data).is_ok());
}

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

use std::path::Path;

#[test]
fn test_safe_path_normal() {
    let base = Path::new("/home/user/repo");
    let result = safe_path(base, Path::new("src/main.rs")).unwrap();
    assert_eq!(result, PathBuf::from("/home/user/repo/src/main.rs"));
}

#[test]
fn test_safe_path_traversal_blocked() {
    let base = Path::new("/home/user/repo");
    let result = safe_path(base, Path::new("../../etc/passwd"));
    assert!(result.is_err(), "path traversal should be blocked");
}

#[test]
fn test_safe_path_absolute_blocked() {
    let base = Path::new("/home/user/repo");
    let result = safe_path(base, Path::new("/etc/passwd"));
    assert!(result.is_err(), "absolute paths should be blocked");
}

#[test]
fn test_normalize_path() {
    let p = Path::new("/a/b/../c/./d");
    assert_eq!(normalize_path(p), PathBuf::from("/a/c/d"));
}

#[test]
fn test_validate_session_id_valid() {
    assert!(validate_session_id("550e8400-e29b-41d4-a716-446655440000").is_ok());
}

#[test]
fn test_validate_session_id_invalid() {
    assert!(validate_session_id("not-a-uuid").is_err());
    assert!(validate_session_id("550e8400-e29b-41d4-a716-44665544000X").is_err());
}

// ── Tool call gating (DC.T40) ─────────────────────────────────────────────

fn default_sec() -> crate::config::SecurityConfig {
    crate::config::SecurityConfig::default()
}

#[test]
fn test_empty_allowlist_permits_all() {
    let cfg = default_sec();
    assert!(check_tool_call("Bash", "echo hello", &cfg).is_ok());
    assert!(check_tool_call("Read", "", &cfg).is_ok());
    assert!(check_tool_call("WebFetch", "", &cfg).is_ok());
}

#[test]
fn test_allowlist_blocks_unlisted_tool() {
    let cfg = crate::config::SecurityConfig {
        allowed_tools: vec!["Read".into(), "Grep".into()],
        ..Default::default()
    };
    assert!(check_tool_call("Read", "", &cfg).is_ok());
    assert!(check_tool_call("Bash", "echo", &cfg).is_err());
}

#[test]
fn test_denylist_blocks_listed_tool() {
    let cfg = crate::config::SecurityConfig {
        denied_tools: vec!["WebFetch".into()],
        ..Default::default()
    };
    assert!(check_tool_call("WebFetch", "", &cfg).is_err());
    assert!(check_tool_call("Read", "", &cfg).is_ok());
}

#[test]
fn test_tool_name_comparison_case_insensitive() {
    let cfg = crate::config::SecurityConfig {
        denied_tools: vec!["bash".into()],
        ..Default::default()
    };
    assert!(check_tool_call("Bash", "echo", &cfg).is_err());
    assert!(check_tool_call("BASH", "echo", &cfg).is_err());
}

#[test]
fn test_denied_path_blocks_bash_call() {
    let cfg = crate::config::SecurityConfig {
        denied_paths: vec!["/etc".into()],
        ..Default::default()
    };
    assert!(check_tool_call("Bash", "cat /etc/passwd", &cfg).is_err());
    assert!(check_tool_call("Bash", "echo hello", &cfg).is_ok());
}

#[test]
fn test_denied_path_only_applies_to_bash() {
    let cfg = crate::config::SecurityConfig {
        denied_paths: vec!["/etc".into()],
        ..Default::default()
    };
    // Read tool with /etc path should still be allowed (path check is Bash-only)
    assert!(check_tool_call("Read", "/etc/passwd", &cfg).is_ok());
}

// ── Input sanitization (DC.T43) ───────────────────────────────────────────

#[test]
fn test_sanitize_short_string_unchanged() {
    let s = "echo hello world";
    assert_eq!(sanitize_tool_input(s), s);
}

#[test]
fn test_sanitize_long_base64_redacted() {
    let key = "A".repeat(44); // 44 base64 chars → REDACTED
    let input = format!("curl -H 'Authorization: Bearer {}'", key);
    let result = sanitize_tool_input(&input);
    assert!(
        result.contains("[REDACTED]"),
        "long token should be redacted: {result}"
    );
    assert!(!result.contains(&key), "original key should not appear");
}

#[test]
fn test_sanitize_normal_code_unchanged() {
    let code = "let x = 42; println!(\"{x}\");";
    assert_eq!(sanitize_tool_input(code), code);
}

// ── Repo path safety (DC.T41) ─────────────────────────────────────────────

#[test]
fn test_repo_path_does_not_overlap_data_dir() {
    let data_dir = Path::new("/tmp/clawd_test_data");
    let repo_path = Path::new("/home/user/my_project");
    // Should not bail for non-overlapping paths
    // (note: canonicalize will fail for non-existent paths, so normalize_path is used)
    let result = check_repo_path_safety(repo_path, data_dir);
    assert!(
        result.is_ok(),
        "non-overlapping paths should be ok: {result:?}"
    );
}

#[test]
fn test_session_create_rejects_data_dir_as_repo() {
    let data_dir = Path::new("/tmp/clawd_data_test_12345");
    let repo_path = Path::new("/tmp/clawd_data_test_12345");
    let result = check_repo_path_safety(repo_path, data_dir);
    assert!(result.is_err(), "repo_path == data_dir should be rejected");
}
