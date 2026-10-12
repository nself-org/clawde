//! Sandbox policy — path-escape and network-access checks.
//!
//! All file mutations must stay within the task's worktree root. Any attempt
//! to write outside the worktree is a `PathEscape` violation.  Network access
//! is denied by default unless explicitly permitted by the policy.

use std::path::{Path, PathBuf};

use thiserror::Error;

// ─── Policy violations ────────────────────────────────────────────────────────

/// A policy rule was broken.
#[derive(Debug, Error, Clone, PartialEq)]
pub enum PolicyViolation {
    /// A file path targets a location outside the worktree root.
    #[error("path escape: target '{target}' is outside worktree '{worktree}'")]
    PathEscape { target: String, worktree: String },

    /// Network access was attempted but is not permitted by policy.
    #[error("network access denied by policy")]
    NetworkDenied,

    /// The tool is not authorised for this agent role.
    #[error("tool '{tool}' is not authorised for role '{role}'")]
    UnauthorizedTool { tool: String, role: String },

    /// A secret was detected in tool arguments or output.
    #[error("secret detected in {location}: {detail}")]
    SecretDetected { location: String, detail: String },

    /// A placeholder stub was found in a patch that is transitioning to CR.
    #[error("placeholder detected at {file}:{line} — pattern: {pattern}")]
    PlaceholderDetected {
        file: String,
        line: usize,
        pattern: String,
    },

    /// MCP server binary hash mismatch.
    #[error(
        "supply-chain mismatch for server '{server}': expected {expected_hash}, got {actual_hash}"
    )]
    SupplyChainMismatch {
        server: String,
        expected_hash: String,
        actual_hash: String,
    },

    /// MCP server is not in the supply-chain allowlist (never registered).
    #[error("supply-chain: server '{server}' is not registered in the allowlist")]
    UnregisteredServer { server: String },

    /// Acceptance criteria are missing from the task spec.
    #[error("task has no acceptance criteria — cannot transition to CR")]
    NoAcceptanceCriteria,

    /// Tests have not been run for this task.
    #[error("tests have not been run — cannot transition to CR")]
    TestsNotRun,

    /// Tests were run but are not passing.
    #[error("tests are failing — cannot transition to CR")]
    TestsFailing,
}

// ─── Sandbox policy ───────────────────────────────────────────────────────────

/// Sandbox rules for a single task's execution context.
pub struct SandboxPolicy {
    /// Absolute path of the task's worktree root.
    pub worktree_root: PathBuf,
    /// Whether outbound network calls are permitted.
    pub allow_network: bool,
}

impl SandboxPolicy {
    /// Create a new sandbox policy.
    pub fn new(worktree_root: impl Into<PathBuf>, allow_network: bool) -> Self {
        let root: PathBuf = worktree_root.into();
        // Canonicalize at construction so symlink-based OS temp dirs (macOS /var → /private/var)
        // are resolved once, preventing divergence in check_path comparisons.
        let worktree_root = root.canonicalize().unwrap_or(root);
        Self {
            worktree_root,
            allow_network,
        }
    }

    /// Check that `target_path` is inside `worktree_root`.
    ///
    /// Uses `Path::starts_with` after canonicalizing both paths.  If
    /// canonicalization fails (e.g. the path does not yet exist), falls back to
    /// a lexical prefix check.
    pub fn check_path(&self, target_path: &Path) -> Result<(), PolicyViolation> {
        // Try canonical paths first (resolves symlinks, `..` components).
        let target_canonical = target_path
            .canonicalize()
            .unwrap_or_else(|_| target_path.to_path_buf());
        let root_canonical = self
            .worktree_root
            .canonicalize()
            .unwrap_or_else(|_| self.worktree_root.clone());

        if target_canonical.starts_with(&root_canonical) {
            return Ok(());
        }

        // Lexical fallback — useful when the target does not exist yet.
        // Normalise both paths and do a prefix check.
        let target_str = target_canonical.to_string_lossy();
        let root_str = format!("{}/", root_canonical.to_string_lossy());

        if target_str.starts_with(root_str.as_str())
            || target_str == root_canonical.to_string_lossy()
        {
            return Ok(());
        }

        Err(PolicyViolation::PathEscape {
            target: target_canonical.to_string_lossy().into_owned(),
            worktree: root_canonical.to_string_lossy().into_owned(),
        })
    }

    /// Check that network access is allowed.
    pub fn check_network_allowed(&self) -> Result<(), PolicyViolation> {
        if self.allow_network {
            Ok(())
        } else {
            Err(PolicyViolation::NetworkDenied)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::env;

    fn sandbox(allow_net: bool) -> SandboxPolicy {
        SandboxPolicy::new(env::temp_dir(), allow_net)
    }

    #[test]
    fn path_inside_worktree_ok() {
        let sb = sandbox(false);
        // Derive the inside path from the sandbox's already-canonical root
        // to avoid /var → /private/var symlink divergence on macOS.
        let inside = sb.worktree_root.join("some_file.txt");
        assert!(sb.check_path(&inside).is_ok());
    }

    #[test]
    fn path_outside_worktree_is_violation() {
        let sb = SandboxPolicy::new("/tmp/worktree", false);
        let outside = Path::new("/etc/passwd");
        assert!(matches!(
            sb.check_path(outside),
            Err(PolicyViolation::PathEscape { .. })
        ));
    }

    #[test]
    fn network_allowed_ok() {
        let sb = sandbox(true);
        assert!(sb.check_network_allowed().is_ok());
    }

    #[test]
    fn network_denied_is_violation() {
        let sb = sandbox(false);
        assert!(matches!(
            sb.check_network_allowed(),
            Err(PolicyViolation::NetworkDenied)
        ));
    }
}
