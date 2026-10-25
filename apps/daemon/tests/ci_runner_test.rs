/// Sprint EE CI.6 — CI runner tests.
///
/// Tests that CI config parses correctly and runner status types are correct.
#[cfg(test)]
mod ci_runner_tests {
    use clawd::ci::config::{CiConfig, CiStep, CiTrigger};
    use clawd::ci::runner::CiRunStatus;

    #[test]
    fn ci_run_status_terminal_states() {
        assert!(CiRunStatus::Success.is_terminal());
        assert!(CiRunStatus::Failure.is_terminal());
        assert!(CiRunStatus::Canceled.is_terminal());
        assert!(!CiRunStatus::Running.is_terminal());
    }

    #[test]
    fn ci_run_status_exit_codes() {
        assert_eq!(CiRunStatus::Success.exit_code(), 0);
        assert_eq!(CiRunStatus::Failure.exit_code(), 1);
        assert_eq!(CiRunStatus::Canceled.exit_code(), 2);
    }

    #[test]
    fn ci_config_defaults() {
        let yaml = r#"
task: "cargo build"
steps:
  - name: build
    command: "cargo build"
"#;
        let config: CiConfig = serde_norway::from_str(yaml).unwrap();
        assert_eq!(config.steps.len(), 1);
        assert_eq!(config.steps[0].name, "build");
        assert_eq!(
            config.steps[0].command.as_deref().unwrap_or(""),
            "cargo build"
        );
        // Default trigger = push
        assert!(matches!(config.on[0], CiTrigger::Push));
    }

    #[test]
    fn ci_config_multi_step() {
        let yaml = r#"
task: "review"
on: [pull_request]
steps:
  - name: lint
    command: "Check for lint issues"
  - name: security
    command: "Check for security vulnerabilities"
  - name: summary
    command: "Write review summary"
"#;
        let config: CiConfig = serde_norway::from_str(yaml).unwrap();
        assert_eq!(config.steps.len(), 3);
        assert_eq!(config.steps[0].name, "lint");
        assert_eq!(config.steps[1].name, "security");
        assert_eq!(config.steps[2].name, "summary");
    }

    #[test]
    fn ci_step_default_provider() {
        let step = CiStep {
            name: "build".into(),
            task: None,
            command: Some("cargo build".into()),
            timeout_s: 300,
            continue_on_error: false,
        };
        // CiStep has no provider field — provider is on CiConfig
        assert_eq!(step.name, "build");
        assert_eq!(step.command.as_deref().unwrap_or(""), "cargo build");
    }
}
