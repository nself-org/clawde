//! Tests for the tool-argument secret scanner.
//!
//! Written against the surviving-mutant list from the mutation gate. The
//! high-entropy branch needs care: the threshold is 4.5 bits, and a
//! 20-character string carries at most log2(20) = 4.32 bits even when every
//! character is distinct. So the shortest string that can possibly trip that
//! branch is 23 characters. The fixtures below use 32 distinct characters
//! (exactly 5.0 bits) with padding sized to move the measurement across the
//! threshold in a controlled direction.

use super::*;

/// 32 distinct alphanumerics — 5.0 bits of entropy, over the 4.5 threshold,
/// and matching none of the six NEVER_EXPOSE_PATTERNS.
const HIGH_ENTROPY: &str = "abcdefghijklmnopqrstuvwxyz012345";

fn detected(v: serde_json::Value) -> bool {
    check_tool_args("bash", &v).is_err()
}

// ─── the high-entropy branch ─────────────────────────────────────────────────

/// Kills `token.len() >= 20` -> `<`.
///
/// `is_high_entropy` returns false below 20 characters, so `len < 20 && ...`
/// can never be true and the entropy branch stops firing entirely — every
/// credential that is not one of the six literal patterns walks through.
#[test]
fn a_bare_high_entropy_token_is_detected() {
    assert!(detected(serde_json::json!({ "arg": HIGH_ENTROPY })));
}

/// Kills `>= 20 && is_high_entropy(..)` -> `||`.
///
/// With `||`, any token of 20+ characters is flagged regardless of entropy, so
/// every long ordinary word becomes a false positive and legitimate tool calls
/// start getting rejected.
#[test]
fn a_long_low_entropy_token_is_not_detected() {
    assert!(!detected(serde_json::json!({ "arg": "a".repeat(30) })));
    assert!(!detected(
        serde_json::json!({ "arg": "aaaabbbbccccddddeeeeffffgggg" })
    ));
}

// ─── the trim rule ───────────────────────────────────────────────────────────

/// Kills four mutations of the `trim_matches` predicate at once: the
/// `delete !`, both `!=` -> `==` comparisons, and the first `&&` -> `||`.
///
/// The real predicate trims surrounding punctuation while keeping the base64
/// alphabet. Wrapped in 40 dots either side, the untrimmed word measures 2.29
/// bits and would be waved through; trimmed, it is the 5.0-bit token it
/// actually contains.
///
/// * dropping the `!` trims alphanumerics instead, so the scan stops at the
///   first dot and the token stays padded;
/// * `c == '+'` or `c == '/'` narrows the trim to that one character, so the
///   dots are never removed;
/// * the first `&&` -> `||` makes the predicate true for alphanumerics too, so
///   the token is trimmed away to nothing.
///
/// All four leave a real credential sitting in the tool arguments.
#[test]
fn a_token_buried_in_punctuation_is_still_detected() {
    let padded = format!("{}{}{}", ".".repeat(40), HIGH_ENTROPY, ".".repeat(40));
    assert!(detected(serde_json::json!({ "arg": padded })));
}

/// Kills the second `&&` -> `||` in the same predicate.
///
/// `+` and `/` are deliberately KEPT by the trim: they are part of the base64
/// alphabet and belong to the token rather than around it. Sixteen leading `+`
/// therefore count towards the measurement and pull it to 4.25 bits, below the
/// threshold. The mutation trims them off, leaving the bare 5.0-bit token, and
/// the scan fires.
///
/// So this asserts a NEGATIVE, and pins the documented trim rule rather than
/// an ideal outcome. A padded token slipping under the entropy threshold is a
/// real limitation of the heuristic; this test records it, it does not bless
/// it.
#[test]
fn the_base64_alphabet_is_kept_by_the_trim() {
    let plus_padded = format!("{}{}", "+".repeat(16), HIGH_ENTROPY);
    assert!(!detected(serde_json::json!({ "arg": plus_padded })));
}

// ─── recursion into containers ───────────────────────────────────────────────

/// Kills the deletion of the `Value::Array` match arm — with it gone, arrays
/// fall through to `_ => {}` and anything inside one is never scanned.
#[test]
fn secrets_inside_arrays_are_detected() {
    assert!(detected(
        serde_json::json!({ "args": ["ok", HIGH_ENTROPY] })
    ));
    // Nested deeper, so the arm must recurse rather than peek one level.
    assert!(detected(
        serde_json::json!([["ok"], [{ "k": HIGH_ENTROPY }]])
    ));
}

#[test]
fn secrets_inside_nested_objects_are_detected() {
    assert!(detected(
        serde_json::json!({ "outer": { "inner": HIGH_ENTROPY } })
    ));
}

/// The negative direction for the container walk: ordinary structured
/// arguments must pass untouched.
#[test]
fn clean_arguments_of_every_shape_are_allowed() {
    assert!(!detected(serde_json::json!({ "command": "cargo test" })));
    assert!(!detected(serde_json::json!({ "files": ["a.rs", "b.rs"] })));
    assert!(!detected(
        serde_json::json!({ "n": 42, "ok": true, "x": null })
    ));
    assert!(!detected(serde_json::json!({})));
}

// ─── the pattern list ────────────────────────────────────────────────────────

/// Each NEVER_EXPOSE pattern is pinned separately, so none can be dropped from
/// the list unnoticed.
#[test]
fn every_never_expose_pattern_is_matched() {
    let ghp = format!("ghp_{}", "A".repeat(36));
    let pat = format!("github_pat_{}", "A".repeat(82));
    let cases = [
        "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAA",
        ghp.as_str(),
        pat.as_str(),
        "AKIAIOSFODNN7EXAMPLE",
        "-----BEGIN RSA PRIVATE KEY-----",
        "password: hunter2hunter2",
    ];
    for c in cases {
        assert!(
            detected(serde_json::json!({ "arg": c })),
            "should have detected {c:?}"
        );
    }
}

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

use serde_json::json;

#[test]
fn clean_args_pass() {
    let args = json!({ "path": "src/main.rs", "content": "fn main() {}" });
    assert!(check_tool_args("read_file", &args).is_ok());
}

#[test]
fn openai_key_in_args_blocked() {
    let args = json!({ "key": "sk-abcdefghijklmnopqrstuvwxyz1234567890" });
    let result = check_tool_args("apply_patch", &args);
    assert!(result.is_err());
    assert!(matches!(
        result,
        Err(PolicyViolation::SecretDetected { .. })
    ));
}

#[test]
fn nested_secret_blocked() {
    let args = json!({
        "config": {
            "api_key": "sk-abcdefghijklmnopqrstuvwxyz1234567890"
        }
    });
    let result = check_tool_args("apply_patch", &args);
    assert!(result.is_err());
}

#[test]
fn aws_key_blocked() {
    let args = json!({ "credentials": "AKIAIOSFODNN7EXAMPLE1234" });
    let result = check_tool_args("run_tests", &args);
    assert!(result.is_err());
}
