// security/content_labels.rs — Content labeling + injection defense (Sprint ZZ PI.T01, PI.T04)
//
// Tags all externally-sourced content with provenance labels.
// High-risk content is stripped before passing to the AI.

use crate::storage::Storage;
use anyhow::Result;
use serde::{Deserialize, Serialize};

/// Source type for incoming content — determines trust level.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SourceType {
    /// File content read from the local filesystem (trusted).
    File,
    /// Git log output (trusted — controlled by user's repo).
    GitLog,
    /// Git diff output (trusted — controlled by user's repo).
    GitDiff,
    /// Standard error from a process (semi-trusted).
    Stderr,
    /// Content fetched from an HTTP endpoint (untrusted).
    WebFetch,
    /// Response from an MCP tool (untrusted).
    McpToolResponse,
    /// Direct user input via chat (untrusted).
    UserInput,
    /// Daemon-generated internal content (fully trusted).
    DaemonInternal,
}

impl SourceType {
    pub fn is_untrusted(&self) -> bool {
        matches!(
            self,
            Self::WebFetch | Self::McpToolResponse | Self::UserInput
        )
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::File => "file",
            Self::GitLog => "git_log",
            Self::GitDiff => "git_diff",
            Self::Stderr => "stderr",
            Self::WebFetch => "web_fetch",
            Self::McpToolResponse => "mcp_tool_response",
            Self::UserInput => "user_input",
            Self::DaemonInternal => "daemon_internal",
        }
    }

    pub fn parse(s: &str) -> Self {
        match s {
            "git_log" => Self::GitLog,
            "git_diff" => Self::GitDiff,
            "stderr" => Self::Stderr,
            "web_fetch" => Self::WebFetch,
            "mcp_tool_response" => Self::McpToolResponse,
            "user_input" => Self::UserInput,
            "daemon_internal" => Self::DaemonInternal,
            _ => Self::File,
        }
    }
}

/// Risk level of content after analysis.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "snake_case")]
pub enum RiskLevel {
    Low,
    Medium,
    High,
}

impl RiskLevel {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Low => "low",
            Self::Medium => "medium",
            Self::High => "high",
        }
    }
}

/// Result of analyzing content for injection risk.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContentAnalysis {
    pub risk_level: RiskLevel,
    pub patterns_found: Vec<String>,
    pub sanitized_content: Option<String>,
}

/// PI.T01 — Tag content with source and analyze for injection risk.
pub fn analyze_content(content: &str, source_type: &SourceType) -> ContentAnalysis {
    let mut patterns_found = Vec::new();
    let mut risk_level = RiskLevel::Low;

    // Escalate baseline risk for untrusted sources
    if source_type.is_untrusted() {
        risk_level = RiskLevel::Medium;
    }

    // High-risk patterns: instruction override attempts
    let high_risk_patterns = [
        "ignore previous instructions",
        "ignore all previous",
        "disregard your instructions",
        "you are now",
        "your new task is",
        "your new role is",
        "act as if",
        "pretend you are",
        "forget everything",
        "override your",
        "bypass your",
        "system: ",
        "[system]",
        "<system>",
        "[[override]]",
    ];

    // Medium-risk patterns: capability escalation attempts
    let medium_risk_patterns = [
        "rm -rf",
        "sudo rm",
        "drop table",
        "delete from",
        "curl | sh",
        "wget | sh",
        "exec(",
        "eval(",
        "os.system(",
        "subprocess.call(",
        "`rm ",
        "$(rm ",
    ];

    let content_lower = content.to_lowercase();

    for pattern in &high_risk_patterns {
        if content_lower.contains(pattern) {
            patterns_found.push((*pattern).to_string());
            risk_level = RiskLevel::High;
        }
    }

    if risk_level < RiskLevel::High {
        for pattern in &medium_risk_patterns {
            if content_lower.contains(pattern) {
                patterns_found.push((*pattern).to_string());
                if risk_level < RiskLevel::Medium {
                    risk_level = RiskLevel::Medium;
                }
            }
        }
    }

    ContentAnalysis {
        risk_level,
        patterns_found,
        sanitized_content: None,
    }
}

/// PI.T04 — Strip high-risk segments from content before passing to the AI.
///
/// Returns sanitized content and a list of stripped segment descriptions.
pub fn sanitize_content(content: &str, analysis: &ContentAnalysis) -> (String, Vec<String>) {
    if analysis.risk_level < RiskLevel::High {
        return (content.to_string(), Vec::new());
    }

    let mut sanitized = content.to_string();
    let mut stripped = Vec::new();

    let high_risk_patterns = [
        "ignore previous instructions",
        "ignore all previous",
        "disregard your instructions",
        "you are now",
        "your new task is",
        "your new role is",
        "act as if",
        "pretend you are",
        "forget everything",
        "override your",
        "bypass your",
        "[system]",
        "<system>",
        "[[override]]",
    ];

    for pattern in &high_risk_patterns {
        // Recompute lowercase on each iteration — sanitized changes as we strip.
        while let Some(idx) = sanitized.to_lowercase().find(pattern) {
            // Find end of the injection sentence/line
            let end = sanitized[idx..]
                .find(['.', '\n', '!', '?'])
                .map(|i| idx + i + 1)
                .unwrap_or(sanitized.len());

            let stripped_segment = sanitized[idx..end].to_string();
            stripped.push(format!(
                "Stripped injection attempt: '{}'",
                &stripped_segment[..stripped_segment.len().min(80)]
            ));
            sanitized = format!("{}[SANITIZED]{}", &sanitized[..idx], &sanitized[end..]);
        }
    }

    (sanitized, stripped)
}

/// Store a content label in the database (PI.T01).
pub async fn record_content_label(
    storage: &Storage,
    session_id: &str,
    source_type: &SourceType,
    analysis: &ContentAnalysis,
) -> Result<String> {
    let id = uuid::Uuid::new_v4().to_string().replace('-', "");
    let now = chrono::Utc::now().timestamp();
    let patterns_json = serde_json::to_string(&analysis.patterns_found)?;

    sqlx::query(
        "INSERT INTO content_labels \
         (id, session_id, source_type, risk_level, flagged_patterns_json, created_at) \
         VALUES (?, ?, ?, ?, ?, ?)",
    )
    .bind(&id)
    .bind(session_id)
    .bind(source_type.as_str())
    .bind(analysis.risk_level.as_str())
    .bind(&patterns_json)
    .bind(now)
    .execute(storage.pool())
    .await?;

    Ok(id)
}

// Tests live in content_labels/tests.rs. They were moved out when the suite
// grew to target the gate's surviving-mutant list; that also keeps this file
// under the 300-line guidance.
#[cfg(test)]
mod tests;
