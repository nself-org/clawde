//! Sprint EE CI.2 — `.claw/ci.yaml` config format.
//!
//! Defines the YAML schema for ClawDE CI configuration.

use serde::{Deserialize, Serialize};

/// The CI configuration loaded from `.claw/ci.yaml`.
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct CiConfig {
    /// Trigger conditions. Defaults to `["push"]`.
    #[serde(default = "default_triggers")]
    pub on: Vec<CiTrigger>,

    /// The AI task to execute. Plain English instruction.
    pub task: String,

    /// AI provider to use. Defaults to `"claude"`.
    #[serde(default = "default_provider")]
    pub provider: String,

    /// If true, exit 1 if the AI flags scope creep.
    #[serde(default)]
    pub fail_on_scope_creep: bool,

    /// If true, post a PR review comment with findings (requires GITHUB_TOKEN).
    #[serde(default)]
    pub post_comment: bool,

    /// Steps to run sequentially (alternative to `task`).
    #[serde(default)]
    pub steps: Vec<CiStep>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum CiTrigger {
    Push,
    PullRequest,
    TaskDone,
    Manual,
}

/// A single CI step.
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct CiStep {
    /// Display name for this step.
    pub name: String,

    /// The AI task/prompt for this step.
    pub task: Option<String>,

    /// Shell command to run (alternative to AI task).
    pub command: Option<String>,

    /// Timeout in seconds. Defaults to 300.
    #[serde(default = "default_timeout")]
    pub timeout_s: u64,

    /// If true, subsequent steps still run if this step fails.
    #[serde(default)]
    pub continue_on_error: bool,
}

/// Load CI config from `.claw/ci.yaml` relative to the given repo path.
pub fn load(repo_path: &std::path::Path) -> anyhow::Result<CiConfig> {
    let config_path = repo_path.join(".claw").join("ci.yaml");
    if !config_path.exists() {
        anyhow::bail!(
            "No CI config found at {}. Create `.claw/ci.yaml` to use ClawDE CI.",
            config_path.display()
        );
    }
    let content = std::fs::read_to_string(&config_path)?;
    let config: CiConfig =
        serde_norway::from_str(&content).map_err(|e| anyhow::anyhow!("Invalid CI config: {e}"))?;
    Ok(config)
}

fn default_triggers() -> Vec<CiTrigger> {
    vec![CiTrigger::Push]
}

fn default_provider() -> String {
    "claude".to_string()
}

fn default_timeout() -> u64 {
    300
}
