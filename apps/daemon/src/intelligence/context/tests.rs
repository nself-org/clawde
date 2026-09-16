//! Tests for the context-window optimizer.
//!
//! The first group is written against the surviving-mutant list from the
//! mutation gate: every assertion below is pinned to an exact value that a
//! specific arithmetic or boundary mutation would change.  See the comment on
//! each test for the mutant it kills.

use super::*;

/// Build a message with `n` bytes of ASCII content.
fn msg(role: &str, n: usize, pinned: bool) -> ContextMessage {
    ContextMessage {
        role: role.to_owned(),
        content: "x".repeat(n),
        pinned,
    }
}

fn cfg(max_tokens: usize, response_reserve_tokens: usize) -> ContextConfig {
    ContextConfig {
        max_tokens,
        response_reserve_tokens,
    }
}

// ─── truncate_to_tokens ──────────────────────────────────────────────────────

/// Kills `char_limit = max_tokens * 4` -> `max_tokens + 4`, and all three
/// boundary mutations of `take_while(|&i| i < char_limit.saturating_sub(3))`
/// (`<=`, `==`, `>`).
///
/// 40 chars at max_tokens=4: char_limit = 16, so the take_while limit is 13 and
/// the last byte index below it is 12 — exactly 12 `x` plus the ellipsis.
/// Under `+` the limit is 8-3=5 and only 4 chars survive; under `<=` 13 survive;
/// under `==` and `>` the very first index fails the predicate, the iterator is
/// empty, and `unwrap_or(0)` yields a bare ellipsis.
#[test]
fn truncation_cuts_at_the_exact_computed_boundary() {
    assert_eq!(
        truncate_to_tokens(&"x".repeat(40), 4),
        format!("{}…", "x".repeat(12))
    );
}

#[test]
fn text_within_budget_is_returned_untouched() {
    // 16 chars, limit 4*4 = 16 — the `<=` branch, no ellipsis.
    let text = "x".repeat(16);
    assert_eq!(truncate_to_tokens(&text, 4), text);
}

#[test]
fn zero_budget_yields_empty_string() {
    assert_eq!(truncate_to_tokens("anything at all", 0), "");
}

#[test]
fn estimate_tokens_rounds_up() {
    // Ceiling division: the answer must differ from both len/4 and len*4.
    assert_eq!(estimate_tokens(""), 0);
    assert_eq!(estimate_tokens("x"), 1);
    assert_eq!(estimate_tokens("xxxx"), 1);
    assert_eq!(estimate_tokens("xxxxx"), 2);
    assert_eq!(estimate_tokens(&"x".repeat(9)), 3);
}

// ─── optimize_context: message partitioning ──────────────────────────────────

/// Kills `msg.pinned || msg.role == "system"` -> `&&` at the partition step.
///
/// The system message here is NOT flagged `pinned`, so under `&&` it falls into
/// the regular pool: its 9 tokens stop being charged against the budget up
/// front and it competes for a slot instead.  That frees enough room for a
/// third regular message, and because the final filter still lets the system
/// message through on its role, the result grows from 3 to 4.
#[test]
fn unpinned_system_message_is_charged_before_regular_messages() {
    let messages = vec![
        msg("system", 12, false), // 2 + 3 + 4 = 9 tokens
        msg("user", 20, false),   // 1 + 5 + 4 = 10 tokens each
        msg("user", 20, false),
        msg("user", 20, false),
    ];
    // budget = 50 - 10 = 40; minus 9 pinned = 31 remaining -> exactly 2 fit.
    let out = optimize_context(&messages, &cfg(50, 10));
    assert_eq!(out.len(), 3);
    assert_eq!(out[0].role, "system");
}

/// Kills both `+` -> `*` mutations in the pinned-token sum
/// (`estimate_tokens(role) + estimate_tokens(content) + 4`).
///
/// role "system" = 2 tokens, content = 9 tokens. The real cost is 2+9+4 = 15,
/// leaving 65 of the 80-token budget and room for 4 regular messages at 16
/// tokens each. Mutating the first `+` gives 2*9+4 = 22 (58 left -> 3 fit);
/// mutating the second gives 2+9*4 = 38 (42 left -> 2 fit).
#[test]
fn pinned_cost_is_a_sum_not_a_product() {
    let mut messages = vec![msg("system", 36, false)];
    messages.extend((0..8).map(|_| msg("user", 44, false))); // 1 + 11 + 4 = 16
    let out = optimize_context(&messages, &cfg(100, 20));
    // 1 system + 4 regular.
    assert_eq!(out.len(), 5);
}

/// Kills both `+` -> `*` mutations in the per-message `cost`.
///
/// role "assistant" = 3 tokens, content = 5 tokens: real cost 3+5+4 = 12, so 6
/// of the 8 messages fit in 80 tokens. First `+` mutated -> 3*5+4 = 19 (4 fit);
/// second `+` mutated -> 3+5*4 = 23 (3 fit).
#[test]
fn regular_cost_is_a_sum_not_a_product() {
    let messages: Vec<_> = (0..8).map(|_| msg("assistant", 20, false)).collect();
    let out = optimize_context(&messages, &cfg(100, 20));
    assert_eq!(out.len(), 6);
}

/// Kills `remaining -= cost` -> `+=` and `/=`, and `remaining < 16` -> `<=`/`==`.
///
/// budget = 80, cost = 16, so `remaining` steps 80 -> 64 -> 48 -> 32 -> 16 -> 0
/// and exactly 5 of the 8 messages are taken. `remaining` lands on 16 after the
/// 4th, which is the discriminator: `< 16` lets the 5th through, `<= 16` and
/// `== 16` both stop at 4. Under `+=` the budget never falls and all 8 are
/// taken; under `/=` it collapses to 80/16 = 5 after the first and only 1 is.
#[test]
fn budget_decrements_and_stops_strictly_below_sixteen() {
    let messages: Vec<_> = (0..8).map(|_| msg("user", 44, false)).collect();
    let out = optimize_context(&messages, &cfg(100, 20));
    assert_eq!(out.len(), 5);
}

/// The surviving messages must be the NEWEST ones, in chronological order —
/// pins the `.rev()` walk and the `selected.reverse()` that follows it.
#[test]
fn the_newest_messages_survive_in_chronological_order() {
    let messages: Vec<_> = (0..8)
        .map(|i| ContextMessage {
            role: "user".to_owned(),
            content: format!("{i}{}", "x".repeat(43)),
            pinned: false,
        })
        .collect();
    let out = optimize_context(&messages, &cfg(100, 20));
    let first_chars: Vec<char> = out
        .iter()
        .map(|m| m.content.chars().next().unwrap())
        .collect();
    assert_eq!(first_chars, vec!['3', '4', '5', '6', '7']);
}

// ─── optimize_context: the trailing-truncation guard ─────────────────────────

/// Kills `last.role != "system" && !last.pinned` -> `||`, and the `delete !`
/// mutation of the same condition.
///
/// A pinned message is included whatever the budget, so this one (50 tokens)
/// sits in a 20-token result. Both mutations make the guard true and truncate
/// it; the real code leaves a pinned message alone by design.
#[test]
fn a_pinned_message_is_never_truncated_even_when_it_blows_the_budget() {
    let messages = vec![msg("user", 200, true)];
    let out = optimize_context(&messages, &cfg(30, 10));
    assert_eq!(out.len(), 1);
    assert_eq!(out[0].content.chars().count(), 200);
    assert!(!out[0].content.contains('…'));
}

/// Kills `last.role != "system"` -> `==`.
///
/// Same shape, but the oversized message is a system message that is not
/// flagged `pinned` — so under `==` the guard passes and the system prompt gets
/// truncated. Silently trimming the system prompt is the bug this pins.
#[test]
fn an_oversized_system_message_is_never_truncated() {
    let messages = vec![msg("system", 200, false)];
    let out = optimize_context(&messages, &cfg(30, 10));
    assert_eq!(out.len(), 1);
    assert_eq!(out[0].content.chars().count(), 200);
    assert!(!out[0].content.contains('…'));
}

// NOTE — equivalent mutants, deliberately not chased:
//
// The `full_cost > budget` comparison inside that guard (and its `<`, `==`,
// `>=` mutations) is unreachable in unmutated code. The guard only fires for a
// message that is neither pinned nor a system message, and such a message
// reaches the result only by being selected — which required
// `role_tokens + full_cost + 4 <= remaining <= budget`, so `full_cost` is
// always strictly less than `budget`. No input can distinguish those variants;
// they are equivalent, not gaps in this suite.

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

fn make_msg(role: &str, content: &str, pinned: bool) -> ContextMessage {
    ContextMessage {
        role: role.to_owned(),
        content: content.to_owned(),
        pinned,
    }
}

#[test]
fn test_estimate_tokens_empty() {
    assert_eq!(estimate_tokens(""), 0);
}

#[test]
fn test_estimate_tokens_four_chars() {
    // 4 chars = 1 token
    assert_eq!(estimate_tokens("abcd"), 1);
}

#[test]
fn test_estimate_tokens_five_chars() {
    // 5 chars → ceil(5/4) = 2
    assert_eq!(estimate_tokens("abcde"), 2);
}

#[test]
fn test_truncate_exact_fit() {
    let s = "abcd"; // 1 token
    assert_eq!(truncate_to_tokens(s, 1), s);
}

#[test]
fn test_truncate_over_limit() {
    let s = "a".repeat(100);
    let result = truncate_to_tokens(&s, 5); // 5 tokens = 20 chars
    assert!(result.len() < s.len(), "should be shorter");
    assert!(result.ends_with('…'), "should end with ellipsis");
}

#[test]
fn test_truncate_zero_limit() {
    let result = truncate_to_tokens("hello", 0);
    assert!(result.is_empty());
}

#[test]
fn test_optimize_keeps_all_within_budget() {
    let messages = vec![
        make_msg("system", "You are a helpful assistant.", false),
        make_msg("user", "Hello!", false),
        make_msg("assistant", "Hi there!", false),
    ];
    let config = ContextConfig {
        max_tokens: 10_000,
        response_reserve_tokens: 500,
    };
    let result = optimize_context(&messages, &config);
    assert_eq!(result.len(), 3, "all 3 messages should fit");
}

#[test]
fn test_optimize_drops_old_messages_first() {
    // Create a tight budget so only system + last user message fit.
    let system_content = "sys";
    let old_user = "a".repeat(1000);
    let new_user = "new question";

    let messages = vec![
        make_msg("system", system_content, false),
        make_msg("user", &old_user, false),
        make_msg("user", new_user, false),
    ];

    // Budget: system (~1 tok) + new_user (~3 tok) + overhead = ~20.
    // old_user (250 tok) should be dropped.
    let config = ContextConfig {
        max_tokens: 30,
        response_reserve_tokens: 4,
    };
    let result = optimize_context(&messages, &config);

    // System must be there.
    assert!(result.iter().any(|m| m.role == "system"));
    // New user message must be there.
    assert!(result.iter().any(|m| m.content == new_user));
    // Old (1000-char) message must be dropped.
    assert!(!result.iter().any(|m| m.content == old_user));
}

#[test]
fn test_optimize_pinned_always_included() {
    let pinned_msg = make_msg("user", "important pinned message", true);
    let other = make_msg("user", "regular message", false);

    let messages = vec![pinned_msg, other];
    // Very tight budget — only the pinned message can fit.
    let config = ContextConfig {
        max_tokens: 10,
        response_reserve_tokens: 0,
    };
    let result = optimize_context(&messages, &config);
    assert!(
        result.iter().any(|m| m.pinned),
        "pinned message must survive budget cuts"
    );
}

#[test]
fn test_optimize_empty_input() {
    let result = optimize_context(&[], &ContextConfig::default());
    assert!(result.is_empty());
}
