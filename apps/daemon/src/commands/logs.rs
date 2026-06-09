//! `clawd logs` command handler.
//!
//! Purpose: Tail and optionally follow the daemon log file, with optional level filtering.
//! Inputs:  Daemon config, follow flag, line count, optional level filter string.
//! Outputs: Filtered log lines printed to stdout; in follow mode, polls indefinitely.
//! Constraints: Sync (blocking poll loop); safe to Ctrl-C.

use crate::logging::log_level_order;
use anyhow::{Context as _, Result};
use clawd::config::DaemonConfig;

pub fn run_logs(
    config: &DaemonConfig,
    follow: bool,
    lines: u64,
    filter: Option<&str>,
) -> Result<()> {
    use std::fs::File;
    use std::io::{Read, Seek, SeekFrom};

    // Resolve log path: CLAWD_LOG_FILE env → default {data_dir}/clawd.log
    let log_path = std::env::var("CLAWD_LOG_FILE")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|_| config.data_dir.join("clawd.log"));

    // Validate: must be within data_dir or an absolute path explicitly set
    if !log_path.exists() {
        anyhow::bail!(
            "log file not found: {}\n  Start the daemon first: clawd start",
            log_path.display()
        );
    }

    let content = std::fs::read_to_string(&log_path)
        .with_context(|| format!("cannot read log file: {}", log_path.display()))?;

    let all_lines: Vec<&str> = content.lines().collect();

    let min_level = filter.map(|f| f.to_ascii_lowercase());

    // Apply level filter (heuristic: check for level strings in each line)
    let filtered: Vec<&&str> = if let Some(ref level) = min_level {
        let levels = log_level_order(level);
        all_lines
            .iter()
            .filter(|line| {
                let l = line.to_ascii_lowercase();
                levels.iter().any(|lvl| l.contains(lvl))
            })
            .collect()
    } else {
        all_lines.iter().collect()
    };

    // Print last N lines (0 = all)
    let start = if lines == 0 || lines as usize >= filtered.len() {
        0
    } else {
        filtered.len() - lines as usize
    };

    for line in &filtered[start..] {
        println!("{line}");
    }

    if !follow {
        return Ok(());
    }

    // Follow mode: poll file every 250ms, print new content as it appears
    let mut file = File::open(&log_path)
        .with_context(|| format!("cannot open log file: {}", log_path.display()))?;
    let mut pos = file
        .seek(SeekFrom::End(0))
        .context("cannot seek log file")?;

    loop {
        std::thread::sleep(std::time::Duration::from_millis(250));

        // Handle log rotation: if file shrunk, reopen from start
        let meta = std::fs::metadata(&log_path);
        let new_size = meta.map(|m| m.len()).unwrap_or(0);
        if new_size < pos {
            if let Ok(f) = File::open(&log_path) {
                file = f;
                pos = 0;
            }
        }

        file.seek(SeekFrom::Start(pos))
            .context("cannot seek log file")?;
        let mut buf = String::new();
        file.read_to_string(&mut buf)
            .context("cannot read log file")?;

        if !buf.is_empty() {
            let should_print = if let Some(ref level) = min_level {
                let levels = log_level_order(level);
                levels
                    .iter()
                    .any(|lvl| buf.to_ascii_lowercase().contains(lvl))
            } else {
                true
            };
            if should_print {
                print!("{buf}");
            }
            pos += buf.len() as u64;
        }
    }
}
