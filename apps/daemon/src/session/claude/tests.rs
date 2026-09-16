//! Tests for the Claude runner's tool-risk and argument-validation helpers.
//!
//! The first group is written against the surviving-mutant list from the
//! mutation gate. Both helpers are long `||` chains, and a `||` -> `&&`
//! mutation at position *i* pairs the terms on either side of it: the chain
//! only notices if some input matches exactly one of that pair. So each test
//! below uses a name that trips exactly one link in the chain.

use super::*;

// ─── classify_tool_risk ──────────────────────────────────────────────────────

/// Kills the `||` -> `&&` mutations guarding the `kill` and `terminal` terms
/// of the high-risk chain.
///
/// `kill_process` matches `kill` and nothing else, so pairing `kill` with
/// either neighbour (`remove` before it, `terminal` after it) collapses the
/// chain and the tool is downgraded to "low" — a shell-killing tool silently
/// reclassified as read-only.
#[test]
fn process_control_tools_are_high_risk_on_every_keyword() {
    assert_eq!(classify_tool_risk("kill_process"), "high");
    assert_eq!(classify_tool_risk("terminal_session"), "high");
}

/// Kills the four `||` -> `&&` mutations in the tail of the medium chain
/// (`overwrite`, `replace`, `insert`, `append`).
///
/// Each name matches exactly one of those terms and none of its neighbours,
/// so every pairing is observable. `apply_patch` covers the `patch`/`overwrite`
/// pair from the `patch` side, which is the only side that can be isolated:
/// any name containing "overwrite" also contains "write" and would be caught
/// by the first term of the chain regardless.
#[test]
fn content_modifying_tools_are_medium_risk_on_every_keyword() {
    assert_eq!(classify_tool_risk("apply_patch"), "medium");
    assert_eq!(classify_tool_risk("replace_text"), "medium");
    assert_eq!(classify_tool_risk("insert_line"), "medium");
    assert_eq!(classify_tool_risk("append_to_file"), "medium");
}

/// The default arm: a name matching no keyword in either chain is "low".
/// Pins that the two chains do not fall through into each other.
#[test]
fn read_only_tools_fall_through_to_low() {
    assert_eq!(classify_tool_risk("glob"), "low");
    assert_eq!(classify_tool_risk("todo_list"), "low");
}

/// Classification is case-insensitive — pins the `to_lowercase()` call.
#[test]
fn classification_ignores_case() {
    assert_eq!(classify_tool_risk("KillProcess"), "high");
    assert_eq!(classify_tool_risk("AppendToFile"), "medium");
}

// ─── validate_tool_args ──────────────────────────────────────────────────────

fn blocked(v: &ArgValidation) -> bool {
    matches!(v, ArgValidation::Blocked(_))
}

/// Kills the `||` -> `&&` mutations guarding the `run` and `terminal` terms of
/// the `is_shell_tool` chain.
///
/// `run_shell` matches `run` alone. Pairing it with `execute` before it or
/// `terminal` after it makes `is_shell_tool` false, and the injection check is
/// skipped entirely — the command is waved through.
#[test]
fn a_run_prefixed_tool_is_recognised_as_a_shell_tool() {
    let input = serde_json::json!({ "command": "echo one && rm -rf /tmp/x" });
    assert!(blocked(&validate_tool_args("run_shell", &input)));
}

/// Kills the `||` -> `&&` mutation guarding the `command` term.
///
/// `terminal` matches `terminal` alone; pairing it with `command` drops it out
/// of the chain.
#[test]
fn a_terminal_tool_is_recognised_as_a_shell_tool() {
    let input = serde_json::json!({ "command": "ls `whoami`" });
    assert!(blocked(&validate_tool_args("terminal", &input)));
}

/// The `cmd` field is checked as well as `command`, and each injection
/// indicator is pinned separately so no single one can be dropped unnoticed.
#[test]
fn every_injection_indicator_is_caught_in_either_field() {
    for cmd in ["a && b", "a || b", "a `b`", "a $(b)"] {
        assert!(
            blocked(&validate_tool_args(
                "bash",
                &serde_json::json!({ "command": cmd })
            )),
            "command field should block {cmd:?}"
        );
        assert!(
            blocked(&validate_tool_args(
                "bash",
                &serde_json::json!({ "cmd": cmd })
            )),
            "cmd field should block {cmd:?}"
        );
    }
}

/// Every destructive pattern is pinned individually.
#[test]
fn every_destructive_pattern_is_blocked() {
    for cmd in [
        "rm -rf /",
        "rm -rf ~",
        "rm -rf $HOME",
        "rm --no-preserve-root /",
        "chmod 777 /etc",
        "mkfs.ext4 /dev/sda",
        ":(){:|:&};:",
    ] {
        assert!(
            blocked(&validate_tool_args(
                "bash",
                &serde_json::json!({ "command": cmd })
            )),
            "should block {cmd:?}"
        );
    }
}

/// A plain command through a shell tool is allowed — the negative direction,
/// so the checks above cannot be satisfied by blocking everything.
#[test]
fn an_ordinary_shell_command_is_allowed() {
    let input = serde_json::json!({ "command": "cargo test --lib" });
    assert!(!blocked(&validate_tool_args("bash", &input)));
}

/// A non-shell tool is not subjected to the command checks at all.
#[test]
fn a_non_shell_tool_skips_the_command_checks() {
    let input = serde_json::json!({ "command": "rm -rf /" });
    assert!(!blocked(&validate_tool_args("read_file", &input)));
}

/// Kills the `delete !` mutation on `!home.is_empty()`.
///
/// With the `!` dropped the guard requires HOME to be *unset*, so with a real
/// HOME nothing is ever blocked and any absolute path escapes.
#[test]
fn an_absolute_path_outside_home_is_blocked() {
    assert!(
        !std::env::var("HOME").unwrap_or_default().is_empty(),
        "this test needs HOME set to mean anything"
    );
    for key in ["path", "file_path", "filename", "target"] {
        let input = serde_json::json!({ key: "/etc/shadow" });
        assert!(
            blocked(&validate_tool_args("read_file", &input)),
            "{key} should be checked"
        );
    }
}

/// Kills `&&` -> `||` and the `delete !` on `starts_with` in the same guard.
///
/// Both mutations invert the home check: `||` makes it true for every absolute
/// path, and dropping the `!` blocks exactly the paths that should be allowed.
/// Either way an agent loses access to the user's own working tree.
#[test]
fn an_absolute_path_inside_home_is_allowed() {
    let home = std::env::var("HOME").unwrap_or_default();
    assert!(
        !home.is_empty(),
        "this test needs HOME set to mean anything"
    );

    let input = serde_json::json!({ "path": format!("{home}/notes.txt") });
    assert!(!blocked(&validate_tool_args("read_file", &input)));
}

/// A relative path is never subjected to the home check.
#[test]
fn a_relative_path_is_allowed() {
    let input = serde_json::json!({ "path": "src/main.rs" });
    assert!(!blocked(&validate_tool_args("read_file", &input)));
}

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

#[test]
fn risk_classifier_high() {
    assert_eq!(classify_tool_risk("bash"), "high");
    assert_eq!(classify_tool_risk("execute_command"), "high");
    assert_eq!(classify_tool_risk("delete_file"), "high");
    assert_eq!(classify_tool_risk("computer_use"), "high");
}

#[test]
fn risk_classifier_medium() {
    assert_eq!(classify_tool_risk("write_file"), "medium");
    assert_eq!(classify_tool_risk("edit_file"), "medium");
    assert_eq!(classify_tool_risk("create_file"), "medium");
}

#[test]
fn risk_classifier_low() {
    assert_eq!(classify_tool_risk("read_file"), "low");
    assert_eq!(classify_tool_risk("list_directory"), "low");
    assert_eq!(classify_tool_risk("glob_search"), "low");
}

/// Confirms the cancel-during-output guard: once the cancelled flag is set,
/// the event_loop must break before processing any further lines.  The full
/// scenario (real child process + flag racing with kill) is covered by the
/// e2e WebSocket integration tests in tests/e2e_websocket.rs.
#[test]
fn cancelled_flag_semantics() {
    use std::sync::atomic::{AtomicBool, Ordering};
    let cancelled = AtomicBool::new(false);
    // Simulate the guard: before the flag is set, loop should proceed.
    assert!(!cancelled.load(Ordering::Acquire));
    cancelled.store(true, Ordering::Release);
    // After stop() sets the flag, the loop sees it and exits.
    assert!(cancelled.load(Ordering::Acquire));
}

// ── validate_tool_args ──────────────────────────────────────────────────

fn val(tool: &str, json: &str) -> ArgValidation {
    validate_tool_args(tool, &serde_json::from_str(json).unwrap())
}

#[test]
fn safe_command_passes() {
    assert_eq!(val("bash", r#"{"command":"ls -la"}"#), ArgValidation::Ok);
    assert_eq!(
        val("execute", r#"{"command":"cargo build"}"#),
        ArgValidation::Ok
    );
}

#[test]
fn double_ampersand_blocked() {
    assert!(matches!(
        val("bash", r#"{"command":"make && rm -rf ."}"#),
        ArgValidation::Blocked(_)
    ));
}

#[test]
fn pipe_blocked() {
    // Pipes alone are actually fine; only && and || trigger injection check.
    // (Pipes are legitimate in shell commands.)
    // Backtick injection IS blocked.
    assert!(matches!(
        val("bash", r#"{"command":"echo `whoami`"}"#),
        ArgValidation::Blocked(_)
    ));
}

#[test]
fn destructive_rm_rf_blocked() {
    assert!(matches!(
        val("bash", r#"{"command":"sudo rm -rf / --no-preserve-root"}"#),
        ArgValidation::Blocked(_)
    ));
    assert!(matches!(
        val("bash", r#"{"command":"rm -rf /"}"#),
        ArgValidation::Blocked(_)
    ));
}

#[test]
fn chmod_777_blocked() {
    assert!(matches!(
        val("bash", r#"{"command":"chmod 777 /etc/passwd"}"#),
        ArgValidation::Blocked(_)
    ));
}

#[test]
fn fork_bomb_blocked() {
    assert!(matches!(
        val("bash", r#"{"command":":(){:|:&};:"}"#),
        ArgValidation::Blocked(_)
    ));
}

#[test]
fn non_shell_tool_passes_freely() {
    // A read_file tool with a path field — path is not absolute, should pass.
    assert_eq!(
        val("read_file", r#"{"path":"src/main.rs"}"#),
        ArgValidation::Ok
    );
}
