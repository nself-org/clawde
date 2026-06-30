//! Merge discipline for task worktrees.
//!
//! Tasks are never auto-merged. A reviewer must first call `stage_for_merge`
//! to inspect the diff, then `merge_to_main` (only after QA + approval) to
//! integrate the changes into the main branch.

use anyhow::{bail, Context, Result};
use tracing::info;

use super::manager::{WorktreeManager, WorktreeStatus};

/// Stage worktree changes as a merge-ready diff string (not auto-merged).
///
/// Returns the unified diff of all changes in the worktree compared to the
/// main branch, as a human/machine-readable string suitable for review.
pub async fn stage_for_merge(manager: &WorktreeManager, task_id: &str) -> Result<String> {
    let info = manager
        .get(task_id)
        .await
        .ok_or_else(|| anyhow::anyhow!("no worktree found for task {}", task_id))?;

    let wt_path = info.worktree_path.clone();
    let branch = info.branch.clone();

    let diff_text = tokio::task::spawn_blocking(move || diff_against_main(&wt_path, &branch))
        .await
        .context("diff task panicked")??;

    Ok(diff_text)
}

/// Merge the task worktree into the main branch after QA + reviewer approval.
///
/// Requires that the task status is `Done` (validated externally before call).
/// Performs a fast-forward merge if possible; otherwise creates a merge commit.
pub async fn merge_to_main(manager: &WorktreeManager, task_id: &str) -> Result<()> {
    let info = manager
        .get(task_id)
        .await
        .ok_or_else(|| anyhow::anyhow!("no worktree found for task {}", task_id))?;

    if info.status != WorktreeStatus::Done {
        bail!(
            "task {} worktree is not in Done state — cannot merge",
            task_id
        );
    }

    let repo_path = info.repo_path.clone();
    let branch = info.branch.clone();
    let task_id_owned = task_id.to_string();

    tokio::task::spawn_blocking(move || merge_branch_to_main(&repo_path, &branch, &task_id_owned))
        .await
        .context("merge task panicked")??;

    manager.set_status(task_id, WorktreeStatus::Merged).await;
    info!(task_id, branch = %info.branch, "worktree merged to main");

    Ok(())
}

// ── Blocking git2 helpers ────────────────────────────────────────────────────

/// Compute the diff between the worktree's current HEAD and its merge base
/// with the main branch. Returns a unified diff string.
fn diff_against_main(wt_path: &std::path::Path, _branch: &str) -> Result<String> {
    let repo = git2::Repository::open(wt_path).context("failed to open worktree for diff")?;

    let head = repo.head().context("worktree has no HEAD")?;
    let head_commit = head
        .peel_to_commit()
        .context("HEAD does not point to a commit")?;
    let head_tree = head_commit.tree().context("failed to get HEAD tree")?;

    let parent_count = head_commit.parent_count();
    let diff = if parent_count > 0 {
        let parent = head_commit
            .parent(0)
            .context("failed to get parent commit")?;
        let parent_tree = parent.tree().context("failed to get parent tree")?;
        repo.diff_tree_to_tree(Some(&parent_tree), Some(&head_tree), None)
            .context("failed to compute diff")?
    } else {
        // First commit — diff against empty tree.
        repo.diff_tree_to_tree(None, Some(&head_tree), None)
            .context("failed to compute initial diff")?
    };

    let mut diff_text = String::new();
    diff.print(git2::DiffFormat::Patch, |_delta, _hunk, line| {
        if let Ok(s) = std::str::from_utf8(line.content()) {
            diff_text.push_str(s);
        }
        true
    })
    .context("failed to format diff")?;

    if diff_text.is_empty() {
        diff_text = "(no changes)".to_string();
    }

    Ok(diff_text)
}

/// Merge `branch` into the current HEAD of `repo_path`.
///
/// Strategy:
/// 1. If the branch is a direct descendant of HEAD, fast-forward.
/// 2. Otherwise, create a merge commit.
fn merge_branch_to_main(repo_path: &std::path::Path, branch: &str, task_id: &str) -> Result<()> {
    let repo =
        git2::Repository::open(repo_path).context("failed to open main repository for merge")?;

    // Resolve the task branch to an annotated commit.
    let branch_ref = repo
        .find_branch(branch, git2::BranchType::Local)
        .with_context(|| format!("branch {} not found", branch))?;
    // git2 0.21: Reference::name() returns Result<&str, Error> (was Option<&str>).
    let branch_ref_name = branch_ref
        .get()
        .name()
        .map_err(|e| anyhow::anyhow!("branch ref has no name: {}", e))?
        .to_string();

    let annotated = repo
        .reference_to_annotated_commit(
            &repo
                .find_reference(&branch_ref_name)
                .context("failed to find branch reference")?,
        )
        .context("failed to create annotated commit")?;

    let analysis = repo
        .merge_analysis(&[&annotated])
        .context("failed to analyse merge")?;

    if analysis.0.is_up_to_date() {
        info!(branch, "merge: already up to date");
        return Ok(());
    }

    if analysis.0.is_fast_forward() {
        // Fast-forward: update HEAD ref directly.
        let mut head_ref = repo.head().context("failed to get HEAD ref")?;
        let target_oid = annotated.id();
        head_ref
            .set_target(target_oid, &format!("fast-forward merge of {}", branch))
            .context("failed to fast-forward HEAD")?;
        repo.checkout_head(Some(git2::build::CheckoutBuilder::default().force()))
            .context("failed to checkout after fast-forward")?;
    } else {
        // Normal merge — create merge commit.
        repo.merge(&[&annotated], None, None)
            .context("failed to perform merge")?;

        // Check for conflicts.
        let index = repo.index().context("failed to get index after merge")?;
        if index.has_conflicts() {
            bail!("merge of {} has conflicts — resolve manually", branch);
        }

        // Build the merge commit.
        let sig = repo.signature().context("failed to get git signature")?;
        let tree_oid = {
            let mut idx = repo.index().context("failed to get index")?;
            idx.write_tree().context("failed to write tree")?
        };
        let tree = repo.find_tree(tree_oid).context("failed to find tree")?;
        let head_commit = repo
            .head()
            .context("failed to get HEAD")?
            .peel_to_commit()
            .context("HEAD is not a commit")?;
        let branch_commit = repo
            .find_commit(annotated.id())
            .context("failed to find branch commit")?;

        repo.commit(
            Some("HEAD"),
            &sig,
            &sig,
            &format!("Merge task {} ({})", task_id, branch),
            &tree,
            &[&head_commit, &branch_commit],
        )
        .context("failed to create merge commit")?;

        repo.cleanup_state()
            .context("failed to cleanup merge state")?;
    }

    Ok(())
}
