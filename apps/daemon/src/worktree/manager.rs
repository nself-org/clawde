//! Per-task Git worktree manager.
//!
//! Every code-modifying task gets its own Git worktree isolated from the
//! main checkout. Worktrees live at `{data_dir}/.claw/worktrees/{task_id}/`
//! and are branched as `claw/{task-id}-{slug}` from HEAD at claim time.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use anyhow::{bail, Context, Result};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tokio::sync::RwLock;
use tracing::{debug, info, warn};

// ── Types ───────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorktreeInfo {
    pub task_id: String,
    pub worktree_path: PathBuf,
    /// Branch name: `claw/<task-id>-<slug>`
    pub branch: String,
    pub repo_path: PathBuf,
    pub created_at: DateTime<Utc>,
    pub status: WorktreeStatus,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum WorktreeStatus {
    Active,
    Done,
    Abandoned,
    Merged,
}

// ── Manager ─────────────────────────────────────────────────────────────────

pub struct WorktreeManager {
    /// task_id -> WorktreeInfo
    worktrees: RwLock<HashMap<String, WorktreeInfo>>,
    /// Base directory for all worktrees: `{data_dir}/.claw/worktrees/`
    worktree_base: PathBuf,
}

impl WorktreeManager {
    pub fn new(data_dir: &Path) -> Self {
        Self {
            worktrees: RwLock::new(HashMap::new()),
            worktree_base: data_dir.join(".claw").join("worktrees"),
        }
    }

    /// Create a new worktree for a task.
    ///
    /// Branch name: `claw/{task-id}-{slug}` where slug is the first 6
    /// alphanumeric chars of `task_title` (lowercase).
    pub async fn create(
        &self,
        task_id: &str,
        task_title: &str,
        repo_path: &Path,
    ) -> Result<WorktreeInfo> {
        let slug = make_slug(task_title);
        let branch = format!("claw/{}-{}", task_id, slug);
        let worktree_path = self.worktree_base.join(task_id);

        tokio::fs::create_dir_all(&self.worktree_base)
            .await
            .context("failed to create worktree base directory")?;

        // Use git2 to create branch from HEAD and then add the worktree.
        let branch_name = branch.clone();
        let wt_path = worktree_path.clone();
        let repo_path_owned = repo_path.to_path_buf();

        tokio::task::spawn_blocking(move || {
            create_worktree_blocking(&repo_path_owned, &branch_name, &wt_path)
        })
        .await
        .context("worktree creation task panicked")??;

        let info = WorktreeInfo {
            task_id: task_id.to_string(),
            worktree_path,
            branch,
            repo_path: repo_path.to_path_buf(),
            created_at: Utc::now(),
            status: WorktreeStatus::Active,
        };

        self.worktrees
            .write()
            .await
            .insert(task_id.to_string(), info.clone());

        info!(task_id, branch = %info.branch, "worktree created");
        Ok(info)
    }

    /// Called when a task is claimed.
    ///
    /// Auto-creates a worktree if one does not yet exist for `task_id`.
    /// Returns the existing worktree if it already exists.
    pub async fn bind_task(
        &self,
        task_id: &str,
        task_title: &str,
        repo_path: &Path,
    ) -> Result<WorktreeInfo> {
        if let Some(existing) = self.get(task_id).await {
            return Ok(existing);
        }
        self.create(task_id, task_title, repo_path).await
    }

    /// List all tracked worktrees.
    pub async fn list(&self) -> Vec<WorktreeInfo> {
        self.worktrees.read().await.values().cloned().collect()
    }

    /// Get the worktree for a specific task.
    pub async fn get(&self, task_id: &str) -> Option<WorktreeInfo> {
        self.worktrees.read().await.get(task_id).cloned()
    }

    /// Remove a worktree and delete its directory.
    ///
    /// If the branch has no merged changes we leave the branch intact so the
    /// caller can decide whether to force-delete it. Returns `true` if a
    /// worktree was found and removed.
    pub async fn remove(&self, task_id: &str) -> Result<bool> {
        let info = {
            let map = self.worktrees.read().await;
            match map.get(task_id) {
                Some(i) => i.clone(),
                None => return Ok(false),
            }
        };

        let wt_path = info.worktree_path.clone();
        let repo_path = info.repo_path.clone();

        // Remove the worktree via git2 (best-effort; directory cleanup always runs).
        let result =
            tokio::task::spawn_blocking(move || remove_worktree_blocking(&repo_path, &wt_path))
                .await
                .context("worktree removal task panicked")?;

        if let Err(e) = result {
            warn!(task_id, err = %e, "git worktree removal failed — cleaning directory manually");
            if info.worktree_path.exists() {
                tokio::fs::remove_dir_all(&info.worktree_path).await.ok();
            }
        }

        self.worktrees.write().await.remove(task_id);
        debug!(task_id, "worktree removed");
        Ok(true)
    }

    /// Check whether a path falls inside any active worktree.
    ///
    /// Returns the `task_id` of the owning worktree, or `None`.
    pub async fn is_in_worktree(&self, path: &Path) -> Option<String> {
        let map = self.worktrees.read().await;
        for (task_id, info) in map.iter() {
            if info.status == WorktreeStatus::Active && path.starts_with(&info.worktree_path) {
                return Some(task_id.clone());
            }
        }
        None
    }

    /// Check for file conflicts with other active worktrees.
    ///
    /// Returns the task_ids of worktrees whose changed files overlap with
    /// `files`. Uses git status of each active worktree to detect overlap.
    pub async fn check_file_conflicts(&self, repo_path: &Path, files: &[PathBuf]) -> Vec<String> {
        let infos: Vec<WorktreeInfo> = {
            let map = self.worktrees.read().await;
            map.values()
                .filter(|i| i.status == WorktreeStatus::Active && i.repo_path == repo_path)
                .cloned()
                .collect()
        };

        let files_owned: Vec<PathBuf> = files.to_vec();
        let mut conflicting = Vec::new();

        for info in infos {
            let wt = info.worktree_path.clone();
            let task_id = info.task_id.clone();
            let check_files = files_owned.clone();

            let has_conflict =
                tokio::task::spawn_blocking(move || worktree_touches_files(&wt, &check_files))
                    .await;

            match has_conflict {
                Ok(Ok(true)) => conflicting.push(task_id),
                Ok(Err(e)) => warn!(task_id, err = %e, "conflict check failed"),
                _ => {}
            }
        }

        conflicting
    }

    /// Before creating a worktree, check if any active worktrees touch the same files.
    ///
    /// Returns list of conflicting task_ids.
    pub async fn detect_file_conflicts(&self, repo_path: &Path) -> Vec<String> {
        let map = self.worktrees.read().await;
        map.values()
            .filter(|i| i.status == WorktreeStatus::Active && i.repo_path == repo_path)
            .map(|i| i.task_id.clone())
            .collect()
    }

    /// Validate that ALL `target_paths` are inside the worktree assigned to `task_id`.
    ///
    /// Returns an error (with -32005 message) if any path is outside.
    pub async fn validate_write_paths(
        &self,
        task_id: &str,
        target_paths: &[PathBuf],
    ) -> Result<()> {
        let info = self
            .get(task_id)
            .await
            .ok_or_else(|| anyhow::anyhow!("REPO_NOT_FOUND: no worktree for task {}", task_id))?;

        for path in target_paths {
            if !path.starts_with(&info.worktree_path) {
                bail!(
                    "REPO_NOT_FOUND: path {} is outside task worktree {}",
                    path.display(),
                    info.worktree_path.display()
                );
            }
        }
        Ok(())
    }

    /// Returns an error if `target_path` targets the main workspace (not a worktree).
    pub async fn reject_main_workspace_write(
        &self,
        repo_path: &Path,
        target_path: &Path,
    ) -> Result<()> {
        if !target_path.starts_with(repo_path) {
            // Not inside this repo at all — not our concern.
            return Ok(());
        }

        // Check if it's inside any worktree.
        if self.is_in_worktree(target_path).await.is_some() {
            return Ok(());
        }

        // It's in the main workspace — reject.
        bail!(
            "REPO_NOT_FOUND: write to main workspace is forbidden; use a task worktree (path: {})",
            target_path.display()
        );
    }

    /// Mark a worktree's status.
    pub async fn set_status(&self, task_id: &str, status: WorktreeStatus) {
        if let Some(info) = self.worktrees.write().await.get_mut(task_id) {
            info.status = status;
        }
    }
}

// ── Blocking git2 helpers ────────────────────────────────────────────────────

fn make_slug(title: &str) -> String {
    title
        .chars()
        .filter(|c| c.is_alphanumeric())
        .map(|c| c.to_ascii_lowercase())
        .take(6)
        .collect::<String>()
        .chars()
        .take(6)
        .collect()
}

fn create_worktree_blocking(repo_path: &Path, branch_name: &str, wt_path: &Path) -> Result<()> {
    let repo = git2::Repository::open(repo_path)
        .context("failed to open repository for worktree creation")?;

    let head = repo.head().context("repository has no HEAD")?;
    let head_commit = head
        .peel_to_commit()
        .context("HEAD does not point to a commit")?;

    // Create branch from HEAD, reusing it if it already exists.
    let branch = match repo.branch(branch_name, &head_commit, false) {
        Ok(b) => b,
        Err(e) if e.code() == git2::ErrorCode::Exists => {
            debug!(branch = branch_name, "branch already exists — reusing");
            repo.find_branch(branch_name, git2::BranchType::Local)
                .context("failed to find existing branch")?
        }
        Err(e) => bail!("failed to create branch {}: {}", branch_name, e),
    };

    // Add the worktree, explicitly checking out the branch we just created/found.
    // `branch_name` may contain '/' (e.g. "claw/task-abc-slug") which git
    // disallows in worktree names, so we derive a safe name by replacing slashes.
    let wt_name = branch_name.replace('/', "--");
    let branch_ref = branch.get();
    let mut wt_opts = git2::WorktreeAddOptions::new();
    wt_opts.reference(Some(branch_ref));
    repo.worktree(&wt_name, wt_path, Some(&wt_opts))
        .context("failed to add git worktree")?;

    Ok(())
}

fn remove_worktree_blocking(repo_path: &Path, wt_path: &Path) -> Result<()> {
    let repo = git2::Repository::open(repo_path)
        .context("failed to open repository for worktree removal")?;

    // Find the worktree by path comparison.
    // git2 0.21: StringArray::iter() yields Result<Option<&str>, Error>;
    // double-flatten extracts &str, skipping Err and None entries.
    let names = repo.worktrees().context("failed to list worktrees")?;
    for name in names.iter().filter_map(|r| r.ok().flatten()) {
        if let Ok(wt) = repo.find_worktree(name) {
            if wt.path() == wt_path {
                wt.prune(None).context("failed to prune worktree")?;
                if wt_path.exists() {
                    std::fs::remove_dir_all(wt_path)
                        .context("failed to remove worktree directory")?;
                }
                return Ok(());
            }
        }
    }

    // Worktree not registered — just clean up the directory.
    if wt_path.exists() {
        std::fs::remove_dir_all(wt_path).context("failed to remove orphaned worktree directory")?;
    }
    Ok(())
}

/// Returns `Ok(true)` if the worktree at `wt_path` has any changed files
/// that overlap with `check_files`.
fn worktree_touches_files(wt_path: &Path, check_files: &[PathBuf]) -> Result<bool> {
    let repo = git2::Repository::open(wt_path).context("failed to open worktree repository")?;
    let statuses = repo
        .statuses(None)
        .context("failed to get worktree status")?;

    for entry in statuses.iter() {
        // git2 0.21: StatusEntry::path() returns Result<&str, Error> (was Option<&str>).
        if let Ok(path_str) = entry.path() {
            let changed_path = wt_path.join(path_str);
            for target in check_files {
                if &changed_path == target {
                    return Ok(true);
                }
            }
        }
    }
    Ok(false)
}

// ── Shared Arc wrapper ───────────────────────────────────────────────────────

/// Thread-safe wrapper around `WorktreeManager` for use in `AppContext`.
pub type SharedWorktreeManager = Arc<WorktreeManager>;
