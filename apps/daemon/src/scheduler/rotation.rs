//! Health-weighted round-robin account rotation.
//!
//! Selects the best available account from the pool for a given provider,
//! weighted by remaining RPM + TPM capacity. Accounts with more remaining
//! capacity are preferred.

use anyhow::{bail, Result};

use super::accounts::{AccountEntry, AccountPool};
use super::rate_limits::RateLimitTracker;

/// Select the best available account for `provider`.
///
/// Ranks candidates by `rpm_remaining + (tpm_remaining / 1000)` — a simple
/// combined health score that favours lightly-loaded accounts.
///
/// Returns an error if no account is available for the requested provider.
pub async fn select_account(
    pool: &AccountPool,
    rate_tracker: &RateLimitTracker,
    provider: &str,
) -> Result<AccountEntry> {
    let all = pool.list().await;
    let now = chrono::Utc::now();

    // Build a scored candidate list.
    let mut candidates: Vec<(AccountEntry, u64)> = Vec::new();

    for entry in all {
        if entry.provider != provider {
            continue;
        }
        if !entry.is_available {
            continue;
        }
        if entry.blocked_until.is_some_and(|t| now < t) {
            continue;
        }
        if rate_tracker.is_limited(&entry.account_id).await {
            continue;
        }

        let (rpm_rem, tpm_rem) = rate_tracker.remaining_capacity(&entry.account_id).await;
        // Combined score: weight RPM more heavily than TPM.
        let score = rpm_rem.saturating_mul(10) + tpm_rem / 1_000;
        candidates.push((entry, score));
    }

    if candidates.is_empty() {
        bail!("no available account for provider '{}'", provider);
    }

    // Pick the highest-scored candidate.
    candidates.sort_by_key(|b| std::cmp::Reverse(b.1));
    Ok(candidates.remove(0).0)
}
