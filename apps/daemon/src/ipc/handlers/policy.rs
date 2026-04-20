// ipc/handlers/policy.rs — Policy test RPC handlers (Sprint ZZ PT.T02)
//
// RPCs:
//   policy.test(file?) → TestSummary
//   policy.seedTests(project_path?) → {created, path}

use crate::policy::tester::{run_all_policy_tests, PolicyTestFile, SEED_POLICY_TESTS_YAML};
use crate::AppContext;
use anyhow::Result;
use serde_json::{json, Value};
use std::path::Path;

/// policy.test — run policy YAML test files.
/// If `file` param is given, runs that single file.
/// Otherwise scans `project_path/.clawd/tests/policy/` for *.yaml/*.yml.
pub async fn test(ctx: &AppContext, params: Value) -> Result<Value> {
    let _ = ctx; // storage not needed for local eval
    let file_path = params["file"].as_str();
    let project_path = params["project_path"].as_str().unwrap_or(".");

    if let Some(fp) = file_path {
        // Single file mode
        let content = tokio::fs::read_to_string(fp).await?;
        let test_file: PolicyTestFile = serde_yaml::from_str(&content)
            .map_err(|e| anyhow::anyhow!("invalid policy test YAML: {}", e))?;
        let summary = crate::policy::tester::run_test_file(&test_file);
        Ok(json!({
            "file": fp,
            "total": summary.total,
            "passed": summary.passed,
            "failed": summary.failed,
            "pass_rate_pct": (summary.passed * 100).checked_div(summary.total).unwrap_or(0),
            "results": summary.results.iter().map(|r| json!({
                "command": r.case.command,
                "expected": format!("{:?}", r.case.expected),
                "actual": format!("{:?}", r.actual),
                "passed": r.passed,
                "triggered_rule": r.triggered_rule,
                "reason": r.case.reason,
            })).collect::<Vec<_>>(),
        }))
    } else {
        // Directory scan mode
        let test_dir = format!("{}/.clawd/tests/policy", project_path.trim_end_matches('/'));
        let summary = run_all_policy_tests(Path::new(&test_dir)).await?;
        Ok(json!({
            "test_dir": test_dir,
            "total": summary.total,
            "passed": summary.passed,
            "failed": summary.failed,
            "pass_rate_pct": (summary.passed * 100).checked_div(summary.total).unwrap_or(0),
            "overall_passed": summary.failed == 0,
        }))
    }
}

/// policy.seedTests — write default seed test file to project.
pub async fn seed_tests(_ctx: &AppContext, params: Value) -> Result<Value> {
    let project_path = params["project_path"].as_str().unwrap_or(".");
    let dir = format!("{}/.clawd/tests/policy", project_path.trim_end_matches('/'));
    let path = format!("{}/seed.yaml", dir);

    tokio::fs::create_dir_all(&dir).await?;
    tokio::fs::write(&path, SEED_POLICY_TESTS_YAML).await?;

    Ok(json!({
        "created": true,
        "path": path,
    }))
}
