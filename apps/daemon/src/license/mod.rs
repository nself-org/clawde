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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::storage::LicenseCacheRow;

    fn make_features(relay: bool, auto_switch: bool, clawde_plus: bool) -> Features {
        Features {
            relay,
            auto_switch,
            clawde_plus,
        }
    }

    fn make_row(
        tier: &str,
        features: &str,
        cached_at: &str,
        valid_until: &str,
        hmac: Option<String>,
    ) -> LicenseCacheRow {
        LicenseCacheRow {
            id: 1,
            tier: tier.to_string(),
            features: features.to_string(),
            cached_at: cached_at.to_string(),
            valid_until: valid_until.to_string(),
            hmac,
        }
    }

    fn independent_hmac(
        tier: &str,
        features_json: &str,
        cached_at: &str,
        valid_until: &str,
    ) -> String {
        use sha2::Digest;

        let seed = format!("clawd-license-cache-{}", env!("CARGO_PKG_VERSION"));
        let key = sha2::Sha256::digest(seed.as_bytes()).to_vec();
        let mut mac = HmacSha256::new_from_slice(&key).expect("HMAC accepts any key length");
        mac.update(tier.as_bytes());
        mac.update(b"|");
        mac.update(features_json.as_bytes());
        mac.update(b"|");
        mac.update(cached_at.as_bytes());
        mac.update(b"|");
        mac.update(valid_until.as_bytes());
        mac.finalize()
            .into_bytes()
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    }

    #[test]
    fn license_info_free_has_exact_defaults() {
        let info = LicenseInfo::free();

        assert_eq!(info.tier, "free");
        assert_eq!(info.features.relay, false);
        assert_eq!(info.features.auto_switch, false);
        assert_eq!(info.features.clawde_plus, false);
        assert_eq!(info.grace_days_remaining, None);
        assert_eq!(info.is_relay_enabled(), false);
        assert_eq!(info.is_auto_switch_enabled(), false);
        assert_eq!(info.is_clawde_plus(), false);
    }

    #[test]
    fn license_feature_accessors_return_their_own_flags() {
        let relay_only = LicenseInfo {
            tier: "personal_remote".to_string(),
            features: make_features(true, false, false),
            grace_days_remaining: Some(2),
        };
        assert_eq!(relay_only.is_relay_enabled(), true);
        assert_eq!(relay_only.is_auto_switch_enabled(), false);
        assert_eq!(relay_only.is_clawde_plus(), false);

        let auto_switch_only = LicenseInfo {
            tier: "cloud_pro".to_string(),
            features: make_features(false, true, false),
            grace_days_remaining: None,
        };
        assert_eq!(auto_switch_only.is_relay_enabled(), false);
        assert_eq!(auto_switch_only.is_auto_switch_enabled(), true);
        assert_eq!(auto_switch_only.is_clawde_plus(), false);

        let clawde_plus_only = LicenseInfo {
            tier: "clawde_plus".to_string(),
            features: make_features(false, false, true),
            grace_days_remaining: None,
        };
        assert_eq!(clawde_plus_only.is_relay_enabled(), false);
        assert_eq!(clawde_plus_only.is_auto_switch_enabled(), false);
        assert_eq!(clawde_plus_only.is_clawde_plus(), true);
    }

    #[test]
    fn verify_response_deserializes_camel_case_and_exact_grace_days() {
        let body = serde_json::json!({
            "tier": "cloud_pro",
            "features": {
                "relay": true,
                "autoSwitch": true,
                "clawdePlus": false
            },
            "gracePeriod": {
                "daysRemaining": 7
            }
        });

        let response: VerifyResponse = serde_json::from_value(body).unwrap();

        assert_eq!(response.tier, "cloud_pro");
        assert_eq!(response.features.relay, true);
        assert_eq!(response.features.auto_switch, true);
        assert_eq!(response.features.clawde_plus, false);
        let grace = response.grace_period.unwrap();
        assert_eq!(grace.days_remaining, 7);
    }

    #[test]
    fn features_deserialization_defaults_missing_clawde_plus_to_false() {
        let body = serde_json::json!({
            "relay": true,
            "autoSwitch": false
        });

        let features: Features = serde_json::from_value(body).unwrap();

        assert_eq!(features.relay, true);
        assert_eq!(features.auto_switch, false);
        assert_eq!(features.clawde_plus, false);
    }

    #[test]
    fn to_hex_uses_lowercase_and_zero_padding_for_each_byte() {
        let bytes = [0x00, 0x01, 0x0a, 0x0f, 0x10, 0xab, 0xff];

        assert_eq!(to_hex(&bytes), "00010a0f10abff");
    }

    #[test]
    fn hmac_key_is_sha256_of_versioned_license_cache_seed() {
        use sha2::Digest;

        let seed = format!("clawd-license-cache-{}", env!("CARGO_PKG_VERSION"));
        let expected = sha2::Sha256::digest(seed.as_bytes()).to_vec();

        assert_eq!(hmac_key(), expected);
    }

    #[test]
    fn compute_hmac_matches_exact_payload_contract() {
        let tier = "cloud_pro";
        let features_json = r#"{"relay":true,"autoSwitch":false,"clawdePlus":true}"#;
        let cached_at = "2026-03-01T00:00:00+00:00";
        let valid_until = "2026-03-02T00:00:00+00:00";

        let expected = independent_hmac(tier, features_json, cached_at, valid_until);

        assert_eq!(
            compute_hmac(tier, features_json, cached_at, valid_until),
            expected
        );
        assert_ne!(
            compute_hmac("free", features_json, cached_at, valid_until),
            expected
        );
        assert_ne!(
            compute_hmac(tier, r#"{"relay":false}"#, cached_at, valid_until),
            expected
        );
        assert_ne!(
            compute_hmac(tier, features_json, "2026-03-01T00:00:01+00:00", valid_until),
            expected
        );
        assert_ne!(
            compute_hmac(tier, features_json, cached_at, "2026-03-02T00:00:01+00:00"),
            expected
        );
    }

    #[test]
    fn verify_hmac_accepts_only_matching_cached_payload() {
        let tier = "cloud_pro";
        let features_json = r#"{"relay":true,"autoSwitch":false,"clawdePlus":true}"#;
        let cached_at = "2026-03-01T00:00:00+00:00";
        let valid_until = "2026-03-02T00:00:00+00:00";
        let hmac = compute_hmac(tier, features_json, cached_at, valid_until);

        let valid = make_row(tier, features_json, cached_at, valid_until, Some(hmac.clone()));
        assert_eq!(verify_hmac(&valid), true);

        let missing_hmac = make_row(tier, features_json, cached_at, valid_until, None);
        assert_eq!(verify_hmac(&missing_hmac), false);

        let wrong_hmac = make_row(
            tier,
            features_json,
            cached_at,
            valid_until,
            Some(format!("0{}", &hmac[1..])),
        );
        assert_eq!(verify_hmac(&wrong_hmac), false);

        let changed_tier = make_row("free", features_json, cached_at, valid_until, Some(hmac.clone()));
        assert_eq!(verify_hmac(&changed_tier), false);

        let changed_features = make_row(tier, r#"{"relay":false}"#, cached_at, valid_until, Some(hmac.clone()));
        assert_eq!(verify_hmac(&changed_features), false);

        let changed_cached_at = make_row(
            tier,
            features_json,
            "2026-03-01T00:00:01+00:00",
            valid_until,
            Some(hmac.clone()),
        );
        assert_eq!(verify_hmac(&changed_cached_at), false);

        let changed_valid_until = make_row(
            tier,
            features_json,
            cached_at,
            "2026-03-02T00:00:01+00:00",
            Some(hmac),
        );
        assert_eq!(verify_hmac(&changed_valid_until), false);
    }
}
