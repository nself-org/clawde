//! Sprint CC TC.2 — AI confidence score parsing at session complete.
//!
//! When a session turn completes, the last assistant message is scanned for
//! a confidence score in the range 0.0–1.0. The score and reasoning are
//! persisted to `agent_tasks.confidence_score` / `confidence_reasoning` for
//! the task linked to the session.

use regex::Regex;
use std::sync::OnceLock;

/// Parsed confidence result from the last AI message.
#[derive(Debug, Clone)]
pub struct ConfidenceResult {
    pub score: f64,
    pub reasoning: String,
}

/// Extract a confidence score from the last assistant message content.
///
/// Looks for patterns like:
/// - `Confidence: 0.85`
/// - `confidence score: 0.9`
/// - `0.85 — I'm fairly confident because…`
/// - `My confidence is 0.7`
///
/// Returns `None` when no confidence marker is detected.
pub fn parse_confidence(content: &str) -> Option<ConfidenceResult> {
    static RE: OnceLock<Regex> = OnceLock::new();
    let re = RE.get_or_init(|| {
        // Match "confidence[:][ ]0.N" or bare "0.N" near the word "confidence"
        Regex::new(r"(?i)(?:confidence[^0-9.]*|my confidence is\s*)([0-9](?:\.[0-9]+)?)")
            .expect("confidence regex is valid")
    });

    let caps = re.captures(content)?;
    let score_str = caps.get(1)?.as_str();
    let score: f64 = score_str.parse().ok()?;

    // Clamp to [0.0, 1.0]
    let score = score.clamp(0.0, 1.0);

    // Extract the reasoning: take the sentence containing the score match.
    let reasoning = extract_reasoning_sentence(content, caps.get(0)?.start());

    Some(ConfidenceResult { score, reasoning })
}

/// Extract the sentence (or up to 200 chars) surrounding the confidence match
/// to use as the reasoning text.
fn extract_reasoning_sentence(content: &str, match_start: usize) -> String {
    // Walk back to sentence start — stop at newline or period followed by a space.
    let before = &content[..match_start];
    let sentence_start = find_sentence_start(before);

    // Walk forward to sentence end — stop at newline or period followed by
    // space/end (avoid breaking on decimal points like "0.9").
    let after_match = &content[match_start..];
    let sentence_end = find_sentence_end(after_match, match_start).unwrap_or(content.len());

    let sentence = &content[sentence_start..sentence_end.min(content.len())];
    let trimmed = sentence.trim().to_string();

    // Cap at 200 chars.
    if trimmed.len() > 200 {
        format!("{}…", &trimmed[..200])
    } else {
        trimmed
    }
}

fn find_sentence_start(before: &str) -> usize {
    // Scan backwards for '\n' or '. ' (period followed by space — end of previous sentence).
    let bytes = before.as_bytes();
    let mut i = bytes.len();
    while i > 0 {
        i -= 1;
        if bytes[i] == b'\n' {
            return i + 1;
        }
        if bytes[i] == b'.' && i + 1 < bytes.len() && bytes[i + 1] == b' ' {
            return i + 1;
        }
    }
    0
}

fn find_sentence_end(after: &str, offset: usize) -> Option<usize> {
    // Find '\n' or a period followed by a space or end-of-string.
    let bytes = after.as_bytes();
    for i in 0..bytes.len() {
        if bytes[i] == b'\n' {
            return Some(offset + i + 1);
        }
        if bytes[i] == b'.' {
            // Period at end of string or followed by whitespace = sentence end.
            if i + 1 >= bytes.len() || bytes[i + 1] == b' ' || bytes[i + 1] == b'\n' {
                return Some(offset + i + 1);
            }
        }
    }
    None
}

// Tests live in completion/tests.rs. Moved out when the suite grew to target
// the mutation gate's surviving-mutant list for the sentence helpers.
#[cfg(test)]
mod tests;
