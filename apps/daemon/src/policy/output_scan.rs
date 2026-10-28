//! Output scanning — secret redaction and untrusted content labelling.
//!
//! Applied after a tool returns its result and before the output is displayed
//! or stored in the event log.  Wraps `crate::evals::scanners::secrets` and
//! `crate::telemetry::redact`.

use serde_json::Value;

use crate::evals::scanners::secrets as secret_scanner;
use crate::telemetry::redact::redact_str;

use super::sandbox::PolicyViolation;

// ─── Untrusted content label ──────────────────────────────────────────────────

/// Wraps a string value with a flag indicating whether its origin is untrusted
/// (e.g. tool output from an external MCP server).
#[derive(Debug, Clone)]
pub struct UntrustedContentLabel {
    /// The (possibly redacted) content.
    pub content: String,
    /// `true` when this content comes from an untrusted source and should be
    /// treated with caution before display or further processing.
    pub from_untrusted: bool,
}

impl UntrustedContentLabel {
    pub fn trusted(content: impl Into<String>) -> Self {
        Self {
            content: content.into(),
            from_untrusted: false,
        }
    }

    pub fn untrusted(content: impl Into<String>) -> Self {
        Self {
            content: content.into(),
            from_untrusted: true,
        }
    }
}

// ─── Patch / diff scanning ────────────────────────────────────────────────────

/// Scan a patch diff for secrets.
///
/// Returns a list of `PolicyViolation::SecretDetected` entries — one for each
/// added line in the diff that contains a credential.  Uses the same pattern
/// set as `evals::scanners::secrets::scan_patch`.
pub fn scan_patch_output(patch: &str) -> Vec<PolicyViolation> {
    secret_scanner::scan_patch(patch)
        .into_iter()
        .map(|v| PolicyViolation::SecretDetected {
            location: format!("{}:{}", v.file, v.line),
            detail: format!("[{}] {}", v.secret_type, v.redacted_preview),
        })
        .collect()
}

// ─── Log / display text redaction ────────────────────────────────────────────

/// Redact secrets from a log or display string.
///
/// Uses `telemetry::redact::redact_str` for the redaction logic.
/// Returns the redacted string (unchanged if no secrets were found).
pub fn scan_log_output(text: &str) -> String {
    let (redacted, _changed) = redact_str(text);
    redacted
}

/// Object keys (matched case-insensitively as substrings) whose string values
/// are always replaced with `[REDACTED]`.
const SENSITIVE_KEYS: &[&str] = &[
    "password",
    "passwd",
    "secret",
    "token",
    "api_key",
    "apikey",
    "access_key",
    "private_key",
    "authorization",
    "credential",
];

/// Recursively redact secrets in a JSON value, keeping its shape.
///
/// - A string under an object key matching [`SENSITIVE_KEYS`] becomes `"[REDACTED]"`.
/// - A string leaf containing `PRIVATE KEY-----` is replaced whole with
///   `"[REDACTED: private key]"`.
/// - Every other string leaf goes through [`scan_log_output`].
///
/// Unlike scanning `value.to_string()`, this sees the real string contents, not
/// their JSON-escaped form, so `{"password":"..."}` and multi-line key bodies
/// are caught.
pub fn redact_value(v: Value) -> Value {
    match v {
        Value::String(s) => Value::String(redact_leaf(&s)),
        Value::Array(items) => Value::Array(items.into_iter().map(redact_value).collect()),
        Value::Object(map) => Value::Object(
            map.into_iter()
                .map(|(k, val)| {
                    let lower = k.to_lowercase();
                    let sensitive = SENSITIVE_KEYS.iter().any(|s| lower.contains(s));
                    let val = match val {
                        Value::String(_) if sensitive => Value::String("[REDACTED]".into()),
                        other => redact_value(other),
                    };
                    (k, val)
                })
                .collect(),
        ),
        other => other,
    }
}

fn redact_leaf(s: &str) -> String {
    if s.contains("PRIVATE KEY-----") {
        "[REDACTED: private key]".to_string()
    } else {
        scan_log_output(s)
    }
}

/// Redact secrets from a log or display string, including whole private key
/// bodies. This is the error-path counterpart to [`redact_value`]: handler
/// errors are plain strings, not structured JSON, so they need the same
/// whole-key rule applied at the string level.
pub fn scan_error_output(text: &str) -> String {
    if text.contains("PRIVATE KEY-----") {
        "[REDACTED: private key]".to_string()
    } else {
        scan_log_output(text)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scan_patch_finds_api_key() {
        let patch = "\
--- a/config.rs
+++ b/config.rs
@@ -1,1 +1,2 @@
 fn config() {}
+const KEY: &str = \"sk-abcdefghijklmnopqrstuvwxyz1234567890\";
";
        let violations = scan_patch_output(patch);
        assert!(!violations.is_empty());
        assert!(matches!(
            &violations[0],
            PolicyViolation::SecretDetected { .. }
        ));
    }

    #[test]
    fn clean_patch_no_violations() {
        let patch = "\
--- a/main.rs
+++ b/main.rs
@@ -1,1 +1,2 @@
 fn main() {}
+fn helper() { println!(\"hello\"); }
";
        let violations = scan_patch_output(patch);
        assert!(violations.is_empty());
    }

    #[test]
    fn scan_log_redacts_token() {
        let input = "calling api with sk-abcdefghijklmnopqrstuvwxyz123456";
        let output = scan_log_output(input);
        assert!(!output.contains("sk-abc"));
        assert!(output.contains("[REDACTED]"));
    }

    #[test]
    fn untrusted_label_flag() {
        let label = UntrustedContentLabel::untrusted("some output");
        assert!(label.from_untrusted);

        let safe = UntrustedContentLabel::trusted("safe output");
        assert!(!safe.from_untrusted);
    }

    #[test]
    fn redact_value_sensitive_key_any_case() {
        let out = redact_value(serde_json::json!({"Password": "hunter2hunter2", "n": 5}));
        assert_eq!(out["Password"], "[REDACTED]");
        assert_eq!(out["n"], 5);
    }

    #[test]
    fn redact_value_escaped_assignment_in_leaf() {
        let out = redact_value(serde_json::json!({"note": "password=\"abcdefgh1234\""}));
        assert!(!out.to_string().contains("abcdefgh1234"));
    }

    #[test]
    fn redact_value_private_key_body() {
        let pem = "-----BEGIN PRIVATE KEY-----\nMIIEFAKEBODY\n-----END PRIVATE KEY-----";
        let out = redact_value(serde_json::json!({"log": pem}));
        assert_eq!(out["log"], "[REDACTED: private key]");
    }

    #[test]
    fn redact_value_nested_and_clean_untouched() {
        let key = format!("sk-{}", "FAKE".repeat(9));
        let v = serde_json::json!({"a": [{"b": format!("x {key}")}], "ok": "hello", "t": true});
        let out = redact_value(v);
        assert!(!out.to_string().contains(&key));
        assert_eq!(out["ok"], "hello");
        assert_eq!(out["t"], true);
    }

    /// D-1145: handler error strings must redact whole private key bodies,
    /// not just the PEM header line. Without the fix, `scan_error_output`
    /// (or `scan_log_output` on the error path) leaves the key body intact.
    #[test]
    fn scan_error_output_redacts_whole_private_key() {
        let pem = "error: -----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA0Z3VS5JJcds3xfn/ygWyF8PbnGy0AHB7MhgHcTz6sE2I2yPB\naFDrBz9vFqU4yplmMFRjHkDfKxYnGdNl0Jg==\n-----END RSA PRIVATE KEY-----";
        let out = scan_error_output(pem);
        assert_eq!(out, "[REDACTED: private key]");
        assert!(!out.contains("MIIEpA"));
        assert!(!out.contains("BEGIN RSA"));
    }

    #[test]
    fn scan_error_output_preserves_non_key_errors() {
        let msg = "connection refused: timeout after 30s";
        assert_eq!(scan_error_output(msg), msg);
    }
}
