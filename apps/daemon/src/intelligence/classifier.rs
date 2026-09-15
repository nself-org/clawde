/// Task complexity classifier — pure heuristic, < 1ms, no LLM calls.
///
/// Analyzes the incoming message text and session context to assign a `TaskComplexity`
/// level with a confidence score. This is Stage 0 of the pre-send pipeline.
///
/// Signal scoring: each fired signal adds to a score.
///   Score  0-2 → Simple
///   Score  3-5 → Moderate
///   Score  6-9 → Complex
///   Score 10+  → DeepReasoning
///
/// `prior_failure = true` always overrides to DeepReasoning regardless of score.
use serde::Serialize;
use std::sync::OnceLock;

// ─── Regex constants ──────────────────────────────────────────────────────────

// Simple signals (score -= 2 each if present)
static RE_SIMPLE_KW: OnceLock<regex::Regex> = OnceLock::new();
// Moderate signals
static RE_MODERATE_KW: OnceLock<regex::Regex> = OnceLock::new();
// Complex signals
static RE_COMPLEX_KW: OnceLock<regex::Regex> = OnceLock::new();
// DeepReasoning signals
static RE_DEEP_KW: OnceLock<regex::Regex> = OnceLock::new();
// File path references (`.rs`, `.dart`, `.ts`, paths with `/`)
static RE_FILE_REF: OnceLock<regex::Regex> = OnceLock::new();
// Code block delimiters (```)
static RE_CODE_BLOCK: OnceLock<regex::Regex> = OnceLock::new();

fn re_simple_kw() -> &'static regex::Regex {
    RE_SIMPLE_KW.get_or_init(|| {
        regex::Regex::new(
            r"(?i)\b(rename|typo|fix typo|format|lint|what is|explain this line|what does|quick fix)\b",
        )
        .expect("simple keyword regex")
    })
}

fn re_moderate_kw() -> &'static regex::Regex {
    RE_MODERATE_KW.get_or_init(|| {
        regex::Regex::new(
            r"(?i)\b(refactor|pr review|debug|write a function|add a test|unit test|implement|function that|method that|class that)\b",
        )
        .expect("moderate keyword regex")
    })
}

fn re_complex_kw() -> &'static regex::Regex {
    RE_COMPLEX_KW.get_or_init(|| {
        regex::Regex::new(
            r"(?i)\b(audit|architect|design system|security|authentication|authorization|multi.?file|across (the )?(codebase|repo|files)|end.to.end)\b",
        )
        .expect("complex keyword regex")
    })
}

fn re_deep_kw() -> &'static regex::Regex {
    RE_DEEP_KW.get_or_init(|| {
        regex::Regex::new(
            r"(?i)\b(novel|from scratch|completely redesign|hard bug|very hard|impossible|solve this|deep dive|comprehensive audit|architect from scratch)\b",
        )
        .expect("deep keyword regex")
    })
}

fn re_file_ref() -> &'static regex::Regex {
    RE_FILE_REF.get_or_init(|| {
        regex::Regex::new(
            r"\b\w+\.(rs|dart|ts|tsx|js|jsx|py|go|swift|kt|java|cpp|c|h|md)\b|(?:\w+/)+\w+",
        )
        .expect("file ref regex")
    })
}

fn re_code_block() -> &'static regex::Regex {
    RE_CODE_BLOCK.get_or_init(|| regex::Regex::new(r"```").expect("code block regex"))
}

// ─── Public types ─────────────────────────────────────────────────────────────

/// Complexity level of the user's task. Drives model selection.
#[derive(Debug, Clone, PartialEq, Serialize)]
pub enum TaskComplexity {
    /// Short, simple request. Maps to Haiku-class models.
    Simple,
    /// Typical coding task. Maps to Sonnet-class models.
    Moderate,
    /// Multi-file, architectural, or security work. Maps to Sonnet or Opus.
    Complex,
    /// Novel design, hard bugs, or prior failure override. Maps to Opus.
    DeepReasoning,
}

/// Full classification result with signals and confidence score.
#[derive(Debug, Clone, Serialize)]
pub struct TaskClassification {
    pub complexity: TaskComplexity,
    /// Confidence in the classification: 0.0 = very uncertain, 1.0 = all signals agree.
    pub confidence: f32,
    /// Human-readable list of signals that fired (for debug logging).
    pub signals: Vec<String>,
    /// Set when the prior model attempt on this task failed — forces DeepReasoning.
    pub prior_failure: bool,
}

/// Minimal session context passed into the classifier.
pub struct SessionContext {
    /// Number of messages in the session history.
    pub message_count: usize,
    /// The model used for the previous AI turn, if any.
    pub prior_model: Option<String>,
    /// True if the previous model attempt on this exact task returned a poor-quality result.
    pub prior_failure: bool,
}

// ─── Classification logic ─────────────────────────────────────────────────────

/// Classify a user message and return the recommended complexity level.
///
/// This function is **pure** — no side effects, no async, no panics.
/// Safe on empty strings, unicode-only content, and messages > 100KB.
pub fn classify_task(message: &str, ctx: &SessionContext) -> TaskClassification {
    // Truncate to 100KB to prevent regex catastrophic backtracking on pathological input.
    let msg = if message.len() > 100_000 {
        &message[..100_000]
    } else {
        message
    };

    let mut score: i32 = 0;
    let mut signals: Vec<String> = Vec::new();

    // ── Word count signals ────────────────────────────────────────────────────
    let word_count = msg.split_whitespace().count();
    if word_count < 20 {
        score -= 2;
        signals.push(format!("word_count<20 ({})", word_count));
    } else if word_count > 200 {
        score += 4;
        signals.push(format!("word_count>200 ({})", word_count));
    } else if word_count > 50 {
        score += 2;
        signals.push(format!("word_count>50 ({})", word_count));
    }

    // ── Code block signals ────────────────────────────────────────────────────
    let code_block_count = re_code_block().find_iter(msg).count() / 2; // pairs
    if code_block_count >= 3 {
        score += 3;
        signals.push(format!("code_blocks>={}", code_block_count));
    } else if code_block_count >= 1 {
        score += 1;
        signals.push(format!("code_blocks={}", code_block_count));
    }

    // ── File reference signals ────────────────────────────────────────────────
    let file_ref_count = re_file_ref().find_iter(msg).count();
    if file_ref_count >= 3 {
        score += 3;
        signals.push(format!("file_refs>={}", file_ref_count));
    } else if file_ref_count >= 1 {
        score += 1;
        signals.push(format!("file_refs={}", file_ref_count));
    }

    // ── Keyword signals ───────────────────────────────────────────────────────
    if re_simple_kw().is_match(msg) {
        score -= 2;
        signals.push("simple_keyword".to_string());
    }
    if re_moderate_kw().is_match(msg) {
        score += 2;
        signals.push("moderate_keyword".to_string());
    }
    // Count every complex keyword occurrence — multiple signals add up (e.g. "security audit" = ×2).
    let complex_kw_count = re_complex_kw().find_iter(msg).count() as i32;
    if complex_kw_count > 0 {
        score += complex_kw_count * 4;
        signals.push(format!("complex_keyword×{}", complex_kw_count));
    }
    if re_deep_kw().is_match(msg) {
        // Deep keywords are explicit high-intent signals — guarantee DeepReasoning even in short msgs.
        score = score.max(10);
        signals.push("deep_reasoning_keyword".to_string());
    }

    // ── Session history depth ─────────────────────────────────────────────────
    if ctx.message_count > 20 {
        score += 2;
        signals.push(format!("history_depth>{}", ctx.message_count));
    }

    // ── Prior failure override ────────────────────────────────────────────────
    if ctx.prior_failure {
        signals.push("prior_failure_override".to_string());
        return TaskClassification {
            complexity: TaskComplexity::DeepReasoning,
            confidence: 1.0,
            signals,
            prior_failure: true,
        };
    }

    // ── Map score to complexity ───────────────────────────────────────────────
    let total_signals = signals.len().max(1) as f32;
    let complexity = match score {
        i32::MIN..=2 => TaskComplexity::Simple,
        3..=5 => TaskComplexity::Moderate,
        6..=9 => TaskComplexity::Complex,
        _ => TaskComplexity::DeepReasoning,
    };

    // Confidence: higher when signals strongly agree (all point same direction).
    // Simple score: |score| relative to max possible from fired signals.
    let max_possible = total_signals * 4.0; // generous upper bound
    let confidence = (score.unsigned_abs() as f32 / max_possible).clamp(0.1, 1.0);

    // Low confidence falls back to Moderate to avoid Haiku on ambiguous tasks.
    let complexity = if confidence < 0.3 && complexity == TaskComplexity::Simple {
        signals.push("low_confidence_fallback_to_moderate".to_string());
        TaskComplexity::Moderate
    } else {
        complexity
    };

    TaskClassification {
        complexity,
        confidence,
        signals,
        prior_failure: false,
    }
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// Tests live in classifier/tests.rs. Moved out when the suite grew to target
// the mutation gate's 32 surviving mutants, all of which are in classify_task.
#[cfg(test)]
mod tests;
