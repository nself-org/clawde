//! Behavioural tests for per-session git worktree management.
//!
//! These drive a **real** git repository in a temp dir rather than mocking the
//! command, because the contract here is precisely "what does git do" — a mock
//! would assert my assumptions about git instead of git's behaviour.
//!
//! The fallback paths matter as much as the happy path: `try_create` returning
//! `None` is how a non-git directory degrades to the main repo instead of
//! failing a session, so it is asserted explicitly rather than left implied.

use super::*;

/// Initialise a real git repo with one commit.
///
/// `git worktree add ... HEAD` needs a commit to detach from, so an empty
/// `git init` is not enough.
fn init_repo() -> tempfile::TempDir {
    let dir = tempfile::tempdir().expect("tempdir");
    let p = dir.path();

    let run = |args: &[&str]| {
        let out = std::process::Command::new("git")
            .args(args)
            .current_dir(p)
            .output()
            .expect("run git");
        assert!(
            out.status.success(),
            "git {args:?} failed: {}",
            String::from_utf8_lossy(&out.stderr)
        );
    };

    run(&["init", "--quiet"]);
    run(&["config", "user.email", "test@example.com"]);
    run(&["config", "user.name", "Test"]);
    std::fs::write(p.join("README.md"), "hello\n").expect("write file");
    run(&["add", "README.md"]);
    run(&["commit", "--quiet", "-m", "initial"]);

    dir
}

#[test]
fn worktree_path_is_data_dir_slash_worktrees_slash_session_id() {
    let got = worktree_path(Path::new("/data"), "sess-1");
    assert_eq!(got, PathBuf::from("/data/worktrees/sess-1"));

    // The session id is the final component, so two sessions never collide.
    let a = worktree_path(Path::new("/data"), "a");
    let b = worktree_path(Path::new("/data"), "b");
    assert_ne!(a, b);
}

#[test]
fn effective_repo_path_falls_back_to_the_repo_when_no_worktree_exists() {
    let dir = tempfile::tempdir().expect("tempdir");

    // Nothing was created, so the fallback branch must be taken.
    let got = effective_repo_path(dir.path(), "sess-1", "/original/repo");
    assert_eq!(
        got, "/original/repo",
        "with no worktree on disk the original repo path must be used"
    );
}

#[test]
fn effective_repo_path_prefers_an_existing_worktree() {
    let dir = tempfile::tempdir().expect("tempdir");
    let wt = worktree_path(dir.path(), "sess-1");
    std::fs::create_dir_all(&wt).expect("create worktree dir");

    let got = effective_repo_path(dir.path(), "sess-1", "/original/repo");
    assert_eq!(
        got,
        wt.to_string_lossy(),
        "an existing worktree must win over the original repo path"
    );
    assert_ne!(
        got, "/original/repo",
        "both directions are pinned so a flipped condition cannot survive"
    );
}

#[tokio::test]
async fn try_create_makes_a_real_detached_worktree() {
    let repo = init_repo();
    let data = tempfile::tempdir().expect("tempdir");
    let wt = worktree_path(data.path(), "sess-1");

    let got = try_create(repo.path(), &wt)
        .await
        .expect("worktree creation should succeed in a real repo");

    assert_eq!(got, wt, "the created path must be the one requested");
    assert!(wt.exists(), "the worktree directory must exist on disk");
    assert!(
        wt.join("README.md").exists(),
        "the worktree must be checked out, not just an empty dir"
    );
    // A linked worktree carries a `.git` FILE pointing at the main repo, not a
    // `.git` directory — this is what distinguishes it from a plain copy.
    assert!(
        wt.join(".git").is_file(),
        "a linked worktree must have a .git file"
    );

    // The parent `worktrees/` directory is created on demand.
    assert!(data.path().join("worktrees").is_dir());
}

#[tokio::test]
async fn try_create_returns_none_for_a_directory_that_is_not_a_git_repo() {
    let not_a_repo = tempfile::tempdir().expect("tempdir");
    let data = tempfile::tempdir().expect("tempdir");
    let wt = worktree_path(data.path(), "sess-1");

    // This is the degrade-to-main-repo path: it must be None, not an error and
    // not an accidental Some.
    let got = try_create(not_a_repo.path(), &wt).await;
    assert!(
        got.is_none(),
        "a non-git directory must yield None so the session falls back"
    );
    assert!(
        !wt.exists(),
        "a failed creation must not leave a worktree directory behind"
    );
}

#[tokio::test]
async fn try_remove_deletes_a_worktree_it_created() {
    let repo = init_repo();
    let data = tempfile::tempdir().expect("tempdir");
    let wt = worktree_path(data.path(), "sess-1");

    try_create(repo.path(), &wt).await.expect("create worktree");
    assert!(wt.exists());

    try_remove(repo.path(), &wt).await;
    assert!(
        !wt.exists(),
        "try_remove must delete the worktree directory"
    );

    // git's own bookkeeping must agree — a directory deleted behind git's back
    // would leave a stale entry and break the next `worktree add`.
    let out = std::process::Command::new("git")
        .args(["worktree", "list"])
        .current_dir(repo.path())
        .output()
        .expect("git worktree list");
    let listed = String::from_utf8_lossy(&out.stdout);
    assert!(
        !listed.contains("sess-1"),
        "git must no longer list the removed worktree, got:\n{listed}"
    );
}

#[tokio::test]
async fn try_remove_is_a_no_op_for_a_path_that_does_not_exist() {
    let repo = init_repo();
    let data = tempfile::tempdir().expect("tempdir");
    let wt = worktree_path(data.path(), "never-created");

    // Must return quietly rather than panicking or shelling out pointlessly.
    try_remove(repo.path(), &wt).await;
    assert!(!wt.exists());
}

#[tokio::test]
async fn try_remove_cleans_the_directory_even_when_git_refuses() {
    // A directory that git does not know about: `git worktree remove` fails,
    // and the fallback must still delete it. This is the branch that a mutant
    // dropping the manual cleanup would silently break.
    let repo = init_repo();
    let data = tempfile::tempdir().expect("tempdir");
    let wt = worktree_path(data.path(), "orphan");
    std::fs::create_dir_all(&wt).expect("create dir");
    std::fs::write(wt.join("stray.txt"), "x").expect("write");

    try_remove(repo.path(), &wt).await;

    assert!(
        !wt.exists(),
        "an orphaned directory must be removed by the manual cleanup fallback"
    );
}
