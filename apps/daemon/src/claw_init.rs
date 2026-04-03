use std::path::Path;
use tokio::fs;

/// Idempotently create the `.claw/` directory structure in the given project root.
///
/// Safe to call multiple times — existing files and directories are not
/// overwritten unless they are missing.
pub async fn init_claw_dir(project_root: &Path) -> anyhow::Result<()> {
    let claw = project_root.join(".claw");

    for dir in [
        "tasks",
        "policies",
        "templates",
        "evals/datasets",
        "telemetry",
        "worktrees",
    ] {
        fs::create_dir_all(claw.join(dir)).await?;
    }

    // Write README.md if not present.
    // The template is tracked at daemon/assets/claw-readme.md so it is always
    // available in CI and in clean checkouts (apps/.claw/ is gitignored).
    let readme = claw.join("README.md");
    if !readme.exists() {
        const README_CONTENT: &str = include_str!("../assets/claw-readme.md");
        fs::write(&readme, README_CONTENT).await?;
    }

    // Write .gitignore if not present.
    let gitignore = claw.join(".gitignore");
    if !gitignore.exists() {
        fs::write(
            &gitignore,
            "# Worktrees are local — never commit\nworktrees/\n\n\
             # Telemetry traces are local — never commit\ntelemetry/\n",
        )
        .await?;
    }

    // Write default tool-risk.json if not present.
    let tool_risk = claw.join("policies/tool-risk.json");
    if !tool_risk.exists() {
        let default_risks = serde_json::json!({
            "read_file": "low",
            "write_file": "medium",
            "apply_patch": "medium",
            "run_tests": "medium",
            "shell_exec": "high",
            "git_push": "critical",
            "delete_file": "high",
            "network_request": "high"
        });
        fs::write(&tool_risk, serde_json::to_string_pretty(&default_risks)?).await?;
    }

    // Write default mcp-trust.json if not present.
    let mcp_trust = claw.join("policies/mcp-trust.json");
    if !mcp_trust.exists() {
        let default_trust = serde_json::json!({ "servers": [] });
        fs::write(&mcp_trust, serde_json::to_string_pretty(&default_trust)?).await?;
    }

    Ok(())
}

/// Validate the `.claw/` directory structure.
///
/// Returns a list of missing required items (relative paths from project root).
/// An empty list means the structure is complete.
pub async fn validate_claw_dir(project_root: &Path) -> Vec<String> {
    let claw = project_root.join(".claw");
    let mut missing = Vec::new();

    for required_dir in ["tasks", "policies", "templates"] {
        if !claw.join(required_dir).exists() {
            missing.push(format!(".claw/{}/", required_dir));
        }
    }

    for required_file in ["policies/tool-risk.json", "policies/mcp-trust.json"] {
        if !claw.join(required_file).exists() {
            missing.push(format!(".claw/{}", required_file));
        }
    }

    missing
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[tokio::test]
    async fn test_init_claw_dir_creates_structure() {
        let dir = TempDir::new().unwrap();
        init_claw_dir(dir.path()).await.unwrap();

        assert!(dir.path().join(".claw/tasks").exists());
        assert!(dir.path().join(".claw/policies").exists());
        assert!(dir.path().join(".claw/templates").exists());
        assert!(dir.path().join(".claw/policies/tool-risk.json").exists());
        assert!(dir.path().join(".claw/policies/mcp-trust.json").exists());
    }

    #[tokio::test]
    async fn test_init_claw_dir_is_idempotent() {
        let dir = TempDir::new().unwrap();
        init_claw_dir(dir.path()).await.unwrap();
        // Second call must not fail
        init_claw_dir(dir.path()).await.unwrap();
    }

    #[tokio::test]
    async fn test_validate_empty_dir() {
        let dir = TempDir::new().unwrap();
        let missing = validate_claw_dir(dir.path()).await;
        assert!(
            !missing.is_empty(),
            "should report missing items in empty dir"
        );
    }

    #[tokio::test]
    async fn test_validate_after_init() {
        let dir = TempDir::new().unwrap();
        init_claw_dir(dir.path()).await.unwrap();
        let missing = validate_claw_dir(dir.path()).await;
        assert!(
            missing.is_empty(),
            "no missing items after init: {:?}",
            missing
        );
    }
}
