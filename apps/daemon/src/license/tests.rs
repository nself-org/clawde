//! Tests for the license cache and its grace window.
//!
//! Written against the surviving-mutant list from the mutation gate. This is
//! the code that decides whether a cached paid tier is still honoured while
//! the licence server is unreachable, so these mutations are not cosmetic:
//! they either hand out paid features from an expired cache, or revoke them
//! from a valid one.

use super::*;

async fn storage() -> (tempfile::TempDir, Storage) {
    let dir = tempfile::tempdir().unwrap();
    let storage = Storage::new(dir.path()).await.expect("open storage");
    (dir, storage)
}

fn paid() -> LicenseInfo {
    LicenseInfo {
        tier: "clawde_plus".to_string(),
        features: Features::default(),
        grace_days_remaining: None,
    }
}

/// Write a cache row directly, with a correct HMAC, at an arbitrary validity.
async fn seed_cache(storage: &Storage, tier: &str, valid_until: DateTime<Utc>) {
    let features_json = serde_json::to_string(&Features::default()).unwrap();
    let cached_at = (valid_until - Duration::hours(24)).to_rfc3339();
    let valid_until_str = valid_until.to_rfc3339();
    let hmac = compute_hmac(tier, &features_json, &cached_at, &valid_until_str);
    storage
        .set_license_cache(
            tier,
            &features_json,
            &cached_at,
            &valid_until_str,
            Some(&hmac),
        )
        .await
        .unwrap();
}

// ─── write_cache ─────────────────────────────────────────────────────────────

/// Kills the `Ok(())` body replacement and the `now + Duration::hours(24)`
/// -> `-` mutation.
///
/// The body replacement is the classic `async fn -> Result<()>` mutation: it
/// reports success without writing anything, so the only way to catch it is to
/// read the row back rather than trust the return value. Flipping the `+` to
/// `-` writes a row that expired 24 hours before it was created, making every
/// cached licence useless the instant it is stored.
#[tokio::test]
async fn write_cache_stores_a_row_valid_for_twenty_four_hours() {
    let (_dir, storage) = storage().await;
    write_cache(&storage, &paid()).await.unwrap();

    let row = storage
        .get_license_cache()
        .await
        .unwrap()
        .expect("a row must actually have been written");
    assert_eq!(row.tier, "clawde_plus");

    let valid_until = DateTime::parse_from_rfc3339(&row.valid_until)
        .unwrap()
        .with_timezone(&Utc);
    let remaining = valid_until - Utc::now();
    assert!(
        remaining > Duration::hours(23) && remaining <= Duration::hours(24),
        "expected ~24h of validity, got {remaining}"
    );

    // The row must carry an HMAC that verifies, or read_cache_grace drops it.
    assert!(verify_hmac(&row));
}

// ─── read_cache_grace ────────────────────────────────────────────────────────

/// Kills `Utc::now() < valid_until` -> `>`, `==` and constant `false`, plus
/// the `Default::default()` body replacement.
///
/// A cache written moments ago is inside its window and must be honoured.
/// Every one of those mutations drops the paid tier on the floor and silently
/// downgrades a paying user to free while their licence is still valid.
#[tokio::test]
async fn a_cache_inside_the_grace_window_is_honoured() {
    let (_dir, storage) = storage().await;
    write_cache(&storage, &paid()).await.unwrap();

    assert_eq!(read_cache_grace(&storage).await.tier, "clawde_plus");
}

/// Kills `Utc::now() < valid_until` -> constant `true` and -> `>`.
///
/// This is the direction that matters for the paywall: once the grace window
/// has closed the cached tier must not be served. Both mutations keep
/// honouring a cache that expired a day ago, turning a 24-hour grace period
/// into an unbounded one.
#[tokio::test]
async fn a_cache_past_the_grace_window_is_refused() {
    let (_dir, storage) = storage().await;
    seed_cache(&storage, "clawde_plus", Utc::now() - Duration::hours(24)).await;

    assert_eq!(
        read_cache_grace(&storage).await.tier,
        "free",
        "an expired cache must not keep granting paid features"
    );
}

/// Kills the `delete !` mutation on `!verify_hmac(&row)`.
///
/// A row whose HMAC does not match has been tampered with — someone editing
/// the local database to grant themselves a tier. Dropping the `!` inverts the
/// check, so exactly the forged rows become the trusted ones.
#[tokio::test]
async fn a_cache_row_with_a_bad_hmac_is_refused() {
    let (_dir, storage) = storage().await;
    let features_json = serde_json::to_string(&Features::default()).unwrap();
    let valid_until = (Utc::now() + Duration::hours(24)).to_rfc3339();
    storage
        .set_license_cache(
            "clawde_plus",
            &features_json,
            &Utc::now().to_rfc3339(),
            &valid_until,
            Some("deadbeef"),
        )
        .await
        .unwrap();

    assert_eq!(
        read_cache_grace(&storage).await.tier,
        "free",
        "a forged cache row must not be trusted"
    );
}

/// A row with no HMAC at all is treated the same as a forged one.
#[tokio::test]
async fn a_cache_row_with_no_hmac_is_refused() {
    let (_dir, storage) = storage().await;
    let features_json = serde_json::to_string(&Features::default()).unwrap();
    let valid_until = (Utc::now() + Duration::hours(24)).to_rfc3339();
    storage
        .set_license_cache(
            "clawde_plus",
            &features_json,
            &Utc::now().to_rfc3339(),
            &valid_until,
            None,
        )
        .await
        .unwrap();

    assert_eq!(read_cache_grace(&storage).await.tier, "free");
}

/// An unparseable expiry must fail closed, not open.
#[tokio::test]
async fn a_cache_row_with_an_unparseable_expiry_is_refused() {
    let (_dir, storage) = storage().await;
    let features_json = serde_json::to_string(&Features::default()).unwrap();
    let cached_at = Utc::now().to_rfc3339();
    let hmac = compute_hmac("clawde_plus", &features_json, &cached_at, "not-a-date");
    storage
        .set_license_cache(
            "clawde_plus",
            &features_json,
            &cached_at,
            "not-a-date",
            Some(&hmac),
        )
        .await
        .unwrap();

    assert_eq!(read_cache_grace(&storage).await.tier, "free");
}

/// With no cached row at all the answer is the free tier.
#[tokio::test]
async fn an_empty_cache_yields_the_free_tier() {
    let (_dir, storage) = storage().await;
    assert_eq!(read_cache_grace(&storage).await.tier, "free");
}

// NOTE — equivalent mutant, deliberately not chased: `Utc::now() < valid_until`
// -> `<=`. The two differ only when the current instant equals the stored
// expiry to the nanosecond, which no test can arrange deterministically and no
// real run will hit. It is unkillable rather than uncovered.

// ─── pre-existing tests, kept verbatim ──────────────────────────────────────

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

fn independent_hmac(tier: &str, features_json: &str, cached_at: &str, valid_until: &str) -> String {
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
    assert!(!info.features.relay);
    assert!(!info.features.auto_switch);
    assert!(!info.features.clawde_plus);
    assert_eq!(info.grace_days_remaining, None);
    assert!(!info.is_relay_enabled());
    assert!(!info.is_auto_switch_enabled());
    assert!(!info.is_clawde_plus());
}

#[test]
fn license_feature_accessors_return_their_own_flags() {
    let relay_only = LicenseInfo {
        tier: "personal_remote".to_string(),
        features: make_features(true, false, false),
        grace_days_remaining: Some(2),
    };
    assert!(relay_only.is_relay_enabled());
    assert!(!relay_only.is_auto_switch_enabled());
    assert!(!relay_only.is_clawde_plus());

    let auto_switch_only = LicenseInfo {
        tier: "cloud_pro".to_string(),
        features: make_features(false, true, false),
        grace_days_remaining: None,
    };
    assert!(!auto_switch_only.is_relay_enabled());
    assert!(auto_switch_only.is_auto_switch_enabled());
    assert!(!auto_switch_only.is_clawde_plus());

    let clawde_plus_only = LicenseInfo {
        tier: "clawde_plus".to_string(),
        features: make_features(false, false, true),
        grace_days_remaining: None,
    };
    assert!(!clawde_plus_only.is_relay_enabled());
    assert!(!clawde_plus_only.is_auto_switch_enabled());
    assert!(clawde_plus_only.is_clawde_plus());
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
    assert!(response.features.relay);
    assert!(response.features.auto_switch);
    assert!(!response.features.clawde_plus);
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

    assert!(features.relay);
    assert!(!features.auto_switch);
    assert!(!features.clawde_plus);
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
        compute_hmac(
            tier,
            features_json,
            "2026-03-01T00:00:01+00:00",
            valid_until
        ),
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

    let valid = make_row(
        tier,
        features_json,
        cached_at,
        valid_until,
        Some(hmac.clone()),
    );
    assert!(verify_hmac(&valid));

    let missing_hmac = make_row(tier, features_json, cached_at, valid_until, None);
    assert!(!verify_hmac(&missing_hmac));

    let wrong_hmac = make_row(
        tier,
        features_json,
        cached_at,
        valid_until,
        Some(format!("0{}", &hmac[1..])),
    );
    assert!(!verify_hmac(&wrong_hmac));

    let changed_tier = make_row(
        "free",
        features_json,
        cached_at,
        valid_until,
        Some(hmac.clone()),
    );
    assert!(!verify_hmac(&changed_tier));

    let changed_features = make_row(
        tier,
        r#"{"relay":false}"#,
        cached_at,
        valid_until,
        Some(hmac.clone()),
    );
    assert!(!verify_hmac(&changed_features));

    let changed_cached_at = make_row(
        tier,
        features_json,
        "2026-03-01T00:00:01+00:00",
        valid_until,
        Some(hmac.clone()),
    );
    assert!(!verify_hmac(&changed_cached_at));

    let changed_valid_until = make_row(
        tier,
        features_json,
        cached_at,
        "2026-03-02T00:00:01+00:00",
        Some(hmac),
    );
    assert!(!verify_hmac(&changed_valid_until));
}
