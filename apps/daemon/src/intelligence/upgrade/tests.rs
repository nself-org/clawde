//! Tests for the model-upgrade helpers.
//!
//! Written against the surviving-mutant list from the mutation gate.

use super::*;

// ─── model_tier ──────────────────────────────────────────────────────────────

/// Kills the `3` -> `0` and `3` -> `1` return-value mutations, and the
/// `contains("opus") || lower == "opus"` -> `&&` pairing.
///
/// A real model id contains "opus" without equalling it, so the `&&` pairing
/// drops opus to the fallback tier — and the cap check in `upgrade_model`
/// then stops rejecting upgrades that exceed the configured maximum.
#[test]
fn opus_models_are_tier_three_however_they_are_named() {
    assert_eq!(model_tier("opus"), 3);
    assert_eq!(model_tier("claude-3-opus-20240229"), 3);
    assert_eq!(model_tier("CLAUDE-OPUS-4"), 3);
}

/// The same shape for the sonnet arm.
#[test]
fn sonnet_models_are_tier_two_however_they_are_named() {
    assert_eq!(model_tier("sonnet"), 2);
    assert_eq!(model_tier("claude-sonnet-4-6"), 2);
    assert_eq!(model_tier("CLAUDE-SONNET-4"), 2);
}

/// Kills both `==` -> `!=` mutations.
///
/// With `lower != "opus"` in place of `==`, every model that is not the
/// literal string "opus" reports as tier 3 — haiku included — which would make
/// the cap check wave through every upgrade. Asserting the LOW tier is what
/// catches it; asserting only the high tiers would not.
#[test]
fn anything_below_sonnet_is_tier_one() {
    assert_eq!(model_tier("haiku"), 1);
    assert_eq!(model_tier("claude-3-5-haiku-20241022"), 1);
    assert_eq!(model_tier(""), 1);
    assert_eq!(model_tier("gpt-5"), 1);
}

/// Strict ordering is the property the cap check actually relies on.
#[test]
fn tiers_are_strictly_ordered() {
    assert!(model_tier("haiku") < model_tier("sonnet"));
    assert!(model_tier("sonnet") < model_tier("opus"));
}

// NOTE: the `|| lower == "opus"` clause (and its sonnet twin) is redundant —
// any string equal to "opus" also contains it, so the equality can never be
// the deciding test. Left alone here because this change is test-only, but it
// is dead weight, and a reader could reasonably think the function matches
// exact names as well as substrings.

// ─── provider_for_model ──────────────────────────────────────────────────────

/// Kills the `String::new()` and `"xyzzy".into()` body replacements.
///
/// Note what this pins: the function ignores its argument and answers "claude"
/// for everything, which is why the parameter is named `_model`. That is
/// correct only because every model in the upgrade chain is a Claude model —
/// if a non-Claude model ever enters the chain, this silently mislabels it.
#[test]
fn every_upgrade_target_is_attributed_to_claude() {
    assert_eq!(provider_for_model("claude-sonnet-4-6"), "claude");
    assert_eq!(provider_for_model("opus"), "claude");
    assert_eq!(provider_for_model(""), "claude");
}

// NOTE — equivalent mutant, deliberately not chased: in `upgrade_model`,
// `config.max_model == "sonnet" || config.max_model == "haiku"` -> `&&`.
// The early `return None` it guards is redundant with the tier check below it:
// with the cap at "sonnet" or "haiku" the only upgrade target from sonnet is
// opus (tier 3), and `next_tier > max_tier` already rejects that. Both the
// real code and the mutant return None for every input, so nothing can
// distinguish them.

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

use crate::config::ModelIntelligenceConfig;

fn runner_ok(model: &str) -> RunnerOutput {
    RunnerOutput {
        content: "Here is the implementation you requested.".to_string(),
        tool_call_error: false,
        output_truncated: false,
        model_id: model.to_string(),
        input_tokens: 100,
        output_tokens: 200,
    }
}

fn runner_empty() -> RunnerOutput {
    RunnerOutput {
        content: String::new(),
        tool_call_error: false,
        output_truncated: false,
        model_id: "claude-haiku-4-5".to_string(),
        input_tokens: 0,
        output_tokens: 0,
    }
}

fn runner_refusal() -> RunnerOutput {
    RunnerOutput {
        content: "I'm unable to complete this task as an AI.".to_string(),
        tool_call_error: false,
        output_truncated: false,
        model_id: "claude-haiku-4-5".to_string(),
        input_tokens: 50,
        output_tokens: 20,
    }
}

fn haiku_selection() -> ModelSelection {
    ModelSelection {
        model_id: "claude-haiku-4-5".to_string(),
        provider: "claude".to_string(),
        reason: "auto_select:Simple".to_string(),
    }
}

fn sonnet_selection() -> ModelSelection {
    ModelSelection {
        model_id: "claude-sonnet-4-6".to_string(),
        provider: "claude".to_string(),
        reason: "auto_select:Moderate".to_string(),
    }
}

#[test]
fn ok_response_no_upgrade() {
    let q = evaluate_response(&runner_ok("claude-haiku-4-5"));
    assert_eq!(q, ResponseQuality::Ok);
}

#[test]
fn empty_response_is_poor() {
    let q = evaluate_response(&runner_empty());
    assert_eq!(q, ResponseQuality::Poor(PoorReason::EmptyResponse));
}

#[test]
fn refusal_is_poor() {
    let q = evaluate_response(&runner_refusal());
    assert_eq!(q, ResponseQuality::Poor(PoorReason::ModelRefusal));
}

#[test]
fn tool_call_error_is_poor() {
    let mut out = runner_ok("claude-haiku-4-5");
    out.tool_call_error = true;
    let q = evaluate_response(&out);
    assert_eq!(q, ResponseQuality::Poor(PoorReason::ToolCallError));
}

#[test]
fn haiku_upgrades_to_sonnet() {
    let cfg = ModelIntelligenceConfig::default();
    let sel = upgrade_model(&haiku_selection(), &cfg, 0);
    assert!(sel.is_some());
    let sel = sel.unwrap();
    assert!(sel.model_id.contains("sonnet"), "got: {}", sel.model_id);
}

#[test]
fn max_one_upgrade_per_message() {
    let cfg = ModelIntelligenceConfig::default();
    let sel = upgrade_model(&haiku_selection(), &cfg, 1);
    assert!(sel.is_none(), "upgrade_count=1 should prevent upgrade");
}

#[test]
fn sonnet_upgrades_to_opus_when_allowed() {
    let cfg = ModelIntelligenceConfig {
        max_model: "opus".to_string(),
        ..Default::default()
    };
    let sel = upgrade_model(&sonnet_selection(), &cfg, 0);
    assert!(sel.is_some());
    assert!(sel.unwrap().model_id.contains("opus"));
}

#[test]
fn sonnet_cannot_upgrade_when_capped_at_sonnet() {
    let cfg = ModelIntelligenceConfig {
        max_model: "sonnet".to_string(),
        ..Default::default()
    };
    let sel = upgrade_model(&sonnet_selection(), &cfg, 0);
    assert!(
        sel.is_none(),
        "sonnet capped at sonnet should block upgrade"
    );
}
