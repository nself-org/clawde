//! Tests for confidence parsing and reasoning-sentence extraction.
//!
//! Written against the gate's actual surviving-mutant list for this file
//! (Mutation Testing run 34917215492): 33 missed, and all 33 sit in just three
//! private helpers — `find_sentence_start` (18), `find_sentence_end` (13) and
//! the 200-char cap in `extract_reasoning_sentence` (2).
//!
//! The existing tests only exercised those helpers *indirectly*, through
//! `parse_confidence`, and asserted with `contains`. That is why 33 mutants
//! lived: index arithmetic can be wrong by one in either direction and a
//! `contains` check still passes. These tests call the helpers directly and
//! assert **exact indices**.
//!
//! The four pre-existing tests are kept verbatim at the end.

use super::*;

// ─── find_sentence_start ────────────────────────────────────────────────────
// Returns the byte index just after the previous sentence boundary: the last
// '\n', or the last '.' that is followed by a space. Mutants here include
// `-> 0`, `-> 1`, `i > 0` -> `i < 0`, `==` -> `!=`, `&&` -> `||`, and every
// `+`/`<` in the index arithmetic.

#[test]
fn find_sentence_start_returns_zero_when_there_is_no_boundary() {
    // Kills `-> 1`: the correct answer here is exactly 0.
    assert_eq!(find_sentence_start(""), 0);
    assert_eq!(find_sentence_start("no boundary here"), 0);
}

#[test]
fn find_sentence_start_returns_the_index_after_the_last_newline() {
    // "hello\nworld" — '\n' at 5, so the sentence starts at 6.
    // Kills `-> 0`, and `i + 1` -> `i - 1` / `i * 1`.
    assert_eq!(find_sentence_start("hello\nworld"), 6);
    assert_eq!(find_sentence_start("a\nb\nc"), 4, "the LAST newline wins");
    assert_eq!(find_sentence_start("\nx"), 1);
}

#[test]
fn find_sentence_start_returns_the_index_after_a_period_space() {
    // "One. Two" — '.' at 3 followed by ' ', so the next sentence starts at 4
    // (the space itself, which the caller trims).
    assert_eq!(find_sentence_start("One. Two"), 4);
    assert_eq!(find_sentence_start("A. B. C"), 5, "the LAST '. ' wins");
}

#[test]
fn find_sentence_start_ignores_a_period_not_followed_by_a_space() {
    // A decimal point must not be read as a sentence boundary — this is the
    // whole reason the `bytes[i + 1] == b' '` half of the condition exists.
    // Kills `&&` -> `||` (which would return on any period) and the
    // `==` -> `!=` mutants.
    assert_eq!(find_sentence_start("score 0.9 is fine"), 0);
    assert_eq!(find_sentence_start("a.b.c.d"), 0);
}

#[test]
fn find_sentence_start_ignores_a_trailing_period_with_nothing_after_it() {
    // "abc." — the '.' is the last byte, so `i + 1 < bytes.len()` is false and
    // it is NOT a boundary. Kills `<` -> `<=` / `==` / `>` at that guard, and
    // the `i + 1` arithmetic inside it.
    assert_eq!(find_sentence_start("abc."), 0);
    assert_eq!(
        find_sentence_start("one. two."),
        4,
        "the trailing '.' has nothing after it, so the inner '. ' at index 3 wins"
    );
}

#[test]
fn find_sentence_start_prefers_whichever_boundary_is_latest() {
    // A newline after a '. ' and vice versa — the backward scan returns at the
    // first boundary it meets, i.e. the rightmost one.
    assert_eq!(find_sentence_start("One. Two\nThree"), 9);
    assert_eq!(find_sentence_start("One\nTwo. Three"), 8);
}

// ─── find_sentence_end ──────────────────────────────────────────────────────
// Returns Some(offset + i + 1) at the first '\n', or at a '.' followed by a
// space, a newline, or end-of-string. Mutants include `-> None`, `||` -> `&&`,
// and every `+` in `offset + i + 1`.

#[test]
fn find_sentence_end_returns_none_when_there_is_no_terminator() {
    assert_eq!(find_sentence_end("no terminator", 0), None);
    assert_eq!(find_sentence_end("", 0), None);
    // A bare decimal is not a terminator.
    assert_eq!(find_sentence_end("0.9", 0), None);
}

#[test]
fn find_sentence_end_stops_at_the_first_newline() {
    // '\n' at index 3 -> Some(0 + 3 + 1) = Some(4).
    // Kills `-> None` and the `i + 1` arithmetic.
    assert_eq!(find_sentence_end("abc\ndef", 0), Some(4));
    assert_eq!(find_sentence_end("\nx", 0), Some(1));
}

#[test]
fn find_sentence_end_stops_at_a_period_followed_by_a_space() {
    // '.' at 5, followed by ' ' -> Some(6).
    assert_eq!(find_sentence_end("Hello. World", 0), Some(6));
}

#[test]
fn find_sentence_end_stops_at_a_period_followed_by_a_newline() {
    // The second arm of the `||` chain. Kills `||` -> `&&`, which would
    // require the period to be simultaneously at end-of-string AND followed by
    // a space AND a newline — impossible, so every case would return None.
    assert_eq!(find_sentence_end("Hello.\nWorld", 0), Some(6));
}

#[test]
fn find_sentence_end_stops_at_a_period_at_end_of_string() {
    // The first arm: `i + 1 >= bytes.len()`. '.' at 4 is the last byte -> Some(5).
    assert_eq!(find_sentence_end("Done.", 0), Some(5));
}

#[test]
fn find_sentence_end_skips_a_decimal_point_and_finds_the_real_end() {
    // "0.9 done." — the '.' at 1 is followed by '9' so it is skipped; the '.'
    // at 8 ends the string -> Some(9). This is the case that proves the
    // decimal guard and the terminator search both work in one string.
    assert_eq!(find_sentence_end("0.9 done.", 0), Some(9));
}

#[test]
fn find_sentence_end_adds_the_offset_to_the_returned_index() {
    // The offset is how the caller maps back into the original content. The
    // same input at a non-zero offset must shift by exactly that amount —
    // which is what kills `offset + i` -> `offset - i` / `offset * i`.
    assert_eq!(find_sentence_end("abc\ndef", 0), Some(4));
    assert_eq!(find_sentence_end("abc\ndef", 100), Some(104));
    assert_eq!(find_sentence_end("Hello. World", 7), Some(13));
    // offset 1 is the case where `*` and `+` would otherwise agree on small
    // numbers, so it is asserted explicitly.
    assert_eq!(find_sentence_end("abc\ndef", 1), Some(5));
}

// ─── extract_reasoning_sentence: the 200-char cap ───────────────────────────
// Mutants: `len() > 200` -> `== 200` and `>= 200`.

#[test]
fn reasoning_is_returned_whole_at_exactly_200_characters() {
    // Exactly 200 must NOT be truncated. Kills `>=` (which would truncate) and
    // `==` (which would truncate only here).
    let body = "x".repeat(200);
    let out = extract_reasoning_sentence(&body, 0);
    assert_eq!(out.len(), 200);
    assert!(!out.ends_with('…'), "200 chars must not be capped: {out}");
}

#[test]
fn reasoning_is_capped_with_an_ellipsis_above_200_characters() {
    // 201 must be truncated to 200 plus the ellipsis. Kills `==` (which would
    // leave 201 untouched).
    let body = "y".repeat(201);
    let out = extract_reasoning_sentence(&body, 0);
    assert!(out.ends_with('…'), "over 200 must be capped: {out}");
    assert_eq!(
        out.chars().count(),
        201,
        "200 kept characters plus one ellipsis"
    );
}

#[test]
fn reasoning_below_the_cap_is_returned_untouched_and_trimmed() {
    let out = extract_reasoning_sentence("  short reasoning  ", 0);
    assert_eq!(out, "short reasoning");
}

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

#[test]
fn test_parse_confidence_colon_format() {
    let msg = "The refactoring is complete. Confidence: 0.85 — all tests pass.";
    let result = parse_confidence(msg).unwrap();
    assert!((result.score - 0.85).abs() < 0.001);
}

#[test]
fn test_parse_confidence_my_format() {
    let msg = "My confidence is 0.7 because there are untested edge cases.";
    let result = parse_confidence(msg).unwrap();
    assert!((result.score - 0.7).abs() < 0.001);
}

#[test]
fn test_no_confidence_marker() {
    let msg = "Done. The file has been updated successfully.";
    assert!(parse_confidence(msg).is_none());
}

#[test]
fn test_score_clamped_to_one() {
    let msg = "Confidence: 1.5 — extremely confident.";
    let result = parse_confidence(msg).unwrap();
    assert!((result.score - 1.0).abs() < 0.001);
}

#[test]
fn test_reasoning_extracted() {
    let msg = "All tests pass. Confidence: 0.9 — I verified every branch.";
    let result = parse_confidence(msg).unwrap();
    assert!(result.reasoning.contains("0.9"));
}
