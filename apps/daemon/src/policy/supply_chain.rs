//! Supply-chain verification for MCP servers.
//!
//! `SupplyChainPolicy` maintains an allowlist of registered MCP server
//! commands.  The first time a server connects its command fingerprint is
//! recorded.  On subsequent connections the fingerprint must match — a
//! mismatch indicates a potential supply-chain attack (e.g. the MCP server
//! binary was replaced).

use std::collections::HashMap;
use std::path::{Path, PathBuf};

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tracing::{info, warn};

use super::sandbox::PolicyViolation;

// ─── Server fingerprint ───────────────────────────────────────────────────────

/// Recorded fingerprint for a registered MCP server.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServerFingerprint {
    /// The command used to launch the server.
    pub command: String,
    /// Arguments passed to the command.
    pub args: Vec<String>,
    /// SHA-256 of `command + args` (hex encoded).
    pub binary_hash: String,
    /// When this server was first seen.
    pub first_seen: DateTime<Utc>,
    /// When this fingerprint was last verified.
    pub last_seen: DateTime<Utc>,
}

// ─── Allowlist file format ────────────────────────────────────────────────────

#[derive(Debug, Default, Serialize, Deserialize)]
struct AllowlistFile {
    #[serde(default)]
    servers: HashMap<String, ServerFingerprint>,
}

// ─── Supply chain policy ──────────────────────────────────────────────────────

/// Manages the MCP server allowlist stored in `.claw/policies/mcp-allowlist.json`.
pub struct SupplyChainPolicy {
    allowlist_path: PathBuf,
    servers: HashMap<String, ServerFingerprint>,
}

impl SupplyChainPolicy {
    /// An empty policy with no registered servers.  Used in tests.
    pub fn empty() -> Self {
        Self {
            allowlist_path: PathBuf::new(),
            servers: HashMap::new(),
        }
    }

    /// Load allowlist from `mcp-allowlist.json`.  Returns an empty policy on
    /// any I/O or parse error.
    pub fn load(path: &Path) -> Self {
        let content = match std::fs::read_to_string(path) {
            Ok(c) => c,
            Err(e) => {
                warn!(path = %path.display(), err = %e, "mcp-allowlist.json not found — starting empty");
                return Self {
                    allowlist_path: path.to_path_buf(),
                    servers: HashMap::new(),
                };
            }
        };

        let file: AllowlistFile = match serde_json::from_str(&content) {
            Ok(f) => f,
            Err(e) => {
                warn!(err = %e, "mcp-allowlist.json parse error — starting empty");
                return Self {
                    allowlist_path: path.to_path_buf(),
                    servers: HashMap::new(),
                };
            }
        };

        Self {
            allowlist_path: path.to_path_buf(),
            servers: file.servers,
        }
    }

    /// Verify that the given server's command matches what was previously
    /// registered, or register it for the first time.
    ///
    /// Returns `Ok(())` on success or `Err(PolicyViolation)` if the command
    /// hash has changed since the server was last registered.
    pub fn verify_or_register(
        &mut self,
        server_name: &str,
        command: &str,
        args: &[&str],
    ) -> Result<(), PolicyViolation> {
        let hash = Self::compute_command_hash(command, args);
        let now = Utc::now();

        if let Some(existing) = self.servers.get_mut(server_name) {
            if existing.binary_hash != hash {
                return Err(PolicyViolation::SupplyChainMismatch {
                    server: server_name.to_string(),
                    expected_hash: existing.binary_hash.clone(),
                    actual_hash: hash,
                });
            }
            // Hash matches — update last_seen.
            existing.last_seen = now;
            info!(server = server_name, "MCP server fingerprint verified");
        } else {
            // First time seeing this server — register it.
            let fp = ServerFingerprint {
                command: command.to_string(),
                args: args.iter().map(|s| s.to_string()).collect(),
                binary_hash: hash,
                first_seen: now,
                last_seen: now,
            };
            self.servers.insert(server_name.to_string(), fp);
            info!(server = server_name, "MCP server fingerprint registered");

            // Persist to disk (best-effort).
            let _ = self.persist();
        }

        Ok(())
    }

    /// Read-only supply-chain check used on the tool-dispatch path.
    ///
    /// Unlike [`verify_or_register`](Self::verify_or_register) this never
    /// registers a server, never updates timestamps and never writes to disk:
    /// an unregistered server is denied, and a fingerprint mismatch is denied.
    pub fn verify(
        &self,
        server_name: &str,
        command: &str,
        args: &[&str],
    ) -> Result<(), PolicyViolation> {
        let Some(existing) = self.servers.get(server_name) else {
            return Err(PolicyViolation::UnregisteredServer {
                server: server_name.to_string(),
            });
        };
        let hash = Self::compute_command_hash(command, args);
        if existing.binary_hash != hash {
            return Err(PolicyViolation::SupplyChainMismatch {
                server: server_name.to_string(),
                expected_hash: existing.binary_hash.clone(),
                actual_hash: hash,
            });
        }
        Ok(())
    }

    /// Compute a deterministic SHA-256 hash from `command` and `args`.
    ///
    /// Format: `SHA256(command + '\0' + args.join('\0'))` (hex).
    pub fn compute_command_hash(command: &str, args: &[&str]) -> String {
        let mut hasher = Sha256::new();
        hasher.update(command.as_bytes());
        for arg in args {
            hasher.update(b"\x00");
            hasher.update(arg.as_bytes());
        }
        format!("{:x}", hasher.finalize())
    }

    /// Write the current allowlist back to disk.
    fn persist(&self) -> anyhow::Result<()> {
        if self.allowlist_path.as_os_str().is_empty() {
            return Ok(()); // In-memory only (tests).
        }
        let file = AllowlistFile {
            servers: self.servers.clone(),
        };
        let json = serde_json::to_string_pretty(&file)?;
        if let Some(parent) = self.allowlist_path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        std::fs::write(&self.allowlist_path, json)?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn first_registration_succeeds() {
        let mut policy = SupplyChainPolicy::empty();
        policy
            .verify_or_register("my-mcp", "/usr/bin/node", &["server.js"])
            .expect("first registration");
        assert!(policy.servers.contains_key("my-mcp"));
    }

    #[test]
    fn same_command_verifies_ok() {
        let mut policy = SupplyChainPolicy::empty();
        policy
            .verify_or_register("my-mcp", "/usr/bin/node", &["server.js"])
            .expect("first registration");
        policy
            .verify_or_register("my-mcp", "/usr/bin/node", &["server.js"])
            .expect("second verification");
    }

    #[test]
    fn changed_command_returns_violation() {
        let mut policy = SupplyChainPolicy::empty();
        policy
            .verify_or_register("my-mcp", "/usr/bin/node", &["server.js"])
            .expect("first registration");

        let result = policy.verify_or_register("my-mcp", "/usr/local/bin/node", &["server.js"]);
        assert!(result.is_err());
        if let Err(PolicyViolation::SupplyChainMismatch { server, .. }) = result {
            assert_eq!(server, "my-mcp");
        } else {
            panic!("expected SupplyChainMismatch");
        }
    }

    #[test]
    fn compute_hash_is_deterministic() {
        let h1 = SupplyChainPolicy::compute_command_hash("node", &["server.js"]);
        let h2 = SupplyChainPolicy::compute_command_hash("node", &["server.js"]);
        assert_eq!(h1, h2);
    }

    #[test]
    fn compute_hash_differs_for_different_commands() {
        let h1 = SupplyChainPolicy::compute_command_hash("node", &["server.js"]);
        let h2 = SupplyChainPolicy::compute_command_hash("deno", &["server.js"]);
        assert_ne!(h1, h2);
    }

    #[test]
    fn verify_registered_ok() {
        let mut policy = SupplyChainPolicy::empty();
        policy
            .verify_or_register("my-mcp", "/usr/bin/node", &["server.js"])
            .expect("register");
        assert!(policy
            .verify("my-mcp", "/usr/bin/node", &["server.js"])
            .is_ok());
    }

    #[test]
    fn verify_unregistered_denied_and_not_registered() {
        let policy = SupplyChainPolicy::empty();
        let result = policy.verify("ghost", "/usr/bin/node", &["server.js"]);
        assert!(matches!(
            result,
            Err(PolicyViolation::UnregisteredServer { .. })
        ));
        // Read-only: a second call still denies.
        assert!(policy
            .verify("ghost", "/usr/bin/node", &["server.js"])
            .is_err());
    }

    #[test]
    fn verify_mismatch_denied() {
        let mut policy = SupplyChainPolicy::empty();
        policy
            .verify_or_register("my-mcp", "/usr/bin/node", &["server.js"])
            .expect("register");
        let result = policy.verify("my-mcp", "/tmp/evil", &["server.js"]);
        assert!(matches!(
            result,
            Err(PolicyViolation::SupplyChainMismatch { .. })
        ));
    }
}
