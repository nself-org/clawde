//! Sprint DD WR.2/WR.3 — Workflow recipe engine.
//!
//! Executes multi-step AI workflow recipes. Each step creates a session and
//! runs a prompt, optionally inheriting context from the previous step.
//!
//! ## Recipe YAML format
//!
//! ```yaml
//! name: code-review
//! description: Review a diff and create follow-up tasks
//! steps:
//!   - prompt: "Review the changes in this diff: {diff}"
//!     provider: claude
//!   - prompt: "Create follow-up tasks for the issues found"
//!     inherit_from: previous
//! triggers:
//!   - on_commit
//! ```

use anyhow::Result;
use serde::{Deserialize, Serialize};

/// A workflow recipe definition.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct WorkflowRecipe {
    pub id: String,
    pub name: String,
    pub description: String,
    pub steps: Vec<WorkflowStep>,
    #[serde(default)]
    pub tags: Vec<String>,
    #[serde(default)]
    pub triggers: Vec<WorkflowTrigger>,
    #[serde(default)]
    pub is_builtin: bool,
    pub run_count: i64,
}

/// A single step in a workflow recipe.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct WorkflowStep {
    pub prompt: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub provider: Option<String>,
    /// If `"previous"`, this step inherits context from the prior step's session.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub inherit_from: Option<String>,
}

/// Events that automatically trigger a workflow.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum WorkflowTrigger {
    OnCommit,
    OnTaskDone,
    OnFileChange,
    OnSessionComplete,
}

/// A running workflow instance.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WorkflowRun {
    pub id: String,
    pub recipe_id: String,
    pub status: String,
    pub current_step: i64,
    pub total_steps: i64,
    pub started_at: String,
    pub finished_at: Option<String>,
}

/// Parse a workflow recipe from YAML.
pub fn parse_recipe_yaml(yaml: &str) -> Result<WorkflowRecipeYaml> {
    let recipe: WorkflowRecipeYaml = serde_norway::from_str(yaml)?;
    Ok(recipe)
}

/// Raw YAML deserialization form (before DB storage).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkflowRecipeYaml {
    pub name: String,
    #[serde(default)]
    pub description: String,
    pub steps: Vec<WorkflowStepYaml>,
    #[serde(default)]
    pub tags: Vec<String>,
    #[serde(default)]
    pub triggers: Vec<WorkflowTrigger>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkflowStepYaml {
    pub prompt: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub provider: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub inherit_from: Option<String>,
}

/// 5 built-in workflow recipes shipped with clawd.
pub fn builtin_recipes() -> Vec<WorkflowRecipeYaml> {
    vec![
        serde_norway::from_str(CODE_REVIEW_YAML).unwrap(),
        serde_norway::from_str(RELEASE_PREP_YAML).unwrap(),
        serde_norway::from_str(ONBOARD_CODEBASE_YAML).unwrap(),
        serde_norway::from_str(DEBUG_SESSION_YAML).unwrap(),
        serde_norway::from_str(SPEC_TO_IMPL_YAML).unwrap(),
    ]
}

const CODE_REVIEW_YAML: &str = r#"
name: code-review
description: Review a diff and create follow-up tasks for issues found
tags: [review, quality]
triggers: [on_commit]
steps:
  - prompt: "Review the recent git diff. List all issues, bugs, and improvements as a numbered list."
    provider: codex
  - prompt: "For each issue you found, create a task with `task.create`. Use the issue description as the title."
    inherit_from: previous
"#;

const RELEASE_PREP_YAML: &str = r#"
name: release-prep
description: Prepare a release — changelog, version bump, and release notes
tags: [release]
steps:
  - prompt: "Summarize all changes since the last git tag as a CHANGELOG entry. Format as markdown with ## Added, ## Fixed, ## Changed sections."
    provider: claude
  - prompt: "Based on the changes summarized, recommend a semantic version bump (patch/minor/major) and explain why."
    inherit_from: previous
"#;

const ONBOARD_CODEBASE_YAML: &str = r#"
name: onboard-codebase
description: Generate a codebase orientation guide for new contributors
tags: [docs, onboarding]
steps:
  - prompt: "Read the project structure and key files. Describe the architecture, major modules, entry points, and how to run the project."
    provider: claude
  - prompt: "Write a CONTRIBUTING.md based on the architecture analysis. Include: setup steps, where to find things, coding conventions, how to run tests."
    inherit_from: previous
"#;

const DEBUG_SESSION_YAML: &str = r#"
name: debug-session
description: Reproduce a bug, bisect the commit that introduced it, then fix it
tags: [debug, fix]
steps:
  - prompt: "Reproduce the bug described in the current task. Write a failing test that demonstrates the problem."
    provider: claude
  - prompt: "Identify the commit that introduced this bug using git bisect or git log. Explain what changed."
    inherit_from: previous
  - prompt: "Fix the bug. Make the failing test pass. Run all tests to confirm no regressions."
    inherit_from: previous
"#;

const SPEC_TO_IMPL_YAML: &str = r#"
name: spec-to-impl
description: Convert a spec file into a task list and then implement each task
tags: [planning, implementation]
steps:
  - prompt: "Read the spec file in .claw/specs/ and break it down into a numbered list of implementation tasks."
    provider: claude
  - prompt: "Create all the tasks from the list using task.create. Use the spec section names as task titles."
    inherit_from: previous
  - prompt: "Implement the first task from the list. Commit when done."
    inherit_from: previous
"#;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_builtin_recipes_parse() {
        let recipes = builtin_recipes();
        assert_eq!(recipes.len(), 5);
        assert!(recipes.iter().any(|r| r.name == "code-review"));
        assert!(recipes.iter().any(|r| r.name == "release-prep"));
    }

    #[test]
    fn test_parse_custom_recipe() {
        let yaml = r#"
name: my-workflow
description: Custom workflow
steps:
  - prompt: "Do something"
    provider: claude
"#;
        let recipe = parse_recipe_yaml(yaml).unwrap();
        assert_eq!(recipe.name, "my-workflow");
        assert_eq!(recipe.steps.len(), 1);
    }

    #[test]
    fn test_trigger_deserialization() {
        let yaml = r#"
name: triggered-workflow
steps:
  - prompt: "Check the commit"
triggers:
  - on_commit
  - on_task_done
"#;
        let recipe = parse_recipe_yaml(yaml).unwrap();
        assert!(recipe.triggers.contains(&WorkflowTrigger::OnCommit));
        assert!(recipe.triggers.contains(&WorkflowTrigger::OnTaskDone));
    }
}
