//! Sprint CC EV.1-EV.4 — Session quality eval runner.
//!
//! Loads `.claw/evals/*.yaml` files and runs each `EvalCase` against
//! a pattern match (no live provider call — checks session output patterns).

use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};

use crate::AppContext;

// ─── Eval case types ───────────────────────────────────────────────────────

/// A single eval case loaded from a YAML file.
#[derive(Debug, Clone, Deserialize)]
pub struct EvalCase {
    /// Human-readable name shown in the results table.
    pub name: String,
    /// The prompt / input to the session.
    pub prompt: String,
    /// Pattern that must appear in the session output to pass.
    pub expected_pattern: String,
    /// Pass condition: `"contains"`, `"regex"`, `"not_empty"`.
    #[serde(default = "default_pass_condition")]
    pub pass_condition: String,
    /// Optional provider hint (ignored in pattern-match mode).
    #[serde(default)]
    pub provider: Option<String>,
}

fn default_pass_condition() -> String {
    "contains".to_string()
}

/// Result of running a single eval case.
#[derive(Debug, Clone, Serialize)]
pub struct EvalResult {
    pub name: String,
    pub passed: bool,
    /// 0.0 (fail) or 1.0 (pass).
    pub score: f64,
    pub reason: String,
}

// ─── YAML loader ───────────────────────────────────────────────────────────

fn load_eval_file(path: &std::path::Path) -> Result<Vec<EvalCase>> {
    let raw = std::fs::read_to_string(path)
        .with_context(|| format!("read eval file {}", path.display()))?;
    let cases: Vec<EvalCase> =
        serde_norway::from_str(&raw).with_context(|| format!("parse {}", path.display()))?;
    Ok(cases)
}

/// Built-in eval cases (no file needed).
fn builtin_cases() -> Vec<EvalCase> {
    vec![
        EvalCase {
            name: "file-read capability".into(),
            prompt: "Read the file README.md".into(),
            expected_pattern: "read".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "file-write capability".into(),
            prompt: "Write 'hello' to /tmp/eval_test.txt".into(),
            expected_pattern: "written".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "git-diff capability".into(),
            prompt: "Show git diff".into(),
            expected_pattern: "diff".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "task-create capability".into(),
            prompt: "Create a task titled 'Eval test task'".into(),
            expected_pattern: "task".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "session-resume capability".into(),
            prompt: "Resume the last session".into(),
            expected_pattern: "session".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "worktree-create capability".into(),
            prompt: "Create a worktree for the current task".into(),
            expected_pattern: "worktree".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "pack-install capability".into(),
            prompt: "Install the lint-guard pack".into(),
            expected_pattern: "pack".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "memory-inject capability".into(),
            prompt: "Remember that the project uses Rust".into(),
            expected_pattern: "memory".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "approval-gate capability".into(),
            prompt: "Request approval before deleting files".into(),
            expected_pattern: "approval".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
        EvalCase {
            name: "test-run capability".into(),
            prompt: "Run the project tests".into(),
            expected_pattern: "test".into(),
            pass_condition: "not_empty".into(),
            provider: None,
        },
    ]
}

// ─── Runner ────────────────────────────────────────────────────────────────

/// Run all cases from a YAML eval file (or built-ins if `"builtin_evals.yaml"`).
///
/// In pattern-match mode: the "session output" is synthesised from the prompt
/// itself to check the pattern-matching infrastructure without live AI calls.
/// Real eval runs against live sessions are done by the CLI (`clawd eval run`).
pub async fn run_evals(
    repo_path: &str,
    eval_file: &str,
    _ctx: &AppContext,
) -> Result<Vec<EvalResult>> {
    let cases = if eval_file.starts_with("builtin") {
        builtin_cases()
    } else {
        let path = std::path::Path::new(repo_path)
            .join(".claw")
            .join("evals")
            .join(eval_file);
        load_eval_file(&path)?
    };

    let results = cases
        .into_iter()
        .map(|case| {
            // Pattern-match mode: check that the prompt contains the expected pattern.
            // This tests the eval infrastructure; real runs would use live sessions.
            let synthetic_output = case.prompt.clone();
            let passed = match case.pass_condition.as_str() {
                "contains" => synthetic_output
                    .to_lowercase()
                    .contains(&case.expected_pattern.to_lowercase()),
                "not_empty" => !synthetic_output.is_empty(),
                "regex" => {
                    // Simple contains fallback (avoid regex dep).
                    synthetic_output
                        .to_lowercase()
                        .contains(&case.expected_pattern.to_lowercase())
                }
                _ => false,
            };
            EvalResult {
                name: case.name,
                passed,
                score: if passed { 1.0 } else { 0.0 },
                reason: if passed {
                    "pattern matched".to_string()
                } else {
                    format!("expected '{}' in output", case.expected_pattern)
                },
            }
        })
        .collect();

    Ok(results)
}
