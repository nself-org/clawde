//! Planner agent — produces phases, tasks, and acceptance criteria (Phase 43e).
//!
//! The Planner operates in READ-ONLY mode: it explores the repository and
//! produces a structured YAML plan but never modifies files.

use serde::{Deserialize, Serialize};

/// A single planned subtask produced by the Planner agent.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlannedTask {
    pub title: String,
    pub acceptance_criteria: Vec<String>,
    pub test_plan: Vec<String>,
    /// One of: "low" | "medium" | "high" | "critical"
    pub risk_level: String,
    /// Description of what could go wrong if this task is implemented incorrectly.
    pub blast_radius: String,
    /// Priority score 1–10 (10 = highest priority).
    pub priority: u8,
}

/// A named phase containing one or more planned tasks.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlannerPhase {
    pub name: String,
    pub tasks: Vec<PlannedTask>,
}

/// Top-level output from the Planner agent.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlannerOutput {
    pub phases: Vec<PlannerPhase>,
}

/// Parse the Planner agent's YAML output.
pub fn parse_planner_output(yaml: &str) -> Result<PlannerOutput, serde_norway::Error> {
    serde_norway::from_str(yaml)
}

/// System prompt content for the Planner agent role.
pub fn planner_prompt_content() -> &'static str {
    "You are the Planner agent for ClawDE. You operate in READ-ONLY mode — \
you cannot modify files. Explore the repository, understand the codebase, and \
produce a structured YAML plan with phases, tasks, acceptance criteria, test \
plans, risk levels, and blast radii. Output ONLY valid YAML."
}
