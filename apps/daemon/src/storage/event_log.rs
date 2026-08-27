use anyhow::Result;
use chrono::Utc;
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::path::{Path, PathBuf};
use tokio::{fs::OpenOptions, io::AsyncWriteExt, sync::Mutex};

/// Maximum audit log file size before rotation (50 MB).
const ROTATE_BYTES: u64 = 50 * 1024 * 1024;

// ─── Entry ────────────────────────────────────────────────────────────────────

/// One structured JSON line written to the audit log per tool execution.
///
/// All fields are `camelCase` for easy `jq` querying:
/// ```sh
/// jq 'select(.riskLevel == "high")' ~/.local/share/clawd/audit.log
/// jq '[.toolName, .approvalStatus] | @tsv' ~/.local/share/clawd/audit.log
/// ```
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AuditEntry {
    /// RFC-3339 timestamp of when the tool call was processed.
    pub timestamp: String,
    /// Session that triggered the tool call.
    pub session_id: String,
    /// Claude session ID (`--resume` token) or `null` if unknown.
    pub agent_id: Option<String>,
    /// Tool name as reported by the provider.
    pub tool_name: String,
    /// Lowercase hex SHA-256 of the JSON-serialised tool arguments.
    /// Allows correlation without storing potentially sensitive values.
    pub arguments_hash: String,
    /// Risk level: `"low"` | `"medium"` | `"high"` | `"critical"`.
    pub risk_level: String,
    /// Approval outcome: `"auto-approved"` | `"approved"` | `"rejected"`.
    pub approval_status: String,
    /// Elapsed time from tool-call creation to completion, in milliseconds.
    /// `0` when timing is not tracked (e.g. auto-approved in the same event).
    pub duration_ms: u64,
}

impl AuditEntry {
    /// Build an entry, hashing `args_json` with SHA-256.
    pub fn new(
        session_id: impl Into<String>,
        agent_id: Option<String>,
        tool_name: impl Into<String>,
        args_json: &str,
        risk_level: impl Into<String>,
        approval_status: impl Into<String>,
        duration_ms: u64,
    ) -> Self {
        let mut hasher = Sha256::new();
        hasher.update(args_json.as_bytes());
        let hash = format!("{:x}", hasher.finalize());
        Self {
            timestamp: Utc::now().to_rfc3339(),
            session_id: session_id.into(),
            agent_id,
            tool_name: tool_name.into(),
            arguments_hash: hash,
            risk_level: risk_level.into(),
            approval_status: approval_status.into(),
            duration_ms,
        }
    }
}

// ─── Log ──────────────────────────────────────────────────────────────────────

/// Append-only structured audit log for tool calls.
///
/// Writes one JSON line per tool execution to `{data_dir}/audit.log`.
/// Rotates to `audit.log.1` when the active file reaches 50 MB.
/// The file handle is cached for the process lifetime to avoid the overhead
/// of an `open()` syscall on every tool call.
pub struct AuditLog {
    path: PathBuf,
    /// Cached, open file handle; `None` until the first write.
    file: Mutex<Option<tokio::fs::File>>,
}

impl AuditLog {
    pub fn new(data_dir: &Path) -> Self {
        Self {
            path: data_dir.join("audit.log"),
            file: Mutex::new(None),
        }
    }

    /// Append one structured entry to the audit log.
    ///
    /// Opens the file lazily on first call.  Rotates to `audit.log.1` when
    /// the active file reaches 50 MB.  Errors are logged at WARN level and
    /// never propagated — a broken audit log must not interrupt session flow.
    pub async fn append(&self, entry: &AuditEntry) {
        if let Err(e) = self.try_append(entry).await {
            tracing::warn!(err = %e, "audit log write failed");
        }
    }

    async fn try_append(&self, entry: &AuditEntry) -> Result<()> {
        let line = serde_json::to_string(entry)? + "\n";
        let bytes = line.as_bytes();

        let mut guard = self.file.lock().await;

        // Rotation check: if the on-disk file has grown past 50 MB, close the
        // handle and rename the file before opening a fresh one.
        if guard.is_some() {
            if let Ok(meta) = tokio::fs::metadata(&self.path).await {
                if meta.len() >= ROTATE_BYTES {
                    *guard = None; // drop file handle (flushes on drop)
                    let rotated = self.path.with_extension("log.1");
                    let _ = tokio::fs::rename(&self.path, &rotated).await;
                }
            }
        }

        // Open (or re-open after rotation) lazily.
        if guard.is_none() {
            if let Some(parent) = self.path.parent() {
                tokio::fs::create_dir_all(parent).await?;
            }
            let f = OpenOptions::new()
                .create(true)
                .append(true)
                .open(&self.path)
                .await?;
            *guard = Some(f);
        }

        let file = guard.as_mut().unwrap();
        file.write_all(bytes).await?;
        // tokio::fs::File buffers, so write_all alone leaves the entry sitting
        // in userspace memory. append() would return as though it had succeeded
        // while nothing was readable and nothing survived a crash, which for an
        // AUDIT log is the one failure mode that matters. It also made
        // appends_line_to_file flaky: the test reads the file immediately and
        // saw an empty one whenever the buffer had not drained yet.
        //
        // flush() pushes the bytes to the OS, which is what makes them visible
        // to any other reader and what the test actually depends on. Deliberately
        // not sync_data(): an fsync per entry is a durability/throughput
        // trade-off worth making on purpose, not as a side effect of fixing a
        // flake. Entries can still be lost to a machine-level crash.
        file.flush().await?;
        Ok(())
    }
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn arguments_hash_is_sha256_hex() {
        let entry = AuditEntry::new(
            "sess-1",
            None,
            "write_file",
            r#"{"path":"foo.txt","content":"hello"}"#,
            "medium",
            "auto-approved",
            0,
        );
        // SHA-256 should be 64 hex chars
        assert_eq!(entry.arguments_hash.len(), 64);
        assert!(entry.arguments_hash.chars().all(|c| c.is_ascii_hexdigit()));
    }

    #[test]
    fn entry_serialises_to_camel_case() {
        let entry = AuditEntry::new("s", None, "bash", "{}", "high", "approved", 42);
        let json = serde_json::to_string(&entry).unwrap();
        assert!(json.contains("\"toolName\""));
        assert!(json.contains("\"riskLevel\""));
        assert!(json.contains("\"approvalStatus\""));
        assert!(json.contains("\"durationMs\""));
        assert!(json.contains("\"argumentsHash\""));
    }

    #[tokio::test]
    async fn appends_line_to_file() {
        let dir = tempfile::tempdir().unwrap();
        let log = AuditLog::new(dir.path());
        let entry = AuditEntry::new(
            "s1",
            Some("ag1".to_string()),
            "read_file",
            "{}",
            "low",
            "auto-approved",
            0,
        );
        log.append(&entry).await;

        let content = tokio::fs::read_to_string(dir.path().join("audit.log"))
            .await
            .unwrap();
        assert!(content.contains("\"sessionId\":\"s1\""));
        assert!(content.ends_with('\n'));
    }
}
