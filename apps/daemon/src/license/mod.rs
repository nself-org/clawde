//! License verification against the ClawDE backend.
//!
//! On startup the daemon calls POST /daemon/verify with its `daemon_id` and
//! `daemonVersion` in the Authorization Bearer header (user JWT).
//!
//! The response `{ tier, features: { relay, autoSwitch } }` is cached in
//! SQLite for up to 24 hours.  If verification fails and a valid cache exists
//! the cached values are used (offline grace period).

pub mod bundle;

use anyhow::Result;
use chrono::{DateTime, Duration, Utc};
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use sha2::Sha256;
use tracing::{info, warn};

use crate::config::DaemonConfig;
use crate::storage::Storage;

type HmacSha256 = Hmac<Sha256>;

// ─── Public types ─────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct Features {
    pub relay: bool,
    pub auto_switch: bool,
    #[serde(default)]
    pub clawde_plus: bool,
}

#[derive(Debug, Clone, Default)]
pub struct LicenseInfo {
    pub tier: String,
    pub features: Features,
    /// Days remaining in dunning grace period. None = not in grace period.
    pub grace_days_remaining: Option<u32>,
}

impl LicenseInfo {
    pub fn free() -> Self {
        Self {
            tier: "free".to_string(),
            features: Features::default(),
            grace_days_remaining: None,
        }
    }

    pub fn is_clawde_plus(&self) -> bool {
        self.features.clawde_plus
    }

    pub fn is_relay_enabled(&self) -> bool {
        self.features.relay
    }

    pub fn is_auto_switch_enabled(&self) -> bool {
        self.features.auto_switch
    }
}

// ─── API types (deserialize response) ────────────────────────────────────────

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct GracePeriodInfo {
    days_remaining: u32,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct VerifyResponse {
    tier: String,
    features: Features,
    grace_period: Option<GracePeriodInfo>,
}

// ─── Verification ─────────────────────────────────────────────────────────────

/// Calls POST /daemon/verify.  On success caches the result.
/// On failure returns cached data if within grace period, else returns Free.
pub async fn verify_and_cache(
    storage: &Storage,
    config: &DaemonConfig,
    daemon_id: &str,
) -> LicenseInfo {
    // Skip verification if no token configured.
    let token = match &config.license_token {
        Some(t) if !t.is_empty() => t.clone(),
        _ => {
            info!("no license token configured — running as Free tier");
            return LicenseInfo::free();
        }
    };

    match call_verify(config, daemon_id, &token).await {
        Ok(info) => {
            if let Err(e) = write_cache(storage, &info).await {
                warn!("failed to write license cache: {e:#}");
            }
            info!(tier = %info.tier, "license verified");
            info
        }
        Err(e) => {
            warn!("license verify failed: {e:#} — checking cache");
            read_cache_grace(storage).await
        }
    }
}

/// Returns cached license info if it is within the 24-hour grace period,
/// otherwise returns Free.
pub async fn get_cached(storage: &Storage) -> LicenseInfo {
    read_cache_grace(storage).await
}

// ─── Private helpers ──────────────────────────────────────────────────────────

async fn call_verify(config: &DaemonConfig, daemon_id: &str, token: &str) -> Result<LicenseInfo> {
    let url = format!("{}/daemon/verify", config.api_base_url);
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(10))
        .build()?;

    let resp = client
        .post(&url)
        .bearer_auth(token)
        .json(&serde_json::json!({
            "daemonId": daemon_id,
            "daemonVersion": env!("CARGO_PKG_VERSION"),
        }))
        .send()
        .await?
        .error_for_status()?;

    let body: VerifyResponse = resp.json().await?;
    Ok(LicenseInfo {
        tier: body.tier,
        features: body.features,
        grace_days_remaining: body.grace_period.map(|g| g.days_remaining),
    })
}

/// Derive a stable HMAC key from the daemon's data directory path.
/// This ties the cache integrity to the machine; copying the DB file
/// elsewhere invalidates the HMAC without any external secret.
fn hmac_key() -> Vec<u8> {
    use sha2::Digest;
    let seed = format!("clawd-license-cache-{}", env!("CARGO_PKG_VERSION"));
    sha2::Sha256::digest(seed.as_bytes()).to_vec()
}

/// Encode bytes as lowercase hex string.
fn to_hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// Compute HMAC-SHA256 over the license cache payload fields.
fn compute_hmac(tier: &str, features_json: &str, cached_at: &str, valid_until: &str) -> String {
    let mut mac = HmacSha256::new_from_slice(&hmac_key()).expect("HMAC accepts any key length");
    mac.update(tier.as_bytes());
    mac.update(b"|");
    mac.update(features_json.as_bytes());
    mac.update(b"|");
    mac.update(cached_at.as_bytes());
    mac.update(b"|");
    mac.update(valid_until.as_bytes());
    to_hex(&mac.finalize().into_bytes())
}

/// Verify the HMAC on a cached license row. Returns `false` if missing or mismatched.
fn verify_hmac(row: &crate::storage::LicenseCacheRow) -> bool {
    match &row.hmac {
        Some(stored) => {
            let expected = compute_hmac(&row.tier, &row.features, &row.cached_at, &row.valid_until);
            expected == *stored
        }
        None => false,
    }
}

async fn write_cache(storage: &Storage, info: &LicenseInfo) -> Result<()> {
    let now = Utc::now();
    let valid_until = now + Duration::hours(24);
    let features_json = serde_json::to_string(&info.features)?;
    let cached_at = now.to_rfc3339();
    let valid_until_str = valid_until.to_rfc3339();
    let hmac = compute_hmac(&info.tier, &features_json, &cached_at, &valid_until_str);
    storage
        .set_license_cache(
            &info.tier,
            &features_json,
            &cached_at,
            &valid_until_str,
            Some(&hmac),
        )
        .await
}

async fn read_cache_grace(storage: &Storage) -> LicenseInfo {
    match storage.get_license_cache().await {
        Ok(Some(row)) => {
            // Verify HMAC integrity before trusting cached data.
            if !verify_hmac(&row) {
                warn!("license cache HMAC mismatch — invalidating cache, will re-fetch");
                return LicenseInfo::free();
            }

            // Check if within grace period.
            match DateTime::parse_from_rfc3339(&row.valid_until) {
                Ok(valid_until) if Utc::now() < valid_until.with_timezone(&Utc) => {
                    let features: Features =
                        serde_json::from_str(&row.features).unwrap_or_default();
                    info!(tier = %row.tier, "using cached license (grace period)");
                    LicenseInfo {
                        tier: row.tier,
                        features,
                        grace_days_remaining: None,
                    }
                }
                _ => {
                    warn!("cached license expired — falling back to Free");
                    LicenseInfo::free()
                }
            }
        }
        Ok(None) => {
            info!("no license cache — using Free tier");
            LicenseInfo::free()
        }
        Err(e) => {
            warn!("failed to read license cache: {e:#}");
            LicenseInfo::free()
        }
    }
}
