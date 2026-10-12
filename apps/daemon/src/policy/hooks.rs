//! Policy hooks — called by the MCP dispatcher before and after every tool call.
//!
//! `PolicyHooks::pre_tool` validates that the current task state permits the
//! call and that arguments do not contain raw secrets.
//!
//! `PolicyHooks::post_tool` scans the tool's output for secrets and computes
//! any follow-up actions (e.g. re-format, lint, redact for display).

use crate::tasks::reducer::TaskState;

use super::output_scan::redact_value;
use super::sandbox::PolicyViolation;
use super::secrets::check_tool_args;

// ─── Post-tool actions ────────────────────────────────────────────────────────

/// Actions the caller should take after receiving a tool result.
#[derive(Debug, Default, Clone)]
pub struct PostToolActions {
    /// Run the project formatter on changed files.
    pub run_formatter: bool,
    /// Run clippy / tsc / eslint on changed files.
    pub run_linter: bool,
    /// If secrets were found in the result, this field contains the redacted
    /// version for display.  `None` means the result is safe to display as-is.
    pub redacted_display: Option<String>,
    /// The redacted result value, keeping the original shape. `Some` only when
    /// redaction changed something; callers return it instead of the original.
    pub redacted: Option<serde_json::Value>,
}

// ─── Policy hooks ─────────────────────────────────────────────────────────────

/// Stateless policy hook runner.
///
/// Both methods are pure with respect to external state — they operate only on
/// their arguments. Stateful decisions (approval requests) live in
/// `ApprovalRouter`.
pub struct PolicyHooks;

impl PolicyHooks {
    /// Pre-tool hook: run before any tool executes.
    ///
    /// Checks:
    /// 1. For write tools, the task must be `Active` (via `check_write_state`).
    /// 2. Tool arguments must not contain raw credential material.
    pub fn pre_tool(
        tool: &str,
        args: &serde_json::Value,
        _task_id: &str,
        task_state: &TaskState,
    ) -> Result<(), PolicyViolation> {
        // Task state gate — write tools require Active state.
        if is_write_tool(tool) {
            check_write_state(task_state, tool)?;
        }

        // Argument secrets scan.
        check_tool_args(tool, args)?;

        Ok(())
    }

    /// Post-tool hook: run after a tool returns its result.
    ///
    /// Checks the result for secrets and computes follow-up actions.
    pub fn post_tool(tool: &str, result: &serde_json::Value, _task_id: &str) -> PostToolActions {
        let mut actions = PostToolActions::default();

        // Patch tool: suggest running the formatter and linter after. Secret
        // redaction (including patch text) is done by `redact_value` below.
        if tool == "apply_patch" {
            actions.run_formatter = true;
            actions.run_linter = true;
        }

        // For test-running tools, suggest linting too.
        if tool == "run_tests" {
            actions.run_linter = true;
        }

        // Redact the result by walking the JSON value (not its serialized text).
        let redacted = redact_value(result.clone());
        if redacted != *result {
            actions.redacted_display = Some(redacted.to_string());
            actions.redacted = Some(redacted);
        }

        actions
    }
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/// Write tools that require the task to be in the `Active` state.
const WRITE_TOOLS: &[&str] = &["apply_patch", "run_tests"];

fn is_write_tool(tool: &str) -> bool {
    WRITE_TOOLS.contains(&tool)
}

fn check_write_state(state: &TaskState, tool: &str) -> Result<(), PolicyViolation> {
    if *state == TaskState::Active {
        Ok(())
    } else {
        Err(PolicyViolation::UnauthorizedTool {
            tool: tool.to_string(),
            role: format!("task-state:{}", state),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn pre_tool_read_always_allowed() {
        // read_file is not a write tool — no state check needed.
        let result = PolicyHooks::pre_tool(
            "read_file",
            &json!({ "path": "src/main.rs" }),
            "t1",
            &TaskState::Pending,
        );
        assert!(result.is_ok());
    }

    #[test]
    fn pre_tool_apply_patch_active_ok() {
        let result = PolicyHooks::pre_tool(
            "apply_patch",
            &json!({ "task_id": "t1", "patch": "diff..." }),
            "t1",
            &TaskState::Active,
        );
        assert!(result.is_ok());
    }

    #[test]
    fn pre_tool_apply_patch_pending_denied() {
        let result = PolicyHooks::pre_tool(
            "apply_patch",
            &json!({ "task_id": "t1", "patch": "diff..." }),
            "t1",
            &TaskState::Pending,
        );
        assert!(result.is_err());
        assert!(matches!(
            result,
            Err(PolicyViolation::UnauthorizedTool { .. })
        ));
    }

    #[test]
    fn pre_tool_secret_in_args_blocked() {
        let result = PolicyHooks::pre_tool(
            "read_file",
            &json!({ "key": "sk-abcdefghijklmnopqrstuvwxyz1234567890" }),
            "t1",
            &TaskState::Active,
        );
        assert!(result.is_err());
        assert!(matches!(
            result,
            Err(PolicyViolation::SecretDetected { .. })
        ));
    }

    #[test]
    fn post_tool_clean_result_no_redaction() {
        let result = json!({ "output": "tests passed: 5/5" });
        let actions = PolicyHooks::post_tool("run_tests", &result, "t1");
        assert!(actions.redacted_display.is_none());
        assert!(actions.run_linter);
    }

    #[test]
    fn post_tool_clean_patch_not_redacted() {
        let result = json!({ "applied": true, "patch": "+fn helper() {}" });
        let actions = PolicyHooks::post_tool("apply_patch", &result, "t1");
        assert!(actions.redacted.is_none());
        assert!(actions.redacted_display.is_none());
    }

    #[test]
    fn post_tool_sensitive_key_redacted() {
        let result = json!({ "token": "abcd1234efgh", "ok": 1 });
        let actions = PolicyHooks::post_tool("log_event", &result, "t1");
        let red = actions.redacted.expect("redacted value");
        assert_eq!(red["token"], "[REDACTED]");
        assert_eq!(red["ok"], 1);
        assert!(!actions.redacted_display.unwrap().contains("abcd1234efgh"));
    }

    #[test]
    fn post_tool_patch_tool_suggests_formatter() {
        let result = json!({ "applied": true });
        let actions = PolicyHooks::post_tool("apply_patch", &result, "t1");
        assert!(actions.run_formatter);
        assert!(actions.run_linter);
    }
}
