//! Mutation-targeted tests for provider capabilities and role routing.
//!
//! Written against the gate's surviving-mutant list for `agents/capabilities.rs`
//! (run 53a9d6d4): 12 missed. Ten of them are killable and covered here; the
//! other two are equivalent and are documented at the bottom rather than
//! chased.
//!
//! The existing tests in this module assert which `Provider` comes back. That
//! leaves the cost fields untouched, which is where eight of the twelve
//! mutants live: `3.0 / 1000.0` mutated to `3.0 % 1000.0` (= 3.0) or
//! `3.0 * 1000.0` (= 3000.0). Nothing asserted those numbers at all.

use super::*;

// ─── cost arithmetic ────────────────────────────────────────────────────────

#[test]
fn claude_costs_are_per_token_not_per_thousand() {
    let c = ProviderCapabilities::claude();
    // 3.0 / 1000.0. The `%` mutant yields 3.0 and the `*` mutant 3000.0, so an
    // exact assertion kills both.
    assert!(
        (c.cost_per_1k_tokens_in - 0.003).abs() < 1e-9,
        "input cost: got {}",
        c.cost_per_1k_tokens_in
    );
    assert!(
        (c.cost_per_1k_tokens_out - 0.015).abs() < 1e-9,
        "output cost: got {}",
        c.cost_per_1k_tokens_out
    );
    // Output must cost more than input — a sanity relation that also dies if
    // either division is mangled independently.
    assert!(c.cost_per_1k_tokens_out > c.cost_per_1k_tokens_in);
}

#[test]
fn codex_costs_are_per_token_not_per_thousand() {
    let c = ProviderCapabilities::codex();
    assert!(
        (c.cost_per_1k_tokens_in - 0.0015).abs() < 1e-9,
        "input cost: got {}",
        c.cost_per_1k_tokens_in
    );
    assert!(
        (c.cost_per_1k_tokens_out - 0.006).abs() < 1e-9,
        "output cost: got {}",
        c.cost_per_1k_tokens_out
    );
    assert!(c.cost_per_1k_tokens_out > c.cost_per_1k_tokens_in);
}

#[test]
fn codex_is_the_cheaper_provider_on_both_directions() {
    // A relation between the two constructors, so a mutant that mangles one
    // side's division shows up even if its absolute value were plausible.
    let claude = ProviderCapabilities::claude();
    let codex = ProviderCapabilities::codex();
    assert!(codex.cost_per_1k_tokens_in < claude.cost_per_1k_tokens_in);
    assert!(codex.cost_per_1k_tokens_out < claude.cost_per_1k_tokens_out);
}

#[test]
fn context_windows_and_capability_flags_are_exact() {
    let claude = ProviderCapabilities::claude();
    assert_eq!(claude.max_context_tokens, 200_000);
    assert!(
        !claude.supports_sandbox,
        "Claude Code has no built-in sandbox"
    );
    assert!(claude.supports_mcp);

    let codex = ProviderCapabilities::codex();
    assert_eq!(codex.max_context_tokens, 128_000);
    assert!(
        codex.supports_sandbox,
        "Codex sandboxes network and filesystem"
    );
    assert!(codex.supports_mcp);
}

// ─── role routing ───────────────────────────────────────────────────────────

#[test]
fn router_and_reviewer_roles_go_to_codex() {
    // These two are the only match arms whose result DIFFERS from the `_`
    // default, so they are the only two arm-deletions that are observable.
    // Complexity is ignored by both arms, so several values are checked.
    for complexity in ["low", "high", "", "anything"] {
        assert_eq!(
            recommend_provider("router", complexity),
            Provider::Codex,
            "router/{complexity}"
        );
        assert_eq!(
            recommend_provider("reviewer", complexity),
            Provider::Codex,
            "reviewer/{complexity}"
        );
    }
}

#[test]
fn qa_role_goes_to_codex() {
    assert_eq!(recommend_provider("qa", "low"), Provider::Codex);
    assert_eq!(recommend_provider("qa", "high"), Provider::Codex);
}

#[test]
fn planner_and_implementer_and_unknown_roles_go_to_claude() {
    assert_eq!(recommend_provider("planner", "high"), Provider::Claude);
    assert_eq!(recommend_provider("implementer", "low"), Provider::Claude);
    assert_eq!(
        recommend_provider("something-else", "low"),
        Provider::Claude
    );
    // A planner at a complexity other than "high" falls to the default, which
    // is also Claude — asserted so the arm ordering stays visible.
    assert_eq!(recommend_provider("planner", "low"), Provider::Claude);
}

// NOTE on the two EQUIVALENT mutants in recommend_provider.
//
// Deleting the ("planner", "high") arm, or the ("implementer", _) arm, changes
// nothing observable: both return Provider::Claude, and the `_` fallback they
// drop through to also returns Provider::Claude. No input can distinguish the
// mutant from the original.
//
// They are only killable if the default ever stops being Claude, at which point
// these arms start carrying real meaning. Recorded here so the file's ceiling
// is understood as 10 of 12 rather than treated as a gap.

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

#[test]
fn reviewer_cross_model_claude_to_codex() {
    let ctx = SelectionContext {
        role: "reviewer".to_string(),
        complexity: "medium".to_string(),
        cost_budget_usd: None,
        available_providers: vec![Provider::Claude, Provider::Codex],
        previous_provider: Some(Provider::Claude),
    };
    assert_eq!(select_provider(&ctx), Provider::Codex);
}

#[test]
fn reviewer_cross_model_codex_to_claude() {
    let ctx = SelectionContext {
        role: "reviewer".to_string(),
        complexity: "medium".to_string(),
        cost_budget_usd: None,
        available_providers: vec![Provider::Claude, Provider::Codex],
        previous_provider: Some(Provider::Codex),
    };
    assert_eq!(select_provider(&ctx), Provider::Claude);
}

#[test]
fn implementer_always_claude() {
    let ctx = SelectionContext {
        role: "implementer".to_string(),
        complexity: "high".to_string(),
        cost_budget_usd: None,
        available_providers: vec![Provider::Claude, Provider::Codex],
        previous_provider: None,
    };
    assert_eq!(select_provider(&ctx), Provider::Claude);
}

#[test]
fn qa_prefers_codex() {
    let ctx = SelectionContext {
        role: "qa".to_string(),
        complexity: "low".to_string(),
        cost_budget_usd: None,
        available_providers: vec![Provider::Claude, Provider::Codex],
        previous_provider: None,
    };
    assert_eq!(select_provider(&ctx), Provider::Codex);
}

#[test]
fn falls_back_when_preferred_unavailable() {
    let ctx = SelectionContext {
        role: "implementer".to_string(),
        complexity: "medium".to_string(),
        cost_budget_usd: None,
        available_providers: vec![Provider::Codex],
        previous_provider: None,
    };
    // Claude is preferred but not available — should return Codex.
    assert_eq!(select_provider(&ctx), Provider::Codex);
}
