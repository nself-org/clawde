// SPDX-License-Identifier: MIT
//! Version bump detection — D64.T16.
//!
//! Polls manifest files (Cargo.toml, package.json, pubspec.yaml) in watched repos
//! and broadcasts `warning.versionBump` when the version field changes during an
//! active session.
//!
//! Design: polling every 5 s avoids modifying the existing notify watcher
//! infrastructure and is sufficient for detecting manual version bumps.

use crate::ipc::event::EventBroadcaster;
use std::{
    collections::HashMap,
    path::{Path, PathBuf},
    sync::Arc,
    time::Duration,
};
use tokio::sync::RwLock;
use tracing::debug;

/// Manifest files to watch and their version field parser.
static MANIFEST_NAMES: &[&str] = &["Cargo.toml", "package.json", "pubspec.yaml"];

/// Parse the version string from a manifest file's content.
/// Returns `None` if no version field is found.
fn parse_version(filename: &str, content: &str) -> Option<String> {
    if filename == "Cargo.toml" {
        // Look for `version = "x.y.z"` — first occurrence (workspace or package)
        for line in content.lines() {
            let trimmed = line.trim();
            if let Some(rest) = trimmed.strip_prefix("version") {
                let rest = rest.trim_start().strip_prefix('=')?.trim_start();
                let rest = rest.strip_prefix('"')?;
                let version = rest.split('"').next()?;
                if !version.is_empty() {
                    return Some(version.to_string());
                }
            }
        }
        None
    } else if filename == "package.json" {
        // Simple grep for `"version": "x.y.z"`
        for line in content.lines() {
            let trimmed = line.trim();
            if let Some(rest) = trimmed.strip_prefix("\"version\"") {
                let rest = rest.trim_start().strip_prefix(':')?.trim_start();
                let rest = rest.strip_prefix('"')?;
                let version = rest.split('"').next()?;
                if !version.is_empty() {
                    return Some(version.to_string());
                }
            }
        }
        None
    } else if filename == "pubspec.yaml" {
        // `version: 1.0.0+1`
        for line in content.lines() {
            let trimmed = line.trim();
            if let Some(rest) = trimmed.strip_prefix("version:") {
                let version = rest.split_whitespace().next()?;
                if !version.is_empty() {
                    return Some(version.to_string());
                }
            }
        }
        None
    } else {
        None
    }
}

struct WatchedFile {
    path: PathBuf,
    last_version: Option<String>,
}

struct RepoEntry {
    /// Manifest files being watched (up to 3 per repo)
    files: Vec<WatchedFile>,
}

/// Background task that polls manifest files for version changes.
pub struct VersionWatcher {
    repos: Arc<RwLock<HashMap<String, RepoEntry>>>,
    broadcaster: Arc<EventBroadcaster>,
}

impl VersionWatcher {
    pub fn new(broadcaster: Arc<EventBroadcaster>) -> Self {
        Self {
            repos: Arc::new(RwLock::new(HashMap::new())),
            broadcaster,
        }
    }

    /// Add a project path to version monitoring.
    /// Idempotent — calling twice with the same path has no effect.
    pub async fn watch(&self, project_path: &Path) {
        let key = project_path.to_string_lossy().to_string();
        {
            if self.repos.read().await.contains_key(&key) {
                return;
            }
        }

        let mut files = Vec::new();
        for name in MANIFEST_NAMES {
            let file_path = project_path.join(name);
            if file_path.exists() {
                let version = tokio::fs::read_to_string(&file_path)
                    .await
                    .ok()
                    .and_then(|c| parse_version(name, &c));
                files.push(WatchedFile {
                    path: file_path,
                    last_version: version,
                });
            }
        }

        if !files.is_empty() {
            self.repos.write().await.insert(key, RepoEntry { files });
        }
    }

    /// Remove a project path from version monitoring.
    pub async fn unwatch(&self, project_path: &Path) {
        let key = project_path.to_string_lossy().to_string();
        self.repos.write().await.remove(&key);
    }

    /// Spawn the background polling loop.
    /// Returns the `JoinHandle` — drop or abort to stop.
    pub fn spawn(self: Arc<Self>) -> tokio::task::JoinHandle<()> {
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);

            loop {
                interval.tick().await;
                self.poll_once().await;
            }
        })
    }

    /// Single polling pass — reads all watched files and fires events on change.
    async fn poll_once(&self) {
        let mut repos = self.repos.write().await;

        for entry in repos.values_mut() {
            for file in entry.files.iter_mut() {
                let filename = match file.path.file_name() {
                    Some(n) => n.to_string_lossy().to_string(),
                    None => continue,
                };

                let content = match tokio::fs::read_to_string(&file.path).await {
                    Ok(c) => c,
                    Err(_) => continue,
                };

                let new_version = parse_version(&filename, &content);

                match (&file.last_version, &new_version) {
                    (Some(old), Some(new)) if old != new => {
                        debug!(
                            file = %file.path.display(),
                            old = %old,
                            new = %new,
                            "version bump detected"
                        );
                        self.broadcaster.broadcast(
                            "warning.versionBump",
                            serde_json::json!({
                                "file": file.path.to_string_lossy(),
                                "oldVersion": old,
                                "newVersion": new,
                            }),
                        );
                        file.last_version = new_version;
                    }
                    (None, Some(_)) => {
                        // File gained a version field — update cache, don't fire event
                        file.last_version = new_version;
                    }
                    _ => {}
                }
            }
        }
    }
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_cargo_toml_version() {
        let content = r#"
[package]
name = "my-crate"
version = "1.2.3"
edition = "2021"
"#;
        assert_eq!(
            parse_version("Cargo.toml", content),
            Some("1.2.3".to_string())
        );
    }

    #[test]
    fn test_parse_package_json_version() {
        let content = r#"{
  "name": "my-app",
  "version": "0.5.0",
  "dependencies": {}
}"#;
        assert_eq!(
            parse_version("package.json", content),
            Some("0.5.0".to_string())
        );
    }

    #[test]
    fn test_parse_pubspec_version() {
        let content = "name: my_app\nversion: 1.0.0+3\n";
        assert_eq!(
            parse_version("pubspec.yaml", content),
            Some("1.0.0+3".to_string())
        );
    }

    #[test]
    fn test_parse_no_version() {
        assert_eq!(
            parse_version("Cargo.toml", "[package]\nname = \"foo\""),
            None
        );
    }

    #[test]
    fn test_parse_unknown_file() {
        assert_eq!(parse_version("other.json", r#"{"version":"1.0"}"#), None);
    }
}
