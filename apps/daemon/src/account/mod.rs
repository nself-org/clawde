//! Multi-account pool manager.
//!
//! Manages a set of AI provider accounts (Claude Code, Codex, Cursor).
//! Tracks rate-limit state and picks the best available account for a session.
//!
//! Feature gating:
//! - Free tier: manual switch prompt when limit hit (broadcasts `session.accountLimited`)
//! - Personal Remote ($9.99/yr): auto-switch silently (broadcasts `session.accountSwitched`)

use anyhow::{Context, Result};
use chrono::{DateTime, Duration, Utc};
use serde_json::json;
use std::sync::Arc;
use tokio::sync::Mutex;
use tracing::{info, warn};

use crate::ipc::event::EventBroadcaster;
use crate::license::LicenseInfo;
use crate::storage::{AccountRow, Storage};

/// Hint for account selection — prefer a specific provider if possible.
#[derive(Debug, Clone, Default)]
pub struct PickHint {
    pub provider: Option<String>,
}

/// The account pool registry.
#[derive(Clone)]
pub struct AccountRegistry {
    storage: Arc<Storage>,
    broadcaster: Arc<EventBroadcaster>,
    /// Tracks the last-used index in the priority-sorted available account slice.
    round_robin_index: Arc<Mutex<usize>>,
}

impl AccountRegistry {
    pub fn new(storage: Arc<Storage>, broadcaster: Arc<EventBroadcaster>) -> Self {
        Self {
            storage,
            broadcaster,
            round_robin_index: Arc::new(Mutex::new(0)),
        }
    }

    /// Pick the best available account for a new session.
    ///
    /// Selection order:
    /// 1. Provider matches hint (if given)
    /// 2. Not rate-limited (`limited_until` is null or in the past)
    /// 3. Priority-sorted (lowest number = highest priority), then round-robin
    ///    within that sorted set so the same account is not always chosen.
    pub async fn pick_account(&self, hint: &PickHint) -> Result<Option<AccountRow>> {
        let accounts = self.storage.list_accounts().await?;
        let now = Utc::now();

        let mut available: Vec<&AccountRow> = accounts
            .iter()
            .filter(|a| {
                // Skip if currently limited.
                if let Some(ref until) = a.limited_until {
                    if let Ok(dt) = DateTime::parse_from_rfc3339(until) {
                        if now < dt.with_timezone(&Utc) {
                            return false;
                        }
                    }
                }
                // Apply provider hint.
                if let Some(ref provider) = hint.provider {
                    return &a.provider == provider;
                }
                true
            })
            .collect();

        if available.is_empty() {
            return Ok(None);
        }

        // Sort by priority (ascending) for a stable ordering.
        available.sort_by_key(|a| a.priority);

        if available.len() == 1 {
            return Ok(Some(available[0].clone()));
        }

        // Advance the round-robin index and pick the next account in sequence.
        let mut idx = self.round_robin_index.lock().await;
        *idx = (*idx + 1) % available.len();
        Ok(Some(available[*idx].clone()))
    }

    /// Mark an account as rate-limited for `cooldown_minutes`.
    /// Broadcasts the appropriate event based on the license tier.
    pub async fn mark_limited(
        &self,
        account_id: &str,
        session_id: &str,
        cooldown_minutes: i64,
        license: &LicenseInfo,
    ) -> Result<()> {
        let until = (Utc::now() + Duration::minutes(cooldown_minutes)).to_rfc3339();
        self.storage
            .set_account_limited(account_id, Some(&until))
            .await
            .context("failed to mark account as limited")?;

        warn!(account_id, until = %until, "account rate-limited");

        if license.features.auto_switch {
            // Personal Remote+: auto-switch, silent notification
            self.broadcaster.broadcast(
                "session.accountSwitched",
                json!({
                    "sessionId": session_id,
                    "accountId": account_id,
                    "reason": "rate_limited",
                }),
            );
            info!(account_id, session_id, "auto-switched account");
        } else {
            // Free tier: requires manual user action
            self.broadcaster.broadcast(
                "session.accountLimited",
                json!({
                    "sessionId": session_id,
                    "accountId": account_id,
                    "requiresManualSwitch": true,
                    "limitedUntil": until,
                }),
            );
            info!(
                account_id,
                session_id, "account limited — user action required"
            );
        }

        Ok(())
    }

    /// Clear the rate-limit on an account (e.g. after cooldown expires).
    pub async fn clear_limit(&self, account_id: &str) -> Result<()> {
        self.storage.set_account_limited(account_id, None).await
    }

    /// Enforce the max_accounts limit. Returns an error if the limit is
    /// reached and a new account cannot be registered.
    pub async fn check_account_limit(&self, max_accounts: usize) -> Result<()> {
        if max_accounts == 0 {
            return Ok(()); // unlimited
        }
        let accounts = self.storage.list_accounts().await?;
        if accounts.len() >= max_accounts {
            anyhow::bail!(
                "account limit reached ({} max) — remove an existing account before adding a new one",
                max_accounts
            );
        }
        Ok(())
    }

    /// Count accounts for a given provider that are not currently rate-limited.
    ///
    /// Used by `daemon.providers` to report Codex availability.
    pub async fn count_available_accounts(&self, provider: Option<&str>) -> usize {
        let accounts = match self.storage.list_accounts().await {
            Ok(a) => a,
            Err(_) => return 0,
        };
        let now = Utc::now();
        accounts
            .iter()
            .filter(|a| {
                // Filter by provider if specified.
                if let Some(p) = provider {
                    if a.provider != p {
                        return false;
                    }
                }
                // Exclude currently limited accounts.
                if let Some(ref limited_until) = a.limited_until {
                    if let Ok(until) = limited_until.parse::<DateTime<Utc>>() {
                        return until <= now;
                    }
                }
                true
            })
            .count()
    }

    /// Detect rate-limit signals in provider output text.
    ///
    /// Returns `Some(cooldown_minutes)` if a limit signal is found.
    /// Uses structured pattern matching rather than fragile substring checks
    /// to reduce false positives (e.g. "429" appearing in unrelated data).
    pub fn detect_limit_signal(output: &str) -> Option<i64> {
        let lower = output.to_lowercase();

        // ── Structured error codes (highest confidence) ─────────────────────
        // JSON error type field from providers
        if lower.contains("\"type\":\"rate_limit_error\"")
            || lower.contains("\"type\": \"rate_limit_error\"")
            || lower.contains("\"error_type\":\"rate_limit\"")
        {
            return Some(60);
        }

        // HTTP status code in structured context (not bare "429" in content)
        if lower.contains("status: 429")
            || lower.contains("status\":429")
            || lower.contains("status\": 429")
            || lower.contains("http 429")
            || lower.contains("statuscode: 429")
        {
            return Some(60);
        }

        // ── Provider-specific messages (medium confidence) ──────────────────
        // Claude / Anthropic
        if lower.contains("rate limit") || lower.contains("rate_limit") {
            return Some(60);
        }
        if lower.contains("too many requests") {
            return Some(60);
        }

        // Quota exhaustion (longer cooldown)
        if lower.contains("quota exceeded") || lower.contains("usage limit") {
            return Some(240); // 4 hours
        }

        // Overloaded (short cooldown)
        if lower.contains("overloaded") && lower.contains("capacity") {
            return Some(15);
        }

        None
    }
}
