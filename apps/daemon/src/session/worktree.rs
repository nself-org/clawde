//! Per-session git worktree management.
//!
//! Each session can optionally run inside a dedicated git worktree, isolating
//! it from the main working tree and other concurrent sessions on the same
//! repo.
//!
//! Worktrees live at `{data_dir}/worktrees/{session_id}/`. Creation and
//! removal are non-fatal — failures fall back to the main repo path.

use std::path::{Path, PathBuf};
use tokio::process::Command;
use tracing::{debug, warn};

/// Compute the canonical worktree path for a session.
pub fn worktree_path(data_dir: &Path, session_id: &str) -> PathBuf {
    data_dir.join("worktrees").join(session_id)
}

/// Create a detached-HEAD git worktree for a session.
///
/// Runs `git worktree add --detach <path> HEAD` inside `repo_path`.
/// Returns the worktree path on success, or `None` if the repo is not a
/// git repo, git is unavailable, or the repo doesn't support worktrees
/// (e.g. bare repos are fine, but shallow clones may fail).
pub async fn try_create(repo_path: &Path, worktree_path: &Path) -> Option<PathBuf> {
    // Ensure the parent directory exists.
    if let Some(parent) = worktree_path.parent() {
        if let Err(e) = tokio::fs::create_dir_all(parent).await {
            warn!(err = %e, "failed to create worktrees dir — skipping worktree");
            return None;
        }
    }

    let out = Command::new("git")
        .args([
            "worktree",
            "add",
            "--detach",
            &worktree_path.to_string_lossy(),
            "HEAD",
        ])
        .current_dir(repo_path)
        .output()
        .await;

    match out {
        Ok(o) if o.status.success() => {
            debug!(path = %worktree_path.display(), "git worktree created");
            Some(worktree_path.to_path_buf())
        }
        Ok(o) => {
            let stderr = String::from_utf8_lossy(&o.stderr);
            warn!(
                repo = %repo_path.display(),
                err = %stderr.trim(),
                "git worktree add failed — falling back to main repo"
            );
            None
        }
        Err(e) => {
            warn!(err = %e, "failed to run git worktree add — falling back to main repo");
            None
        }
    }
}

/// Remove a session worktree.
///
/// Runs `git worktree remove --force <path>` inside `repo_path`, then
/// deletes the directory. Both steps are best-effort — errors are logged
/// but not propagated.
pub async fn try_remove(repo_path: &Path, worktree_path: &Path) {
    if !worktree_path.exists() {
        return;
    }

    let out = Command::new("git")
        .args([
            "worktree",
            "remove",
            "--force",
            &worktree_path.to_string_lossy(),
        ])
        .current_dir(repo_path)
        .output()
        .await;

    match out {
        Ok(o) if o.status.success() => {
            debug!(path = %worktree_path.display(), "git worktree removed");
        }
        Ok(o) => {
            let stderr = String::from_utf8_lossy(&o.stderr);
            warn!(
                path = %worktree_path.display(),
                err = %stderr.trim(),
                "git worktree remove failed — cleaning up directory manually"
            );
            let _ = tokio::fs::remove_dir_all(worktree_path).await;
        }
        Err(e) => {
            warn!(err = %e, "failed to run git worktree remove — cleaning up manually");
            let _ = tokio::fs::remove_dir_all(worktree_path).await;
        }
    }
}

/// Returns the effective repo path to use for a runner:
/// the worktree if it exists, otherwise the original repo path.
pub fn effective_repo_path(data_dir: &Path, session_id: &str, repo_path: &str) -> String {
    let wt = worktree_path(data_dir, session_id);
    if wt.exists() {
        wt.to_string_lossy().into_owned()
    } else {
        repo_path.to_string()
    }
}

// Kept in its own file so the tests do not need to be read alongside the
// implementation.
#[cfg(test)]
mod tests;
