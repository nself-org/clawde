/// Auto-upgrade on failure — evaluates response quality and upgrades model if needed.
///
/// This is the post-response hook in the pre-send pipeline (doc 61).
/// Maximum one auto-upgrade per message to prevent runaway cost.
use super::model_router::ModelSelection;
use super::RunnerOutput;
use crate::config::ModelIntelligenceConfig;

// ─── Quality evaluation ───────────────────────────────────────────────────────

/// Refusal signal phrases that indicate the model declined or couldn't complete the task.
const REFUSAL_PHRASES: &[&str] = &[
    "i cannot",
    "i'm unable to",
    "i am unable to",
    "as an ai",
    "i don't have the ability",
    "i can't do that",
    "i'm not able to",
];

/// Reason the response was considered poor quality.
#[derive(Debug, Clone, PartialEq)]
pub enum PoorReason {
    /// The provider returned a tool call error (schema invalid, tool not found, etc.).
    ToolCallError,
    /// The output appears truncated (empty or suspiciously short for the task).
    OutputTruncated,
    /// The model explicitly refused the task.
    ModelRefusal,
    /// No content returned at all.
    EmptyResponse,
}

/// Quality assessment of a completed provider turn.
#[derive(Debug, Clone, PartialEq)]
pub enum ResponseQuality {
    /// Response looks good — no upgrade needed.
    Ok,
    /// Response is poor quality for the given reason.
    Poor(PoorReason),
}

/// Evaluate the quality of a runner output.
///
/// This function is **pure** — no side effects, no async, no panics.
pub fn evaluate_response(output: &RunnerOutput) -> ResponseQuality {
    if output.content.is_empty() {
        return ResponseQuality::Poor(PoorReason::EmptyResponse);
    }
    if output.tool_call_error {
        return ResponseQuality::Poor(PoorReason::ToolCallError);
    }
    if output.output_truncated {
        return ResponseQuality::Poor(PoorReason::OutputTruncated);
    }
    let lower = output.content.to_lowercase();
    for phrase in REFUSAL_PHRASES {
        if lower.contains(phrase) {
            return ResponseQuality::Poor(PoorReason::ModelRefusal);
        }
    }
    ResponseQuality::Ok
}

// ─── Upgrade logic ────────────────────────────────────────────────────────────

/// Attempt to upgrade to the next model tier.
///
/// Returns `None` if already at the maximum configured model or upgrade count exceeded.
/// The `upgrade_count` parameter tracks how many upgrades have been attempted this turn;
/// callers must enforce max 1 upgrade per message.
pub fn upgrade_model(
    current: &ModelSelection,
    config: &ModelIntelligenceConfig,
    upgrade_count: u8,
) -> Option<ModelSelection> {
    // Max 1 auto-upgrade per message.
    if upgrade_count >= 1 {
        return None;
    }

    let current_lower = current.model_id.to_lowercase();

    // Upgrade chain: haiku → sonnet → opus
    let (next_model, reason) = if current_lower.contains("haiku") {
        (
            config.provider_models.sonnet.clone(),
            "auto_upgrade:haiku→sonnet".to_string(),
        )
    } else if current_lower.contains("sonnet") {
        // Only upgrade to opus if config allows it
        if config.max_model == "sonnet" || config.max_model == "haiku" {
            return None; // cap prevents upgrade
        }
        (
            config.provider_models.opus.clone(),
            "auto_upgrade:sonnet→opus".to_string(),
        )
    } else {
        // Already at opus or unknown — cannot upgrade further
        return None;
    };

    // Check the next model doesn't exceed max_model cap
    let next_tier = model_tier(&next_model);
    let max_tier = model_tier(&config.max_model);
    if next_tier > max_tier {
        return None;
    }

    Some(ModelSelection {
        provider: provider_for_model(&next_model),
        model_id: next_model,
        reason,
    })
}

fn model_tier(model: &str) -> u8 {
    let lower = model.to_lowercase();
    if lower.contains("opus") || lower == "opus" {
        3
    } else if lower.contains("sonnet") || lower == "sonnet" {
        2
    } else {
        1
    }
}

fn provider_for_model(_model: &str) -> String {
    "claude".to_string()
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// Tests live in tests.rs.
#[cfg(test)]
mod tests;
