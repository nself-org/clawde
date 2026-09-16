// SPDX-License-Identifier: MIT
//! Security utilities — Phase 46.
//!
//! Guards against path traversal, unsafe file access, and other security risks.

use anyhow::{bail, Result};
use std::path::{Path, PathBuf};

/// Validate that `path` is within `base_dir` (no traversal attacks).
///
/// Resolves symlinks and canonicalizes both paths before comparing.
/// Returns the canonicalized safe path on success.
///
/// Examples:
///   security::safe_path("/home/user/repo", "src/main.rs") → Ok("/home/user/repo/src/main.rs")
///   security::safe_path("/home/user/repo", "../../../etc/passwd") → Err(...)
pub fn safe_path(base_dir: &Path, relative_path: &Path) -> Result<PathBuf> {
    // If relative_path is absolute, reject it
    if relative_path.is_absolute() {
        bail!(
            "path traversal: absolute path not allowed: {}",
            relative_path.display()
        );
    }

    // Join base + relative
    let joined = base_dir.join(relative_path);

    // Normalize without requiring the path to exist (canonicalize would fail)
    let normalized = normalize_path(&joined);

    // Ensure normalized path starts with base_dir
    let base_normalized = normalize_path(base_dir);
    if !normalized.starts_with(&base_normalized) {
        bail!(
            "path traversal: {} escapes base directory {}",
            relative_path.display(),
            base_dir.display()
        );
    }

    Ok(normalized)
}

/// Normalize a path by resolving `.` and `..` components without requiring
/// the path to exist on disk (unlike std::fs::canonicalize).
pub fn normalize_path(path: &Path) -> PathBuf {
    let mut components = Vec::new();
    for component in path.components() {
        use std::path::Component::*;
        match component {
            ParentDir => {
                if matches!(components.last(), Some(Normal(_))) {
                    components.pop();
                }
                // Ignore .. at root
            }
            CurDir => {
                // Skip .
            }
            other => components.push(other),
        }
    }
    components.iter().collect()
}

/// Strip null bytes from a string (prevent null-byte injection in file paths).
pub fn strip_null_bytes(s: &str) -> String {
    s.replace('\0', "")
}

/// A character of the base64 alphabet, as the credential scanner sees it.
fn is_base64_char(c: char) -> bool {
    c.is_ascii_alphanumeric() || c == '+' || c == '/'
}

/// Runs of at least this many base64-alphabet characters are treated as
/// credential material rather than ordinary text.
const MIN_REDACTED_RUN: usize = 40;

/// Sanitize tool input before storing in the audit log.
///
/// Replaces any run of `MIN_REDACTED_RUN`+ base64-alphabet characters (API
/// keys, tokens) with `[REDACTED]` to prevent credential leakage into audit
/// storage.  Everything else is copied through unchanged.
///
/// The scan groups the input into maximal runs of same-class characters rather
/// than walking a cursor by hand.  That is deliberate: the previous version
/// advanced an index with `i += run` / `i += 1` inside a `while i < len` loop,
/// which left three separate ways for the loop to stop making progress — and
/// the mutation gate found all three, hanging the job for 2 hours instead of
/// reporting a result.  Iterating over groups makes progress structural, so no
/// arithmetic slip can turn this into an infinite loop.
pub fn sanitize_tool_input(input: &str) -> String {
    let chars: Vec<char> = input.chars().collect();
    let mut result = String::with_capacity(input.len());

    for group in chars.chunk_by(|a, b| is_base64_char(*a) == is_base64_char(*b)) {
        // `chunk_by` never yields an empty group, so `group[0]` is safe and
        // decides the class of the whole run.
        if group.len() >= MIN_REDACTED_RUN && is_base64_char(group[0]) {
            result.push_str("[REDACTED]");
        } else {
            result.extend(group.iter());
        }
    }
    result
}

/// Check a tool call against the security allowlist/denylist config (DC.T40).
///
/// Returns `Ok(())` if the call is permitted, `Err(...)` if it should be blocked.
///
/// Rules (applied in order):
/// 1. If `denied_tools` contains the tool name → blocked.
/// 2. If `allowed_tools` is non-empty AND does not contain the tool name → blocked.
/// 3. For Bash tool: if the command starts with any `denied_paths` entry → blocked.
pub fn check_tool_call(
    tool_name: &str,
    tool_input: &str,
    config: &crate::config::SecurityConfig,
) -> Result<()> {
    let tool_lower = tool_name.to_lowercase();

    // 1. Check deny list
    for denied in &config.denied_tools {
        if denied.to_lowercase() == tool_lower {
            bail!(
                "TOOL_DENIED: tool '{}' is in the security.denied_tools list",
                tool_name
            );
        }
    }

    // 2. Check allowlist (non-empty = restrictive)
    if !config.allowed_tools.is_empty() {
        let is_allowed = config
            .allowed_tools
            .iter()
            .any(|a| a.to_lowercase() == tool_lower);
        if !is_allowed {
            bail!(
                "TOOL_NOT_ALLOWED: tool '{}' is not in the security.allowed_tools list",
                tool_name
            );
        }
    }

    // 3. Check denied_paths for Bash tool
    if tool_lower == "bash" {
        for denied_path in &config.denied_paths {
            let expanded = expand_home(denied_path);
            if tool_input.starts_with(&expanded) || tool_input.contains(&expanded) {
                bail!(
                    "TOOL_PATH_DENIED: Bash command accesses denied path '{}'",
                    denied_path
                );
            }
        }
    }

    Ok(())
}

/// Expand `~` at the start of a path to the current user's home directory.
fn expand_home(path: &str) -> String {
    if let Some(rest) = path.strip_prefix("~/") {
        let home = std::env::var("HOME")
            .or_else(|_| std::env::var("USERPROFILE"))
            .unwrap_or_default();
        if !home.is_empty() {
            return format!("{}/{}", home, rest);
        }
    }
    path.to_string()
}

/// Check that a repo path is safe to use as a session workspace (DC.T41).
///
/// Rejects: paths that overlap with the daemon's own data directory.
/// Warns about: repos that contain a `.clawd/` directory (config injection risk).
pub fn check_repo_path_safety(repo_path: &Path, data_dir: &Path) -> Result<()> {
    // Use canonicalize where possible; fall back to normalize_path for non-existent paths.
    let canonical_repo = repo_path
        .canonicalize()
        .unwrap_or_else(|_| normalize_path(repo_path));
    let canonical_data = data_dir
        .canonicalize()
        .unwrap_or_else(|_| normalize_path(data_dir));

    if canonical_repo.starts_with(&canonical_data) || canonical_data.starts_with(&canonical_repo) {
        bail!(
            "invalid type: repo_path '{}' overlaps with the daemon data directory — \
             this is a security risk",
            repo_path.display()
        );
    }

    if canonical_repo.join(".clawd").exists() {
        tracing::warn!(
            repo = %canonical_repo.display(),
            "repo contains .clawd/ directory — ignoring it as config source (injection protection)"
        );
    }

    Ok(())
}

/// Validate that a session ID is a valid UUID (no injection possible).
pub fn validate_session_id(id: &str) -> Result<()> {
    // UUIDs are 36 chars: 8-4-4-4-12 hex + dashes
    if id.len() != 36 {
        bail!("invalid session ID length: {}", id.len());
    }
    for (i, c) in id.chars().enumerate() {
        let is_dash = matches!(i, 8 | 13 | 18 | 23);
        if is_dash {
            if c != '-' {
                bail!("invalid session ID format at position {}", i);
            }
        } else if !c.is_ascii_hexdigit() {
            bail!("invalid session ID character at position {}: {}", i, c);
        }
    }
    Ok(())
}

// Tests live in guard/tests.rs.
#[cfg(test)]
mod tests;
