//! Secrets policy — prevents raw credentials from being passed as tool
//! arguments to AI agents or external MCP servers.
//!
//! `check_tool_args` scans all string values in the arguments JSON object and
//! returns a `PolicyViolation::SecretDetected` error if any value matches a
//! known secret pattern or appears to be a high-entropy token.

use once_cell::sync::Lazy;
use regex::Regex;

use super::sandbox::PolicyViolation;

// ─── Never-expose patterns ────────────────────────────────────────────────────

/// Regex patterns that must never appear in tool arguments sent to an LLM.
///
/// These cover the most common credential formats. The list mirrors the
/// patterns in `evals::scanners::secrets` but is used here at the *policy
/// layer* — i.e. before a tool is even invoked.
pub static NEVER_EXPOSE_PATTERNS: &[&str] = &[
    r"sk-[A-Za-z0-9\-_]{20,}",      // Anthropic / OpenAI API key
    r"ghp_[A-Za-z0-9]{36}",         // GitHub classic PAT
    r"github_pat_[A-Za-z0-9_]{82}", // GitHub fine-grained PAT
    r"AKIA[0-9A-Z]{16}",            // AWS access key ID
    r"-----BEGIN\s+(?:RSA |EC |OPENSSH )?PRIVATE KEY-----", // PEM header
    r#"(?i)(password|secret|token|api_key|auth_key|private_key)\s*[:=]\s*["']?[A-Za-z0-9+/\-_]{8,}"#,
];

static COMPILED_PATTERNS: Lazy<Vec<Regex>> = Lazy::new(|| {
    NEVER_EXPOSE_PATTERNS
        .iter()
        .map(|p| Regex::new(p).expect("NEVER_EXPOSE_PATTERNS: invalid regex"))
        .collect()
});

/// Opaque reference to a stored secret. Used when a secret must be passed
/// through the system without being exposed directly.
#[derive(Debug, Clone)]
pub struct SecretReference {
    /// Stable identifier used to look up the secret in the vault.
    pub ref_id: String,
    /// Human-readable description of what this secret is.
    pub description: String,
}

/// Conceptual interface for a secrets vault (placeholder for now).
///
/// In a future phase this will be wired to the system keychain or an
/// encrypted vault file.
#[derive(Debug, Default)]
pub struct SecretsVault {
    // Fields will be added when the vault is implemented.
}

impl SecretsVault {
    pub fn new() -> Self {
        Self::default()
    }
}

// ─── Tool argument checking ───────────────────────────────────────────────────

/// Scan all string values in `args` for raw credential material.
///
/// Returns the first `PolicyViolation::SecretDetected` found, or `Ok(())` if
/// the arguments are clean.  Callers should reject the tool call entirely on
/// violation.
pub fn check_tool_args(tool: &str, args: &serde_json::Value) -> Result<(), PolicyViolation> {
    scan_value(tool, args, "args")
}

// ─── Private helpers ──────────────────────────────────────────────────────────

fn scan_value(tool: &str, value: &serde_json::Value, path: &str) -> Result<(), PolicyViolation> {
    match value {
        serde_json::Value::String(s) => {
            check_string(tool, s, path)?;
        }
        serde_json::Value::Object(map) => {
            for (key, val) in map {
                let new_path = format!("{}.{}", path, key);
                scan_value(tool, val, &new_path)?;
            }
        }
        serde_json::Value::Array(arr) => {
            for (i, val) in arr.iter().enumerate() {
                let new_path = format!("{}[{}]", path, i);
                scan_value(tool, val, &new_path)?;
            }
        }
        _ => {}
    }
    Ok(())
}

fn check_string(_tool: &str, s: &str, path: &str) -> Result<(), PolicyViolation> {
    for regex in COMPILED_PATTERNS.iter() {
        if let Some(m) = regex.find(s) {
            let matched = m.as_str();
            let preview = format!("{}...", &matched[..matched.len().min(4)]);
            return Err(PolicyViolation::SecretDetected {
                location: path.to_string(),
                detail: preview,
            });
        }
    }

    // High-entropy check: any word of 20+ chars with Shannon entropy > 4.5.
    for word in s.split_whitespace() {
        let token = word.trim_matches(|c: char| !c.is_alphanumeric() && c != '+' && c != '/');
        if token.len() >= 20 && crate::telemetry::redact::is_high_entropy(token) {
            let preview = format!("{}...", &token[..token.len().min(4)]);
            return Err(PolicyViolation::SecretDetected {
                location: path.to_string(),
                detail: preview,
            });
        }
    }

    Ok(())
}

// Tests live in tests.rs.
#[cfg(test)]
mod tests;
