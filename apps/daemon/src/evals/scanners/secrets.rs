//! Secrets-in-diff scanner.
//!
//! Inspects added lines in a unified diff for credential material — API keys,
//! tokens, private keys, and high-entropy strings — and reports violations
//! before a patch is applied or committed.

use once_cell::sync::Lazy;
use regex::Regex;

// ─── Pattern registry ─────────────────────────────────────────────────────────

static SECRET_REGEXES: Lazy<Vec<(&'static str, Regex)>> = Lazy::new(|| {
    vec![
        (
            "openai_key",
            Regex::new(r"sk-[A-Za-z0-9\-_]{20,}").expect("regex: openai key"),
        ),
        (
            "github_token",
            Regex::new(r"ghp_[A-Za-z0-9]{36}").expect("regex: github token"),
        ),
        (
            "github_pat",
            Regex::new(r"github_pat_[A-Za-z0-9_]{82}").expect("regex: github pat"),
        ),
        (
            "aws_key",
            Regex::new(r"AKIA[0-9A-Z]{16}").expect("regex: aws key"),
        ),
        (
            "private_key",
            Regex::new(r"-----BEGIN\s+(?:RSA |EC |OPENSSH )?PRIVATE KEY-----")
                .expect("regex: pem header"),
        ),
        (
            "generic_secret",
            Regex::new(
                r#"(?i)(password|secret|token|api_key|auth_key|private_key)\s*[:=]\s*["']?[A-Za-z0-9+/\-_]{8,}"#,
            )
            .expect("regex: generic secret"),
        ),
    ]
});

// ─── Violation type ───────────────────────────────────────────────────────────

/// A credential violation found in an added line of a diff.
#[derive(Debug, Clone)]
pub struct SecretViolation {
    /// File the violation was found in.
    pub file: String,
    /// 1-based line number in the destination file.
    pub line: usize,
    /// Name of the pattern that matched (e.g. `"openai_key"`).
    pub secret_type: String,
    /// First 4 characters of the matched secret followed by `"..."`.
    pub redacted_preview: String,
}

// ─── Patch scanning ───────────────────────────────────────────────────────────

/// Scan a unified diff for secrets.
///
/// Only inspects **added** lines (lines starting with `+` but not `+++`).
/// Returns one `SecretViolation` per matching line (first pattern wins).
pub fn scan_patch(patch: &str) -> Vec<SecretViolation> {
    let mut violations = Vec::new();
    let mut current_file = String::new();
    let mut line_num: usize = 0;

    for line in patch.lines() {
        if line.starts_with("+++ b/") || line.starts_with("+++ ") {
            current_file = line
                .trim_start_matches("+++ b/")
                .trim_start_matches("+++ ")
                .to_string();
            line_num = 0;
            continue;
        }
        if line.starts_with("@@") {
            if let Some(plus_part) = line.split('+').nth(1) {
                line_num = plus_part
                    .split([',', ' '])
                    .next()
                    .and_then(|n| n.parse::<usize>().ok())
                    .unwrap_or(0);
            }
            continue;
        }

        if line.starts_with('+') && !line.starts_with("+++") {
            line_num += 1;
            let content = &line[1..];

            if let Some(v) = check_line(content, &current_file, line_num) {
                violations.push(v);
            }
        } else if line.starts_with(' ') {
            line_num += 1;
        }
    }

    violations
}

// ─── Entropy detection ────────────────────────────────────────────────────────

/// Detect high-entropy strings (Shannon entropy > 4.5 bits/char, min length).
///
/// Random tokens and base64-encoded secrets have significantly higher entropy
/// than natural language text.
pub fn is_high_entropy(s: &str, min_len: usize) -> bool {
    if s.len() < min_len {
        return false;
    }
    let mut freq = [0u32; 256];
    let len = s.len() as f64;
    for b in s.bytes() {
        freq[b as usize] += 1;
    }
    let entropy: f64 = freq
        .iter()
        .filter(|&&c| c > 0)
        .map(|&c| {
            let p = c as f64 / len;
            -p * p.log2()
        })
        .sum();
    entropy > 4.5
}

// ─── Private helpers ──────────────────────────────────────────────────────────

fn check_line(content: &str, file: &str, line: usize) -> Option<SecretViolation> {
    // Pattern-based check first.
    for (name, regex) in SECRET_REGEXES.iter() {
        if let Some(m) = regex.find(content) {
            let matched = m.as_str();
            let preview = format!("{}...", matched.chars().take(4).collect::<String>());
            return Some(SecretViolation {
                file: file.to_string(),
                line,
                secret_type: name.to_string(),
                redacted_preview: preview,
            });
        }
    }

    // High-entropy token check (20+ chars, entropy > 4.5).
    for word in content.split_whitespace() {
        let token = word.trim_matches(|c: char| !c.is_alphanumeric() && c != '+' && c != '/');
        if token.len() >= 20 && is_high_entropy(token, 20) {
            let preview = format!("{}...", token.chars().take(4).collect::<String>());
            return Some(SecretViolation {
                file: file.to_string(),
                line,
                secret_type: "high_entropy".to_string(),
                redacted_preview: preview,
            });
        }
    }

    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn finds_openai_key_in_diff() {
        let patch = "\
--- a/config.rs
+++ b/config.rs
@@ -1,1 +1,2 @@
 fn config() {}
+const KEY: &str = \"sk-abcdefghijklmnopqrstuvwxyz1234567890\";
";
        let violations = scan_patch(patch);
        assert!(!violations.is_empty());
        assert_eq!(violations[0].secret_type, "openai_key");
    }

    #[test]
    fn multibyte_high_entropy_token_preview_does_not_panic() {
        // Byte 4 falls inside the second 'é'; a byte-sliced preview panicked (D-1143).
        let patch = "\
--- a/main.rs
+++ b/main.rs
@@ -1,1 +1,2 @@
 fn main() {}
+let t = \"aééBCDEFGHIJKLMNOPQRSTUVWXYZ\";
";
        let violations = scan_patch(patch);
        assert_eq!(violations.len(), 1);
        assert_eq!(violations[0].redacted_preview, "aééB...");
    }

    #[test]
    fn clean_diff_no_violations() {
        let patch = "\
--- a/main.rs
+++ b/main.rs
@@ -1,1 +1,2 @@
 fn main() {}
+fn helper() { println!(\"hello\"); }
";
        let violations = scan_patch(patch);
        assert!(violations.is_empty());
    }

    #[test]
    fn high_entropy_detection() {
        // All-unique characters → Shannon entropy = log2(24) ≈ 4.58 > 4.5 threshold.
        assert!(is_high_entropy("ABCDEFGHIJ0123456789abcd", 20));
        // Natural language is not high entropy.
        assert!(!is_high_entropy("hello world this is text", 20));
    }
}
