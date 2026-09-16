//! Tests for the provider output-stream guards.
//!
//! These two rules previously lived inline in the Codex and Cursor runners,
//! where reaching them meant spawning a real child process — so every mutant
//! the gate generated for them survived.  Each test below is pinned to a value
//! that a specific mutation would change.

use super::*;

// ─── is_rate_limit_notice ────────────────────────────────────────────────────

/// Every marker is pinned separately, so no single one can be dropped from the
/// list unnoticed. Each string here matches exactly one marker: dropping that
/// marker makes the line unrecognised and the session stalls with no status
/// event to explain why.
#[test]
fn each_rate_limit_marker_is_recognised_on_its_own() {
    assert!(is_rate_limit_notice("error: rate limit exceeded"));
    assert!(is_rate_limit_notice("error: rate_limit_exceeded"));
    assert!(is_rate_limit_notice("HTTP 400: too many requests"));
    assert!(is_rate_limit_notice("server responded 429"));
}

/// Matching is case-insensitive — pins the `to_lowercase()` call, without
/// which a provider shouting about a RATE LIMIT would be ignored.
#[test]
fn markers_are_matched_regardless_of_case() {
    assert!(is_rate_limit_notice("RATE LIMIT EXCEEDED"));
    assert!(is_rate_limit_notice("Too Many Requests"));
    assert!(is_rate_limit_notice("Rate_Limit"));
}

/// The negative direction, so the assertions above cannot be satisfied by a
/// function that returns `true` for everything.
#[test]
fn ordinary_output_is_not_a_rate_limit_notice() {
    assert!(!is_rate_limit_notice(""));
    assert!(!is_rate_limit_notice("compiling 12 crates"));
    assert!(!is_rate_limit_notice("HTTP 200 OK"));
    // Near misses: a different 4xx, and the words apart from each other.
    assert!(!is_rate_limit_notice("server responded 42"));
    assert!(!is_rate_limit_notice("HTTP 409: conflict"));
    assert!(!is_rate_limit_notice("too many open files"));
}

// ─── would_exceed_cap ────────────────────────────────────────────────────────

/// Kills both `+` -> `*` and `+` -> `-` mutations of the size sum.
///
/// The numbers are chosen so the true sum is distinguishable from every
/// mutation of it: 10 + 5 + 1 = 16, against 10 * 5 + 1 = 51, 10 - 5 + 1 = 6,
/// 10 + 5 * 1 = 15 and 10 + 5 - 1 = 14. With a cap of 15 the real answer is
/// "yes, this line would exceed it", while `+ 5 * 1`, `+ 5 - 1` and `10 - 5`
/// all say no, and `10 * 5` says yes for the wrong reason — so the companion
/// test below pins a case where the products and the sum disagree the other
/// way round.
#[test]
fn a_line_that_would_pass_the_cap_is_refused() {
    assert!(would_exceed_cap(10, 5, 15));
}

/// The other side of the same sum: 10 + 5 + 1 = 16 fits under a cap of 16,
/// but 10 * 5 + 1 = 51 does not. Together with the test above this pins every
/// arithmetic mutation in both directions.
#[test]
fn a_line_that_exactly_fills_the_cap_is_accepted() {
    assert!(!would_exceed_cap(10, 5, 16));
}

/// Kills `>` -> `>=`, `==` and `<`.
///
/// Landing exactly on the cap must be allowed, which separates `>` from `>=`
/// and from `==`; being well under it must be allowed, which separates `>`
/// from `<`. The cap is the largest permitted size, not the first forbidden
/// one, and an off-by-one here silently truncates output that fit.
#[test]
fn the_cap_is_an_inclusive_ceiling() {
    // Exactly at the cap: allowed.
    assert!(!would_exceed_cap(100, 99, 200));
    // One byte past it: refused.
    assert!(would_exceed_cap(100, 100, 200));
    // Far below it: allowed.
    assert!(!would_exceed_cap(1, 1, 200));
    // Far above it: refused.
    assert!(would_exceed_cap(500, 1, 200));
}

/// The newline the caller appends is counted. Without the `+ 1` a line that
/// exactly fills the cap would be accepted and then stored one byte over.
#[test]
fn the_trailing_newline_counts_against_the_cap() {
    // 4 + 5 = 9 bytes of text, plus the newline, is 10 — one past a cap of 9.
    assert!(would_exceed_cap(4, 5, 9));
    assert!(!would_exceed_cap(4, 5, 10));
}

/// An empty line still costs its newline.
#[test]
fn an_empty_line_still_costs_one_byte() {
    assert!(!would_exceed_cap(9, 0, 10));
    assert!(would_exceed_cap(10, 0, 10));
}
