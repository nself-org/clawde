//! Tests for task classification.
//!
//! The mutation gate reported **32 surviving mutants in this file, every one of
//! them inside `classify_task`** — despite 22 existing tests. The reason is
//! visible in those tests: they assert `r.complexity` and nothing else.
//! `complexity` is a four-way bucket, so flipping `score += 2` to `score -= 2`
//! usually lands in the same bucket and the test still passes.
//!
//! The lever these tests use instead is **`confidence`**, which is
//! `|score| / (signals.len() * 4)`. It is a precise function of the score, so
//! asserting it pins the arithmetic itself. Paired with an exact `signals`
//! vector — which carries the measured counts, e.g. `"word_count>50 (51)"` —
//! every branch and every `+=` becomes observable.
//!
//! Expected values were derived from an independent model of the algorithm and
//! the model was validated against the real implementation before these
//! assertions were written, rather than read off the code.
//!
//! The 22 pre-existing tests are kept verbatim at the end.

use super::*;

fn ctx(message_count: usize, prior_failure: bool) -> SessionContext {
    SessionContext {
        message_count,
        prior_model: None,
        prior_failure,
    }
}

/// `n` words of filler, so word-count boundaries can be hit exactly.
fn words(n: usize) -> String {
    vec!["alpha"; n].join(" ")
}

/// Assert the full observable result: bucket, confidence and signals.
///
/// Asserting all three together is the point — `complexity` alone is too
/// coarse to see an arithmetic change, and `confidence` alone cannot
/// distinguish which branch fired.
fn assert_classified(
    got: &TaskClassification,
    complexity: TaskComplexity,
    confidence: f32,
    signals: &[&str],
) {
    assert_eq!(
        got.complexity, complexity,
        "complexity; signals={:?}",
        got.signals
    );
    assert!(
        (got.confidence - confidence).abs() < 0.0005,
        "confidence: got {}, want {}; signals={:?}",
        got.confidence,
        confidence,
        got.signals
    );
    let want: Vec<String> = signals.iter().map(|s| (*s).to_string()).collect();
    assert_eq!(got.signals, want, "signals");
}

// ─── word-count boundaries ──────────────────────────────────────────────────

#[test]
fn exactly_twenty_words_is_not_short() {
    // `word_count < 20` — at exactly 20 the branch must NOT fire, so no word
    // signal is recorded and the result falls through to the low-confidence
    // fallback. Kills `<` -> `<=`.
    let r = classify_task(&words(20), &ctx(0, false));
    assert_classified(
        &r,
        TaskComplexity::Moderate,
        0.1,
        &["low_confidence_fallback_to_moderate"],
    );
}

#[test]
fn nineteen_words_is_short() {
    // The other side of the same boundary.
    let r = classify_task(&words(19), &ctx(0, false));
    assert_classified(&r, TaskComplexity::Simple, 0.5, &["word_count<20 (19)"]);
}

#[test]
fn thirty_words_fires_no_word_count_branch_at_all() {
    // Between the boundaries: not < 20, not > 50. Kills `> 50` -> `< 50`,
    // which would fire here.
    let r = classify_task(&words(30), &ctx(0, false));
    assert_classified(
        &r,
        TaskComplexity::Moderate,
        0.1,
        &["low_confidence_fallback_to_moderate"],
    );
}

#[test]
fn exactly_fifty_words_does_not_fire_the_over_fifty_branch() {
    // Kills `> 50` -> `>= 50` and `> 50` -> `== 50`.
    let r = classify_task(&words(50), &ctx(0, false));
    assert_classified(
        &r,
        TaskComplexity::Moderate,
        0.1,
        &["low_confidence_fallback_to_moderate"],
    );
}

#[test]
fn fifty_one_words_scores_exactly_two() {
    // confidence 2/(1*4) = 0.5 pins the score at 2.
    let r = classify_task(&words(51), &ctx(0, false));
    assert_classified(&r, TaskComplexity::Simple, 0.5, &["word_count>50 (51)"]);
}

#[test]
fn exactly_two_hundred_words_takes_the_over_fifty_branch_not_the_over_two_hundred_one() {
    // Kills `> 200` -> `>= 200`: that mutant would score 4 and label this
    // "word_count>200 (200)".
    let r = classify_task(&words(200), &ctx(0, false));
    assert_classified(&r, TaskComplexity::Simple, 0.5, &["word_count>50 (200)"]);
}

#[test]
fn two_hundred_and_one_words_scores_exactly_four() {
    // confidence 4/(1*4) = 1.0 pins the score at 4, killing `+= 4` -> `*=`
    // (which would leave the score at 0) and `-=`.
    let r = classify_task(&words(201), &ctx(0, false));
    assert_classified(&r, TaskComplexity::Moderate, 1.0, &["word_count>200 (201)"]);
}

// ─── code blocks ────────────────────────────────────────────────────────────

#[test]
fn one_fenced_pair_counts_as_one_code_block() {
    // Two ``` markers / 2 = 1. Kills `/ 2` -> `% 2` (which gives 0, no signal)
    // and `/ 2` -> `* 2` (which gives 4 and the >= 3 branch).
    let msg = format!("```\nx\n``` {}", words(25));
    let r = classify_task(&msg, &ctx(0, false));
    assert_classified(
        &r,
        TaskComplexity::Moderate,
        0.25,
        &["code_blocks=1", "low_confidence_fallback_to_moderate"],
    );
}

#[test]
fn three_fenced_pairs_take_the_bulk_branch_and_score_three() {
    // confidence 3/(1*4) = 0.75 pins the score at 3, killing `+= 3` -> `-=`/`*=`.
    let msg = format!("``` ``` ``` ``` ``` ``` {}", words(25));
    let r = classify_task(&msg, &ctx(0, false));
    assert_classified(&r, TaskComplexity::Moderate, 0.75, &["code_blocks>=3"]);
}

// ─── file references ────────────────────────────────────────────────────────

#[test]
fn a_single_file_reference_scores_one() {
    let msg = format!("see main.rs {}", words(25));
    let r = classify_task(&msg, &ctx(0, false));
    assert_classified(
        &r,
        TaskComplexity::Moderate,
        0.25,
        &["file_refs=1", "low_confidence_fallback_to_moderate"],
    );
}

#[test]
fn three_file_references_take_the_bulk_branch_and_score_three() {
    let msg = format!("see a.rs b.ts c.py {}", words(25));
    let r = classify_task(&msg, &ctx(0, false));
    assert_classified(&r, TaskComplexity::Moderate, 0.75, &["file_refs>=3"]);
}

// ─── complex keyword counting ───────────────────────────────────────────────

#[test]
fn no_complex_keyword_records_no_signal_at_all() {
    // Kills `complex_kw_count > 0` -> `>= 0`, which would push a
    // "complex_keyword×0" signal for every message on earth.
    let r = classify_task(&words(30), &ctx(0, false));
    assert!(
        !r.signals.iter().any(|s| s.starts_with("complex_keyword")),
        "no complex keyword present, got {:?}",
        r.signals
    );
}

#[test]
fn each_complex_keyword_occurrence_is_counted_and_multiplied_by_four() {
    // Two occurrences -> score 8, one signal -> confidence 8/4 clamped to 1.0.
    // Kills `count * 4` -> `count + 4` (which would score 6).
    let msg = format!("{} security audit", words(30));
    let r = classify_task(&msg, &ctx(0, false));
    assert_classified(&r, TaskComplexity::Complex, 1.0, &["complex_keyword×2"]);
}

#[test]
fn a_single_complex_keyword_adds_exactly_four() {
    // 2 (word_count>50) + 4 = 6 -> Complex, confidence 6/(2*4) = 0.75.
    // This pairing is what kills `+= 2` -> `-=` on the word-count branch: with
    // a minus the score would be 2, landing in Simple with confidence 0.25.
    // It also kills the `6..=9` match arm deletion, which would make this
    // DeepReasoning.
    let msg = format!("{} security", words(51));
    let r = classify_task(&msg, &ctx(0, false));
    assert_classified(
        &r,
        TaskComplexity::Complex,
        0.75,
        &["word_count>50 (52)", "complex_keyword×1"],
    );
}

// ─── session history depth ──────────────────────────────────────────────────

#[test]
fn exactly_twenty_prior_messages_is_not_deep_history() {
    // Kills `> 20` -> `>= 20` and `> 20` -> `== 20`.
    let r = classify_task(&words(30), &ctx(20, false));
    assert_classified(
        &r,
        TaskComplexity::Moderate,
        0.1,
        &["low_confidence_fallback_to_moderate"],
    );
}

#[test]
fn twenty_one_prior_messages_scores_exactly_two() {
    let r = classify_task(&words(30), &ctx(21, false));
    assert_classified(&r, TaskComplexity::Simple, 0.5, &["history_depth>21"]);
}

#[test]
fn history_depth_adds_to_a_complex_keyword_rather_than_subtracting() {
    // 4 + 2 = 6 -> Complex at 0.75. Kills the history `+= 2` -> `-=`, which
    // would give 2 and fall back to Moderate at 0.25.
    let msg = format!("{} security", words(30));
    let r = classify_task(&msg, &ctx(21, false));
    assert_classified(
        &r,
        TaskComplexity::Complex,
        0.75,
        &["complex_keyword×1", "history_depth>21"],
    );
}

// ─── score-to-bucket mapping ────────────────────────────────────────────────

#[test]
fn a_score_of_four_maps_to_moderate_not_deep_reasoning() {
    // Kills the `3..=5` match-arm deletion, which would drop a score of 4 into
    // the `_` arm and return DeepReasoning.
    let r = classify_task(&words(201), &ctx(0, false));
    assert_eq!(r.complexity, TaskComplexity::Moderate);
}

#[test]
fn a_deep_keyword_floors_the_score_at_ten_even_in_a_short_message() {
    // score.max(10) with one signal -> confidence 10/4 clamped to 1.0.
    let msg = format!("solve this {}", words(25));
    let r = classify_task(&msg, &ctx(0, false));
    assert_classified(
        &r,
        TaskComplexity::DeepReasoning,
        1.0,
        &["deep_reasoning_keyword"],
    );
}

// ─── prior-failure short circuit ────────────────────────────────────────────

#[test]
fn a_prior_failure_returns_deep_reasoning_immediately_with_full_confidence() {
    let r = classify_task("tiny message", &ctx(0, true));
    assert_eq!(r.complexity, TaskComplexity::DeepReasoning);
    assert!((r.confidence - 1.0).abs() < 0.0005);
    assert!(r.prior_failure, "the flag must be propagated");
    assert_eq!(
        r.signals,
        vec![
            "word_count<20 (2)".to_string(),
            "prior_failure_override".to_string()
        ]
    );
}

// ─── oversized input ────────────────────────────────────────────────────────

#[test]
fn content_past_one_hundred_kb_is_truncated_and_cannot_influence_the_score() {
    // Kills `len() > 100_000` -> `== 100_000`: that mutant stops truncating
    // anything larger, so the trailing keywords past the cut would be scored.
    let mut msg = "a ".repeat(60_000); // ~120KB, comfortably past the cap
    assert!(msg.len() > 100_000);
    msg.push_str(" security security security");

    let r = classify_task(&msg, &ctx(0, false));
    assert!(
        !r.signals.iter().any(|s| s.starts_with("complex_keyword")),
        "keywords past the 100KB cut must be invisible, got {:?}",
        r.signals
    );
}

// NOTE on two mutants at this boundary that are EQUIVALENT and cannot be
// killed by any test:
//
//   `len() > 100_000` -> `>= 100_000`: at exactly 100_000 bytes the mutant
//   takes `&message[..100_000]`, which is the whole string. Identical result.
//
//   `confidence < 0.3` -> `<= 0.3`: the fallback needs confidence exactly 0.3
//   AND complexity Simple. Confidence is |score| / (signals * 4) with
//   |score| <= 2 for Simple, so 0.3 would need signals = 1.67. Unreachable.
//
// They are recorded here so the file's ceiling is understood rather than
// chased.

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

#[test]
fn simple_short_message() {
    let r = classify_task("rename this variable", &ctx(0, false));
    assert_eq!(r.complexity, TaskComplexity::Simple);
}

#[test]
fn empty_message_does_not_panic() {
    let r = classify_task("", &ctx(0, false));
    // Empty message has no word count > 20 so should be Simple or Moderate
    assert!(matches!(
        r.complexity,
        TaskComplexity::Simple | TaskComplexity::Moderate
    ));
}

#[test]
fn unicode_only_does_not_panic() {
    let r = classify_task("مرحبا بالعالم 🦀", &ctx(0, false));
    let _ = r.complexity; // just verify no panic
}

#[test]
fn very_long_message_does_not_panic() {
    let long = "word ".repeat(30_000);
    let r = classify_task(&long, &ctx(0, false));
    // Very long = Complex or DeepReasoning
    assert!(matches!(
        r.complexity,
        TaskComplexity::Complex | TaskComplexity::DeepReasoning | TaskComplexity::Moderate
    ));
}

#[test]
fn prior_failure_forces_deep_reasoning() {
    let r = classify_task("what is 2+2", &ctx(0, true));
    assert_eq!(r.complexity, TaskComplexity::DeepReasoning);
    assert_eq!(r.confidence, 1.0);
    assert!(r.prior_failure);
}

#[test]
fn deep_keyword_routes_to_deep_reasoning() {
    let r = classify_task(
        "architect from scratch a completely new event sourcing system",
        &ctx(0, false),
    );
    assert_eq!(r.complexity, TaskComplexity::DeepReasoning);
}

#[test]
fn security_audit_is_complex() {
    let r = classify_task(
        "perform a security audit of the authentication system across all files",
        &ctx(0, false),
    );
    assert!(matches!(
        r.complexity,
        TaskComplexity::Complex | TaskComplexity::DeepReasoning
    ));
}

#[test]
fn moderate_keyword_is_moderate() {
    let r = classify_task(
        "write a function that parses JSON and returns a struct",
        &ctx(0, false),
    );
    assert!(matches!(
        r.complexity,
        TaskComplexity::Moderate | TaskComplexity::Complex
    ));
}

#[test]
fn high_message_depth_increases_complexity() {
    let r_shallow = classify_task("fix this bug", &ctx(2, false));
    let r_deep = classify_task("fix this bug", &ctx(25, false));
    // Deep history should increase complexity score
    let score_shallow = match r_shallow.complexity {
        TaskComplexity::Simple => 0,
        TaskComplexity::Moderate => 1,
        TaskComplexity::Complex => 2,
        TaskComplexity::DeepReasoning => 3,
    };
    let score_deep = match r_deep.complexity {
        TaskComplexity::Simple => 0,
        TaskComplexity::Moderate => 1,
        TaskComplexity::Complex => 2,
        TaskComplexity::DeepReasoning => 3,
    };
    assert!(score_deep >= score_shallow);
}

#[test]
fn confidence_is_in_range() {
    for msg in [
        "rename x",
        "implement a full auth system",
        "architect from scratch",
        "",
    ] {
        let r = classify_task(msg, &ctx(0, false));
        assert!(
            r.confidence >= 0.0 && r.confidence <= 1.0,
            "confidence out of range: {}",
            r.confidence
        );
    }
}

// ── Additional coverage for 20+ test functions (MI.T24) ──────────────────

#[test]
fn rename_variable_is_simple() {
    let r = classify_task("rename the variable `count` to `total`", &ctx(0, false));
    assert_eq!(r.complexity, TaskComplexity::Simple);
}

#[test]
fn fix_typo_is_simple() {
    let r = classify_task("fix typo in the README", &ctx(0, false));
    assert_eq!(r.complexity, TaskComplexity::Simple);
}

#[test]
fn what_is_question_is_simple_or_moderate() {
    let r = classify_task(
        "what is the difference between Vec and slice in Rust?",
        &ctx(0, false),
    );
    assert!(matches!(
        r.complexity,
        TaskComplexity::Simple | TaskComplexity::Moderate
    ));
}

#[test]
fn implement_function_is_moderate_or_complex() {
    let r = classify_task(
        "implement a function that parses an ISO 8601 date string and returns a chrono DateTime",
        &ctx(0, false),
    );
    assert!(matches!(
        r.complexity,
        TaskComplexity::Moderate | TaskComplexity::Complex
    ));
}

#[test]
fn refactor_is_at_least_moderate() {
    let r = classify_task(
        "refactor the session handler to use the new error type",
        &ctx(0, false),
    );
    let level = match r.complexity {
        TaskComplexity::Simple => 0,
        TaskComplexity::Moderate => 1,
        TaskComplexity::Complex => 2,
        TaskComplexity::DeepReasoning => 3,
    };
    assert!(
        level >= 1,
        "refactor should be at least Moderate, got {level}"
    );
}

#[test]
fn unit_test_keyword_is_at_least_moderate() {
    let r = classify_task("write a unit test for the cost estimator", &ctx(0, false));
    assert!(matches!(
        r.complexity,
        TaskComplexity::Moderate | TaskComplexity::Complex
    ));
}

#[test]
fn authentication_across_codebase_is_complex_or_deep() {
    let r = classify_task(
        "implement authentication and authorization across the entire codebase",
        &ctx(0, false),
    );
    assert!(matches!(
        r.complexity,
        TaskComplexity::Complex | TaskComplexity::DeepReasoning
    ));
}

#[test]
fn multi_file_keyword_with_context_is_at_least_moderate() {
    // "multi-file" fires the complex keyword (+4). A longer message avoids
    // the short-message penalty (-2 for <20 words).
    let r = classify_task(
        "update the multi-file session handling and context management to properly \
         support cancellation tokens and propagate errors across all file boundaries",
        &ctx(0, false),
    );
    assert!(matches!(
        r.complexity,
        TaskComplexity::Moderate | TaskComplexity::Complex | TaskComplexity::DeepReasoning
    ));
}

#[test]
fn whitespace_only_does_not_panic() {
    let r = classify_task("   \t\n  ", &ctx(0, false));
    let _ = r.complexity; // just verify no panic
}

#[test]
fn code_block_only_does_not_panic() {
    let r = classify_task(
        "```\nfn main() { println!(\"hello\"); }\n```",
        &ctx(0, false),
    );
    let _ = r.complexity; // verify no panic; code block signal should fire
}

#[test]
fn novel_design_is_deep_reasoning() {
    let r = classify_task(
        "design a novel event sourcing architecture from scratch for our audit log system",
        &ctx(0, false),
    );
    assert_eq!(r.complexity, TaskComplexity::DeepReasoning);
}

#[test]
fn signals_list_is_populated_for_complex_task() {
    let r = classify_task(
        "perform a comprehensive security audit of the authentication module across all files",
        &ctx(0, false),
    );
    assert!(
        !r.signals.is_empty(),
        "signals list should be populated for a complex task"
    );
}
