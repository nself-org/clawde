//! Tests for the repo-context builder.
//!
//! The first group is written against the surviving-mutant list from the
//! mutation gate. `build_modified_section` had no coverage at all — every one
//! of its mutants survived — so it gets real git fixtures here rather than a
//! mock.

use super::*;

/// Initialise a git repo in a fresh temp dir.
fn init_repo() -> (TempDir, git2::Repository) {
    let dir = TempDir::new().unwrap();
    let repo = git2::Repository::init(dir.path()).unwrap();
    (dir, repo)
}

/// Stage everything in the work tree and commit it, chaining onto HEAD when
/// one exists. Returns nothing — the tests assert on the section text.
fn commit_all(repo: &git2::Repository, message: &str) {
    let mut index = repo.index().unwrap();
    index
        .add_all(["*"].iter(), git2::IndexAddOption::DEFAULT, None)
        .unwrap();
    index.write().unwrap();
    let tree = repo.find_tree(index.write_tree().unwrap()).unwrap();
    let sig = git2::Signature::now("Test", "test@example.com").unwrap();

    let head = repo.head().ok().and_then(|h| h.peel_to_commit().ok());
    let parents: Vec<&git2::Commit> = head.iter().collect();
    repo.commit(Some("HEAD"), &sig, &sig, message, &tree, &parents)
        .unwrap();
}

fn write(dir: &TempDir, name: &str, body: &str) {
    std::fs::write(dir.path().join(name), body).unwrap();
}

// ─── is_sensitive ────────────────────────────────────────────────────────────

/// Kills the `||` -> `&&` mutation pairing the `secrets.json` and
/// `credentials.json` exact-name checks. No single name can equal both, so the
/// pairing makes each unreachable and both files start leaking into AI context.
#[test]
fn both_exact_name_secrets_are_matched_independently() {
    assert!(is_sensitive("secrets.json"));
    assert!(is_sensitive("credentials.json"));
    // The negative direction, so the checks cannot be met by matching all names.
    assert!(!is_sensitive("settings.json"));
}

// ─── build_modified_section ──────────────────────────────────────────────────

/// Kills the whole-body replacements (`String::new()`, `"xyzzy".into()`), the
/// `>= 0` and `== 0` mutations of the `parent_count() > 0` branch, and the
/// `delete !` / `false` mutations of the `!files.is_empty()` match guard.
///
/// On an initial commit `parent_count()` is 0, so the real code diffs against
/// the empty tree and every file in the commit is listed. Both `>= 0` and
/// `== 0` send it down the parent branch instead, where `parent(0)` fails and
/// the whole section collapses to "".
#[test]
fn an_initial_commit_is_diffed_against_the_empty_tree() {
    let (dir, repo) = init_repo();
    write(&dir, "a.txt", "one\n");
    write(&dir, "b.txt", "two\n");
    commit_all(&repo, "initial");

    let out = build_modified_section(dir.path());
    let mut lines: Vec<&str> = out.lines().collect();
    lines.sort_unstable();
    assert_eq!(lines, vec!["a.txt", "b.txt"]);
}

/// Kills the `< 0` and `== 0` mutations of `parent_count() > 0`.
///
/// With a parent present the real code diffs the commit against that parent,
/// so only the file that actually changed is listed. Both mutations make the
/// condition false and fall into the empty-tree branch, which reports every
/// file in the tree as recently modified.
#[test]
fn a_later_commit_is_diffed_against_its_parent_only() {
    let (dir, repo) = init_repo();
    write(&dir, "a.txt", "one\n");
    write(&dir, "b.txt", "two\n");
    commit_all(&repo, "initial");

    write(&dir, "b.txt", "two, revised\n");
    commit_all(&repo, "second");

    assert_eq!(build_modified_section(dir.path()), "b.txt");
}

/// Kills the `delete !` mutation on `!is_sensitive(&name)` inside the diff
/// walk — with the `!` dropped the filter inverts and the section lists
/// *only* the secrets.
#[test]
fn sensitive_files_are_never_listed_as_modified() {
    let (dir, repo) = init_repo();
    write(&dir, ".env", "TOKEN=shh\n");
    write(&dir, "app.rs", "fn main() {}\n");
    commit_all(&repo, "initial");

    assert_eq!(build_modified_section(dir.path()), "app.rs");
}

#[test]
fn a_repo_with_no_commits_has_no_modified_section() {
    let (dir, _repo) = init_repo();
    write(&dir, "a.txt", "one\n");
    assert_eq!(build_modified_section(dir.path()), "");
}

#[test]
fn a_directory_that_is_not_a_repo_has_no_modified_section() {
    let dir = TempDir::new().unwrap();
    write(&dir, "a.txt", "one\n");
    assert_eq!(build_modified_section(dir.path()), "");
}

// NOTE — equivalent mutant, deliberately not chased: replacing the
// `Ok(files) if !files.is_empty()` guard with a constant `true`. When `files`
// is empty, `files.join("\n")` is the empty string, which is exactly what the
// `_` arm returns; no input can tell the two apart.

// ─── build_structure_section ─────────────────────────────────────────────────

/// Kills the `!=` -> `==` mutation in `name.starts_with('.') && name !=
/// ".claude"`. The real rule is "skip hidden entries, except .claude"; the
/// mutation inverts it into "skip only .claude", which both hides the one
/// hidden directory that matters and exposes every other dotfile.
#[test]
fn dot_claude_is_the_only_hidden_entry_kept() {
    let dir = TempDir::new().unwrap();
    std::fs::create_dir(dir.path().join(".claude")).unwrap();
    std::fs::create_dir(dir.path().join("src")).unwrap();
    write(&dir, ".hidden", "x");
    write(&dir, "Cargo.toml", "x");

    let out = build_structure_section(dir.path());
    assert!(out.contains(".claude/"), "got: {out:?}");
    assert!(out.contains("src/"));
    assert!(out.contains("Cargo.toml"));
    assert!(!out.contains(".hidden"), "got: {out:?}");
}

/// Kills the `>` -> `>=` mutation of `total > MAX_ROOT_ENTRIES`.
///
/// At exactly the cap there is nothing left over, so the real code adds no
/// trailing line; `>=` appends a nonsensical "... 0 more files".
#[test]
fn exactly_the_entry_cap_adds_no_overflow_line() {
    let dir = TempDir::new().unwrap();
    for i in 0..MAX_ROOT_ENTRIES {
        write(&dir, &format!("f{i:03}.txt"), "x");
    }
    let out = build_structure_section(dir.path());
    assert_eq!(out.lines().count(), MAX_ROOT_ENTRIES);
    assert!(!out.contains("more files"), "got: {out:?}");
}

#[test]
fn one_past_the_entry_cap_reports_the_remainder() {
    let dir = TempDir::new().unwrap();
    for i in 0..MAX_ROOT_ENTRIES + 3 {
        write(&dir, &format!("f{i:03}.txt"), "x");
    }
    let out = build_structure_section(dir.path());
    assert!(out.contains("... 3 more files"), "got: {out:?}");
}

// ─── build_repo_context ──────────────────────────────────────────────────────

/// Kills the `delete !` mutation on `!modified.is_empty()` — with the `!`
/// dropped the section is emitted only when there is nothing to put in it, so
/// the header never appears for a repo that actually has commits.
#[test]
fn the_modified_section_appears_when_there_are_modifications() {
    let (dir, repo) = init_repo();
    write(&dir, "a.txt", "one\n");
    commit_all(&repo, "initial");

    let out = build_repo_context(dir.path(), &[]).unwrap();
    assert!(out.contains("## Recently Modified"), "got: {out:?}");
    assert!(out.contains("a.txt"));
}

#[test]
fn the_modified_section_is_omitted_when_there_are_no_modifications() {
    let dir = TempDir::new().unwrap();
    write(&dir, "a.txt", "one\n");

    let out = build_repo_context(dir.path(), &[]).unwrap();
    assert!(!out.contains("## Recently Modified"), "got: {out:?}");
}

/// Kills the `>` -> `==` mutation of `out.len() > MAX_CHARS`. Overshooting the
/// cap by an arbitrary amount must still truncate; `==` only truncates on an
/// exact hit, so oversized context sails straight through to the model.
#[test]
fn context_well_over_the_cap_is_truncated() {
    let dir = TempDir::new().unwrap();
    // 50 entries (the structure cap) of 200 chars each — far past MAX_CHARS.
    for i in 0..MAX_ROOT_ENTRIES {
        write(&dir, &format!("f{i:03}{}", "x".repeat(196)), "x");
    }
    let out = build_repo_context(dir.path(), &[]).unwrap();
    assert!(out.len() <= MAX_CHARS, "len was {}", out.len());
    assert!(
        out.ends_with("\n... (truncated)"),
        "got tail: {:?}",
        &out[out.len().saturating_sub(40)..]
    );
}

/// Kills the `>` -> `>=` mutation of the same comparison — the only input that
/// separates them is a context of *exactly* MAX_CHARS, which must NOT be
/// truncated.
///
/// The fixture is sized to land on the cap precisely: the output is the
/// 21-byte "## Project Structure\n" header plus one line per file, and the
/// filename lengths below are chosen so those lines total MAX_CHARS - 21. The
/// first assertion checks the fixture actually hit the mark, so if the header
/// or the section layout ever changes this test fails loudly rather than
/// quietly stopping to test anything.
#[test]
fn context_of_exactly_the_cap_is_not_truncated() {
    const HEADER: usize = "## Project Structure\n".len();
    let dir = TempDir::new().unwrap();

    // Each entry contributes name.len() + 1 (the newline).
    let budget = MAX_CHARS - HEADER - MAX_ROOT_ENTRIES;
    let base = budget / MAX_ROOT_ENTRIES;
    let long = budget % MAX_ROOT_ENTRIES; // this many need one extra char
    for i in 0..MAX_ROOT_ENTRIES {
        let len = if i < long { base + 1 } else { base };
        // "fNNN" prefix keeps the names unique; pad the rest to `len`.
        write(&dir, &format!("f{i:03}{}", "x".repeat(len - 4)), "x");
    }

    let out = build_repo_context(dir.path(), &[]).unwrap();
    assert_eq!(out.len(), MAX_CHARS, "fixture did not land on the cap");
    assert!(
        !out.contains("(truncated)"),
        "exactly at the cap must not truncate"
    );
}

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

use std::io::Write as _;
use tempfile::TempDir;

// ── helpers ───────────────────────────────────────────────────────────────

fn make_dir() -> TempDir {
    tempfile::tempdir().expect("tempdir")
}

fn create_file(dir: &TempDir, name: &str) {
    let path = dir.path().join(name);
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent).ok();
    }
    let mut f = std::fs::File::create(&path).expect("create file");
    writeln!(f, "// {}", name).ok();
}

fn msg(content: &str) -> ContextMessage {
    ContextMessage {
        role: "user".to_string(),
        content: content.to_string(),
        pinned: false,
    }
}

// ── structure section tests ───────────────────────────────────────────────

#[test]
fn test_structure_small_project_no_truncation() {
    let dir = make_dir();
    // Create 10 files at root
    for i in 0..10 {
        create_file(&dir, &format!("file{i}.rs"));
    }
    create_file(&dir, "Cargo.toml");

    let result = build_structure_section(dir.path());
    // All 11 files should appear — no truncation message
    assert!(!result.contains("more files"), "should not truncate");
    assert!(result.contains("Cargo.toml\n") || result.contains("file0.rs\n"));
}

#[test]
fn test_structure_large_project_truncated() {
    let dir = make_dir();
    // Create 80 files at root — exceeds MAX_ROOT_ENTRIES (50)
    for i in 0..80 {
        create_file(&dir, &format!("file{i:03}.rs"));
    }

    let result = build_structure_section(dir.path());
    assert!(
        result.contains("more files"),
        "should have truncation note — got:\n{result}"
    );
    // Exactly 50 entries + truncation line
    let file_lines: Vec<&str> = result.lines().filter(|l| l.starts_with("file")).collect();
    assert_eq!(file_lines.len(), 50, "exactly 50 file entries expected");
}

#[test]
fn test_structure_hides_sensitive_files() {
    let dir = make_dir();
    create_file(&dir, "main.rs");
    create_file(&dir, ".env");
    create_file(&dir, "private.key");
    create_file(&dir, "cert.pem");

    let result = build_structure_section(dir.path());
    assert!(result.contains("main.rs"), "main.rs should appear");
    assert!(!result.contains(".env"), ".env must be hidden");
    assert!(!result.contains("private.key"), "*.key must be hidden");
    assert!(!result.contains("cert.pem"), "*.pem must be hidden");
}

#[test]
fn test_structure_hides_dot_git() {
    let dir = make_dir();
    create_file(&dir, "README.md");
    // Create a .git directory
    std::fs::create_dir(dir.path().join(".git")).ok();

    let result = build_structure_section(dir.path());
    assert!(result.contains("README.md"), "README should appear");
    assert!(!result.contains(".git"), ".git must be hidden");
}

// ── session section tests ─────────────────────────────────────────────────

#[test]
fn test_session_file_ref_included() {
    let dir = make_dir();
    create_file(&dir, "src/auth.rs");

    let messages = vec![
        msg("Let's look at src/auth.rs for the auth logic"),
        msg("The handler is in src/auth.rs:42"),
    ];

    let result = build_session_section(dir.path(), &messages);
    assert!(
        result.contains("src/auth.rs"),
        "src/auth.rs should appear in session context — got: {result}"
    );
}

#[test]
fn test_session_deduplicates_refs() {
    let dir = make_dir();
    create_file(&dir, "src/main.rs");

    let messages = vec![
        msg("check src/main.rs"),
        msg("also see src/main.rs"),
        msg("and src/main.rs again"),
    ];

    let result = build_session_section(dir.path(), &messages);
    let count = result.matches("src/main.rs").count();
    assert_eq!(count, 1, "should deduplicate repeated refs");
}

#[test]
fn test_session_no_path_traversal() {
    let dir = make_dir();
    let messages = vec![msg("look at ../../etc/passwd for details")];
    let result = build_session_section(dir.path(), &messages);
    assert!(
        !result.contains("etc/passwd"),
        "traversal path must be rejected"
    );
}

#[test]
fn test_session_sensitive_refs_excluded() {
    let dir = make_dir();
    create_file(&dir, "config/.env");
    create_file(&dir, "keys/server.key");

    let messages = vec![msg("I edited config/.env and keys/server.key today")];
    let result = build_session_section(dir.path(), &messages);
    assert!(!result.contains(".env"), ".env refs must be excluded");
    assert!(
        !result.contains("server.key"),
        "*.key refs must be excluded"
    );
}

#[test]
fn test_session_only_last_5_messages() {
    let dir = make_dir();
    create_file(&dir, "src/old.rs");
    create_file(&dir, "src/new.rs");

    // 6 messages — only last 5 should be scanned.
    // src/old.rs only in message #1 (index 0), src/new.rs in message #6 (index 5).
    let messages = vec![
        msg("see src/old.rs"), // index 0 — NOT in last 5
        msg("nothing here"),
        msg("nothing here"),
        msg("nothing here"),
        msg("nothing here"),
        msg("check src/new.rs"), // index 5 — in last 5
    ];

    let result = build_session_section(dir.path(), &messages);
    assert!(result.contains("src/new.rs"), "src/new.rs should appear");
    assert!(
        !result.contains("src/old.rs"),
        "src/old.rs is outside last 5 messages — should not appear"
    );
}

// ── full build_repo_context tests ─────────────────────────────────────────

#[test]
fn test_full_output_under_max_chars() {
    let dir = make_dir();
    for i in 0..30 {
        create_file(&dir, &format!("src/module{i}.rs"));
    }
    create_file(&dir, "Cargo.toml");
    create_file(&dir, "README.md");

    let messages = vec![msg("let's look at src/module0.rs")];
    let output = build_repo_context(dir.path(), &messages).unwrap();

    assert!(
        output.len() <= MAX_CHARS,
        "output length {} exceeds MAX_CHARS {}",
        output.len(),
        MAX_CHARS
    );
}

#[test]
fn test_full_output_has_sections() {
    let dir = make_dir();
    create_file(&dir, "main.rs");
    create_file(&dir, "lib.rs");

    let output = build_repo_context(dir.path(), &[]).unwrap();
    assert!(
        output.contains("## Project Structure"),
        "missing structure section"
    );
}

#[test]
fn test_full_session_context_section_present() {
    let dir = make_dir();
    create_file(&dir, "src/auth.rs");
    let messages = vec![msg("working on src/auth.rs today")];

    let output = build_repo_context(dir.path(), &messages).unwrap();
    assert!(
        output.contains("## Session Context"),
        "missing session section"
    );
    assert!(output.contains("src/auth.rs"));
}

#[test]
fn test_is_sensitive_patterns() {
    assert!(is_sensitive(".env"));
    assert!(is_sensitive(".env.local"));
    assert!(is_sensitive(".envrc"));
    assert!(is_sensitive("private.key"));
    assert!(is_sensitive("cert.pem"));
    assert!(is_sensitive("bundle.p12"));
    assert!(is_sensitive("bundle.pfx"));
    assert!(is_sensitive("api.secret"));
    assert!(is_sensitive("server.crt"));
    assert!(!is_sensitive("main.rs"));
    assert!(!is_sensitive("Cargo.toml"));
    assert!(!is_sensitive("README.md"));
    assert!(!is_sensitive("env.rs")); // not a dotfile
}

#[test]
fn test_directories_have_trailing_slash() {
    let dir = make_dir();
    std::fs::create_dir(dir.path().join("src")).ok();
    create_file(&dir, "main.rs");

    let result = build_structure_section(dir.path());
    assert!(
        result.contains("src/"),
        "directories should have trailing /"
    );
    assert!(
        result.contains("main.rs"),
        "files should not have trailing /"
    );
}
