//! `clawd init` command handler.
//!
//! Purpose: Initialize a `.claude/` AFS directory structure in a project.
//! Inputs:  Target path, optional stack template name, quiet flag.
//! Outputs: Created directories and template files; prints summary unless quiet.
//! Constraints: Async (file I/O via `tokio::fs`); idempotent — skips existing files.

use anyhow::Result;

pub async fn run_init(path: &std::path::Path, template: Option<&str>, quiet: bool) -> Result<()> {
    use clawd::init_templates::{detect_stack, template_for, Stack};
    use tokio::fs;

    // Determine stack — override or auto-detect.
    let stack = if let Some(t) = template {
        t.parse::<Stack>().unwrap_or_else(|_| {
            if !quiet {
                eprintln!("warn: unknown template '{}' — using generic", t);
            }
            Stack::Generic
        })
    } else {
        detect_stack(path)
    };

    let tmpl = template_for(stack);
    let claude_dir = path.join(".claude");
    let mut created: Vec<String> = Vec::new();

    for dir in &[
        ".claude",
        ".claude/rules",
        ".claude/agents",
        ".claude/skills",
        ".claude/memory",
        ".claude/tasks",
        ".claude/planning",
        ".claude/qa",
        ".claude/docs",
        ".claude/archive/inbox",
        ".claude/inbox",
        ".claude/ideas",
        ".claude/temp",
    ] {
        let full = path.join(dir);
        if !full.exists() {
            fs::create_dir_all(&full).await?;
            created.push(dir.to_string());
        }
    }

    let claude_md = claude_dir.join("CLAUDE.md");
    if !claude_md.exists() {
        fs::write(&claude_md, tmpl.claude_md).await?;
        created.push(".claude/CLAUDE.md".to_string());
    }

    let decisions_md = claude_dir.join("memory/decisions.md");
    if !decisions_md.exists() {
        fs::write(&decisions_md, tmpl.decisions_md).await?;
        created.push(".claude/memory/decisions.md".to_string());
    }

    let active_md = claude_dir.join("tasks/active.md");
    if !active_md.exists() {
        fs::write(&active_md, clawd::ipc::handlers::afs::ACTIVE_MD_TEMPLATE).await?;
        created.push(".claude/tasks/active.md".to_string());
    }

    let settings = claude_dir.join("settings.json");
    if !settings.exists() {
        fs::write(&settings, clawd::ipc::handlers::afs::SETTINGS_JSON_TEMPLATE).await?;
        created.push(".claude/settings.json".to_string());
    }

    // Ensure .claude/ is in .gitignore (D64.T22)
    let gitignore = path.join(".gitignore");
    let mut gitignore_updated = false;
    if gitignore.exists() {
        let content = fs::read_to_string(&gitignore).await.unwrap_or_default();
        let missing_entry = !content.contains(".claude/");
        let missing_stack = !tmpl.gitignore_additions.is_empty()
            && !content.contains(tmpl.gitignore_additions.trim());
        if missing_entry || missing_stack {
            let mut updated = content.trim_end().to_string();
            if missing_entry {
                updated.push_str("\n\n# AI agent directories\n.claude/\n");
            }
            if missing_stack && !tmpl.gitignore_additions.trim().is_empty() {
                updated.push_str(tmpl.gitignore_additions);
            }
            fs::write(&gitignore, updated).await?;
            gitignore_updated = true;
        }
    } else {
        let mut content = ".claude/\n".to_string();
        if !tmpl.gitignore_additions.trim().is_empty() {
            content.push_str(tmpl.gitignore_additions);
        }
        fs::write(&gitignore, content).await?;
        created.push(".gitignore".to_string());
        gitignore_updated = true;
    }

    if !quiet {
        if created.is_empty() && !gitignore_updated {
            println!("Already initialized: {}", path.display());
        } else {
            println!("Initialized AFS at: {} (stack: {})", path.display(), stack);
            for item in &created {
                println!("  created   {item}");
            }
            if gitignore_updated {
                println!("  updated   .gitignore");
            }
        }
    }
    Ok(())
}
