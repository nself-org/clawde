//! Approval rule evaluation — determines whether a tool call may proceed
//! immediately, must wait for human approval, or should be denied outright.

use crate::tasks::reducer::TaskState;
use crate::tasks::schema::RiskLevel;

use super::engine::PolicyDecision;

// ─── Approval rules ───────────────────────────────────────────────────────────

/// Tools that create a task instead of acting on one. Their schema has no
/// `task_id`, so no task state can exist for the Active-task rule to inspect.
const TASKLESS_TOOLS: &[&str] = &["create_task"];

/// Tools an agent calls to start the approval flow. Gating them behind
/// approval would make approvals impossible to initiate, so they are allowed
/// while the task is Active and denied otherwise.
const APPROVAL_REQUEST_TOOLS: &[&str] = &["request_approval"];

/// Configurable approval policy.
///
/// The defaults are conservative: low-risk tools auto-approve, medium-risk
/// tools require an active task, and high/critical tools always need explicit
/// human approval.
#[derive(Debug, Clone)]
pub struct ApprovalRules {
    /// Risk level at which a tool call must wait for approval.
    pub min_risk_for_approval: RiskLevel,
    /// Auto-approve tools whose risk is Low.
    pub auto_approve_low: bool,
    /// Require the task to be Active+Claimed for Medium-risk tools.
    pub require_active_task_for_medium: bool,
}

impl Default for ApprovalRules {
    fn default() -> Self {
        Self {
            min_risk_for_approval: RiskLevel::High,
            auto_approve_low: true,
            require_active_task_for_medium: true,
        }
    }
}

impl ApprovalRules {
    /// Evaluate whether a tool call with `risk` may proceed given the optional
    /// current `task_state`.
    ///
    /// Decision matrix:
    /// - `Low`      → `Allow` always (if `auto_approve_low` is set)
    /// - `Medium`   → `Allow` only when task is `Active`+`Claimed`; otherwise `Deny`
    /// - `High`     → `NeedsApproval`
    /// - `Critical` → `NeedsApproval`
    ///
    /// Two tools are special-cased ahead of the matrix: [`TASKLESS_TOOLS`]
    /// skip the Active-task requirement, and [`APPROVAL_REQUEST_TOOLS`] need an
    /// Active task but never `NeedsApproval`.
    pub fn should_approve(
        &self,
        tool: &str,
        risk: RiskLevel,
        task_state: Option<&TaskState>,
    ) -> PolicyDecision {
        if APPROVAL_REQUEST_TOOLS.contains(&tool) {
            return if matches!(task_state, Some(TaskState::Active)) {
                PolicyDecision::Allow
            } else {
                PolicyDecision::Deny {
                    reason: format!(
                        "approval-request tool '{}' requires an Active task (current: {})",
                        tool,
                        task_state.map_or_else(|| "none".to_string(), |s| s.to_string())
                    ),
                }
            };
        }
        if risk == RiskLevel::Medium && TASKLESS_TOOLS.contains(&tool) {
            return PolicyDecision::Allow;
        }

        match risk {
            RiskLevel::Low => {
                if self.auto_approve_low {
                    PolicyDecision::Allow
                } else {
                    PolicyDecision::NeedsApproval {
                        tool: tool.to_string(),
                        risk: RiskLevel::Low,
                        reason: "auto-approve disabled for low-risk tools".to_string(),
                    }
                }
            }

            RiskLevel::Medium => {
                if !self.require_active_task_for_medium {
                    return PolicyDecision::Allow;
                }
                // Medium tools require Active+Claimed task state.
                let allowed = matches!(task_state, Some(TaskState::Active));
                if allowed {
                    PolicyDecision::Allow
                } else {
                    let state_desc = task_state
                        .map(|s| s.to_string())
                        .unwrap_or_else(|| "none".to_string());
                    PolicyDecision::Deny {
                        reason: format!(
                            "medium-risk tool '{}' requires an Active+Claimed task (current: {})",
                            tool, state_desc
                        ),
                    }
                }
            }

            RiskLevel::High | RiskLevel::Critical => PolicyDecision::NeedsApproval {
                tool: tool.to_string(),
                risk,
                reason: format!(
                    "tool '{}' requires explicit approval before execution",
                    tool
                ),
            },
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rules() -> ApprovalRules {
        ApprovalRules::default()
    }

    #[test]
    fn low_risk_allows() {
        let r = rules();
        assert_eq!(
            r.should_approve("read_file", RiskLevel::Low, None),
            PolicyDecision::Allow
        );
    }

    #[test]
    fn medium_without_task_denies() {
        let r = rules();
        let decision = r.should_approve("run_tests", RiskLevel::Medium, None);
        matches!(decision, PolicyDecision::Deny { .. });
    }

    #[test]
    fn medium_with_active_task_allows() {
        let r = rules();
        let state = TaskState::Active;
        assert_eq!(
            r.should_approve("run_tests", RiskLevel::Medium, Some(&state)),
            PolicyDecision::Allow
        );
    }

    #[test]
    fn medium_with_pending_task_denies() {
        let r = rules();
        let state = TaskState::Pending;
        let decision = r.should_approve("run_tests", RiskLevel::Medium, Some(&state));
        assert!(matches!(decision, PolicyDecision::Deny { .. }));
    }

    #[test]
    fn high_risk_needs_approval() {
        let r = rules();
        let decision = r.should_approve("apply_patch", RiskLevel::High, Some(&TaskState::Active));
        assert!(matches!(decision, PolicyDecision::NeedsApproval { .. }));
    }

    #[test]
    fn create_task_allowed_without_task() {
        let r = rules();
        assert_eq!(
            r.should_approve("create_task", RiskLevel::Medium, None),
            PolicyDecision::Allow
        );
    }

    #[test]
    fn taskless_exemption_is_per_tool() {
        let r = rules();
        let decision = r.should_approve("claim_task", RiskLevel::Medium, None);
        assert!(matches!(decision, PolicyDecision::Deny { .. }));
    }

    #[test]
    fn request_approval_allowed_on_active_task() {
        let r = rules();
        assert_eq!(
            r.should_approve(
                "request_approval",
                RiskLevel::High,
                Some(&TaskState::Active)
            ),
            PolicyDecision::Allow
        );
    }

    #[test]
    fn request_approval_denied_without_active_task() {
        let r = rules();
        for state in [
            None,
            Some(TaskState::Pending),
            Some(TaskState::NeedsApproval),
        ] {
            let decision = r.should_approve("request_approval", RiskLevel::High, state.as_ref());
            assert!(matches!(decision, PolicyDecision::Deny { .. }), "{state:?}");
        }
    }

    #[test]
    fn critical_risk_needs_approval() {
        let r = rules();
        let decision = r.should_approve("git_push", RiskLevel::Critical, None);
        assert!(matches!(decision, PolicyDecision::NeedsApproval { .. }));
    }
}
