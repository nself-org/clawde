//! Forbidden tool detector.
//!
//! Checks whether a tool call is permitted given the session's approved
//! permissions and the worktree boundary.  Returns a `ForbiddenToolViolation`
//! when the tool is not allowed; `None` when it is permitted.

// ─── Violation type ───────────────────────────────────────────────────────────

/// A tool call that exceeded the session's granted permissions.
#[derive(Debug, Clone)]
pub struct ForbiddenToolViolation {
    /// Name of the tool that was called.
    pub tool_name: String,
    /// Human-readable reason the tool call was rejected.
    pub reason: String,
    /// Permission name that would allow this tool call.
    pub permission_needed: String,
}

// ─── Check function ───────────────────────────────────────────────────────────

/// Determine whether a tool call is allowed given the approved permissions.
///
/// # Arguments
/// * `tool_name`            — the name of the tool being invoked
/// * `approved_permissions` — slice of permission strings granted to the session
/// * `target_path`          — optional file path the tool will act on
/// * `worktree_path`        — optional root of the agent's working directory
///
/// Returns `Some(ForbiddenToolViolation)` if the call should be blocked,
/// or `None` if it is permitted.
pub fn check_tool_allowed(
    tool_name: &str,
    approved_permissions: &[String],
    target_path: Option<&str>,
    worktree_path: Option<&str>,
) -> Option<ForbiddenToolViolation> {
    let has = |p: &str| approved_permissions.iter().any(|s| s == p);

    match tool_name {
        // ── Network tools ────────────────────────────────────────────────────
        "http_get" | "http_post" | "web_fetch" | "curl" | "wget" if !has("network") => {
            return Some(ForbiddenToolViolation {
                tool_name: tool_name.to_string(),
                reason: format!(
                    "`{}` requires network access, which has not been granted",
                    tool_name
                ),
                permission_needed: "network".to_string(),
            });
        }
        "http_get" | "http_post" | "web_fetch" | "curl" | "wget" => {}

        // ── Shell execution ──────────────────────────────────────────────────
        "bash" | "shell" | "exec" | "run_command" | "execute" if !has("shell_exec") => {
            return Some(ForbiddenToolViolation {
                tool_name: tool_name.to_string(),
                reason: format!(
                    "`{}` requires shell execution permission, which has not been granted",
                    tool_name
                ),
                permission_needed: "shell_exec".to_string(),
            });
        }
        "bash" | "shell" | "exec" | "run_command" | "execute" => {}

        // ── Git write operations ─────────────────────────────────────────────
        "git_push" | "git_force_push" if !has("git") => {
            return Some(ForbiddenToolViolation {
                tool_name: tool_name.to_string(),
                reason: format!(
                    "`{}` requires git push permission, which has not been granted",
                    tool_name
                ),
                permission_needed: "git".to_string(),
            });
        }
        "git_push" | "git_force_push" => {}

        // ── Write tools — check worktree boundary ────────────────────────────
        "write_file" | "edit_file" | "create_file" | "delete_file" | "move_file" => {
            if let (Some(target), Some(worktree)) = (target_path, worktree_path) {
                if !is_within_worktree(target, worktree) {
                    return Some(ForbiddenToolViolation {
                        tool_name: tool_name.to_string(),
                        reason: format!(
                            "`{}` targets `{}`, which is outside the worktree `{}`",
                            tool_name, target, worktree
                        ),
                        permission_needed: "write_outside_worktree".to_string(),
                    });
                }
            }
        }

        // All other tools are allowed by default.
        _ => {}
    }

    None
}

// ─── Path checking ────────────────────────────────────────────────────────────

/// Returns `true` if `target` is under `worktree` (inclusive).
///
/// Canonicalizes both paths first (resolving symlinks and `..` components) to
/// prevent traversal attacks via symlinks, `/worktree/../secret`, or
/// `/worktree_extra/file` falsely matching `/worktree`.
/// Falls back to lexical normalization if the filesystem path does not exist.
fn is_within_worktree(target: &str, worktree: &str) -> bool {
    let wt = std::path::Path::new(worktree);
    let t = std::path::Path::new(target);
    let base = wt.canonicalize().unwrap_or_else(|_| normalize_path(wt));
    let resolved = t.canonicalize().unwrap_or_else(|_| normalize_path(t));
    resolved.starts_with(&base)
}

/// Normalize a path by resolving `.` and `..` components without filesystem
/// access.  Excess `..` at the root are silently dropped (same as `realpath`).
fn normalize_path(path: &std::path::Path) -> std::path::PathBuf {
    use std::path::Component;
    let mut parts: Vec<std::ffi::OsString> = Vec::new();
    for component in path.components() {
        match component {
            Component::ParentDir => {
                parts.pop();
            }
            Component::CurDir => {}
            other => parts.push(other.as_os_str().to_os_string()),
        }
    }
    parts.iter().collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn perms(list: &[&str]) -> Vec<String> {
        list.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn network_without_permission_blocked() {
        let result = check_tool_allowed("http_get", &perms(&[]), None, None);
        assert!(result.is_some());
        assert_eq!(result.unwrap().permission_needed, "network");
    }

    #[test]
    fn network_with_permission_allowed() {
        let result = check_tool_allowed("http_get", &perms(&["network"]), None, None);
        assert!(result.is_none());
    }

    #[test]
    fn shell_without_permission_blocked() {
        let result = check_tool_allowed("bash", &perms(&[]), None, None);
        assert!(result.is_some());
        assert_eq!(result.unwrap().permission_needed, "shell_exec");
    }

    #[test]
    fn write_outside_worktree_blocked() {
        let result = check_tool_allowed(
            "write_file",
            &perms(&["write"]),
            Some("/etc/passwd"),
            Some("/Users/user/project"),
        );
        assert!(result.is_some());
        assert!(result.unwrap().reason.contains("outside the worktree"));
    }

    #[test]
    fn write_inside_worktree_allowed() {
        let result = check_tool_allowed(
            "write_file",
            &perms(&["write"]),
            Some("/Users/user/project/src/main.rs"),
            Some("/Users/user/project"),
        );
        assert!(result.is_none());
    }

    #[test]
    fn git_push_without_permission_blocked() {
        let result = check_tool_allowed("git_push", &perms(&[]), None, None);
        assert!(result.is_some());
        assert_eq!(result.unwrap().permission_needed, "git");
    }

    #[test]
    fn unknown_tool_always_allowed() {
        let result = check_tool_allowed("read_file", &perms(&[]), None, None);
        assert!(result.is_none());
    }
}
