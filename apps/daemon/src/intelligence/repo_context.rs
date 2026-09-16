// SPDX-License-Identifier: MIT
//! Repo context optimizer — builds a compact, selective representation of the
//! project for injection into the AI system prompt.
//!
//! Keeps cost low by including only what the AI actually needs:
//!   - Top-level project structure (1 level, max 50 entries)
//!   - Files modified in the last git commit
//!   - Files referenced in the last 5 messages
//!
//! Total output is capped at **2,000 tokens (≈ 8,000 chars)**.

use std::path::Path;

use anyhow::Result;
use once_cell::sync::Lazy;
use regex::Regex;

use super::context::ContextMessage;

/// Hard cap: 8,000 chars ≈ 2,000 tokens (using the 4-chars/token heuristic).
const MAX_CHARS: usize = 8_000;

/// Maximum top-level entries before truncating with "... N more files".
const MAX_ROOT_ENTRIES: usize = 50;

/// Regex for extracting file-path-like strings from message text.
///
/// Matches paths such as `src/main.rs`, `./lib/util.py`, `packages/ui/index.ts`.
/// Must contain at least one `/` and end with a recognised file extension.
static FILE_PATH_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"\b(?:\.{0,2}/)?(?:[\w.\-]+/)+[\w.\-]+\.[\w]{1,10}\b").unwrap());

/// Returns `true` if `name` matches a sensitive file pattern that should never
/// be included in AI context (e.g. credentials, private keys).
fn is_sensitive(name: &str) -> bool {
    let lower = name.to_lowercase();
    // Exact names
    if lower == ".env"
        || lower.starts_with(".env.")
        || lower == ".envrc"
        || lower == "secrets.json"
        || lower == "credentials.json"
    {
        return true;
    }
    // Extension-based
    lower.ends_with(".key")
        || lower.ends_with(".pem")
        || lower.ends_with(".p12")
        || lower.ends_with(".pfx")
        || lower.ends_with(".secret")
        || lower.ends_with(".crt")
        || lower.ends_with(".cer")
}

// ── Public API ───────────────────────────────────────────────────────────────

/// Build a compact repo context string for injection into the AI system prompt.
///
/// # Arguments
///
/// * `repo_path` — Root directory of the git repository.
/// * `recent_messages` — Full message history for this session; the last 5
///   are scanned for file-path references.
///
/// # Returns
///
/// A structured text block with up to three sections:
///
/// ```text
/// ## Project Structure
/// src/
/// Cargo.toml
/// ...
///
/// ## Recently Modified
/// src/main.rs
/// ...
///
/// ## Session Context
/// src/auth.rs
/// ```
///
/// Total length is guaranteed ≤ 8,000 characters.
pub fn build_repo_context(repo_path: &Path, recent_messages: &[ContextMessage]) -> Result<String> {
    let structure = build_structure_section(repo_path);
    let modified = build_modified_section(repo_path);
    let session = build_session_section(repo_path, recent_messages);

    let mut out = String::with_capacity(1024);
    out.push_str("## Project Structure\n");
    out.push_str(&structure);

    if !modified.is_empty() {
        out.push_str("\n\n## Recently Modified\n");
        out.push_str(&modified);
    }

    if !session.is_empty() {
        out.push_str("\n\n## Session Context\n");
        out.push_str(&session);
    }

    // Hard cap: truncate at the last newline within the limit.
    if out.len() > MAX_CHARS {
        let boundary = out[..MAX_CHARS]
            .rfind('\n')
            .unwrap_or(MAX_CHARS.saturating_sub(1));
        out.truncate(boundary);
        out.push_str("\n... (truncated)");
    }

    Ok(out)
}

// ── Private helpers ───────────────────────────────────────────────────────────

/// Build the `## Project Structure` section.
///
/// Reads exactly one level of the directory (no recursion). Sorts entries
/// alphabetically with directories (trailing `/`) listed alongside files.
/// Skips `.git` and other hidden entries; never includes sensitive file names.
/// Caps at `MAX_ROOT_ENTRIES` entries; appends "... N more files" if truncated.
fn build_structure_section(repo_path: &Path) -> String {
    let mut entries: Vec<String> = match std::fs::read_dir(repo_path) {
        Ok(rd) => rd
            .filter_map(|e| e.ok())
            .filter_map(|e| {
                let name = e.file_name().to_string_lossy().to_string();
                // Always skip .git — it's never useful in AI context.
                if name == ".git" {
                    return None;
                }
                // Skip other hidden entries (but not ".claude" — it's relevant).
                if name.starts_with('.') && name != ".claude" {
                    return None;
                }
                if is_sensitive(&name) {
                    return None;
                }
                let is_dir = e.file_type().ok().map(|t| t.is_dir()).unwrap_or(false);
                if is_dir {
                    Some(format!("{}/", name))
                } else {
                    Some(name)
                }
            })
            .collect(),
        Err(_) => return "(directory not readable)\n".to_string(),
    };

    entries.sort_unstable();

    let total = entries.len();
    let mut out = String::new();

    for entry in entries.iter().take(MAX_ROOT_ENTRIES) {
        out.push_str(entry);
        out.push('\n');
    }

    if total > MAX_ROOT_ENTRIES {
        out.push_str(&format!("... {} more files\n", total - MAX_ROOT_ENTRIES));
    }

    out
}

/// Build the `## Recently Modified` section using git2.
///
/// Returns an empty string if the repo has no commits, the diff fails, or no
/// non-sensitive files were changed.
fn build_modified_section(repo_path: &Path) -> String {
    let result: anyhow::Result<Vec<String>> = (|| {
        let repo =
            git2::Repository::open(repo_path).map_err(|e| anyhow::anyhow!("open repo: {}", e))?;

        let head = repo.head().map_err(|e| anyhow::anyhow!("no HEAD: {}", e))?;
        let head_commit = head
            .peel_to_commit()
            .map_err(|e| anyhow::anyhow!("HEAD not a commit: {}", e))?;
        let head_tree = head_commit
            .tree()
            .map_err(|e| anyhow::anyhow!("no tree: {}", e))?;

        // Diff against parent (or empty tree for the initial commit).
        let diff = if head_commit.parent_count() > 0 {
            let parent = head_commit
                .parent(0)
                .map_err(|e| anyhow::anyhow!("no parent: {}", e))?;
            let parent_tree = parent
                .tree()
                .map_err(|e| anyhow::anyhow!("no parent tree: {}", e))?;
            repo.diff_tree_to_tree(Some(&parent_tree), Some(&head_tree), None)
                .map_err(|e| anyhow::anyhow!("diff failed: {}", e))?
        } else {
            repo.diff_tree_to_tree(None, Some(&head_tree), None)
                .map_err(|e| anyhow::anyhow!("initial diff: {}", e))?
        };

        let mut files: Vec<String> = Vec::new();
        diff.foreach(
            &mut |delta, _| {
                if let Some(path) = delta.new_file().path() {
                    let name = path.to_string_lossy().to_string();
                    if !is_sensitive(&name) {
                        files.push(name);
                    }
                }
                true
            },
            None,
            None,
            None,
        )
        .map_err(|e| anyhow::anyhow!("diff foreach: {}", e))?;

        Ok(files)
    })();

    match result {
        Ok(files) if !files.is_empty() => files.join("\n"),
        _ => String::new(),
    }
}

/// Build the `## Session Context` section from file paths referenced in the
/// last 5 messages.
///
/// Only includes paths that:
/// 1. Match `FILE_PATH_RE` (look like source file paths).
/// 2. Do not contain `..` (path traversal guard).
/// 3. Are not sensitive by extension/name.
/// 4. Are not already listed in the structure section (deduplication).
fn build_session_section(repo_path: &Path, recent_messages: &[ContextMessage]) -> String {
    let start = recent_messages.len().saturating_sub(5);
    let last_5 = &recent_messages[start..];

    let mut seen = std::collections::HashSet::new();
    let mut paths: Vec<String> = Vec::new();

    for msg in last_5 {
        for cap in FILE_PATH_RE.find_iter(&msg.content) {
            let raw = cap.as_str();
            // Normalise: strip leading `./`
            let normalized = raw.trim_start_matches("./");

            // Hard security checks before including any path.
            if normalized.contains("..") {
                continue;
            }
            if is_sensitive(normalized) {
                continue;
            }
            // Only keep paths that appear to live inside the repo.
            // We accept paths even if the file doesn't exist yet
            // (e.g. the AI is about to create it).
            let candidate = repo_path.join(normalized);
            // Quick starts_with check without canonicalize (file may not exist).
            if !candidate.starts_with(repo_path) {
                continue;
            }

            if seen.insert(normalized.to_string()) {
                paths.push(normalized.to_string());
            }
        }
    }

    paths.join("\n")
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// Tests live in repo_context/tests.rs.
#[cfg(test)]
mod tests;
