//! Behavioural tests for the config precedence chain.
//!
//! `DaemonConfig::new` layers four sources — CLI/env args, process environment,
//! `{data_dir}/config.toml`, and built-in defaults — so the mutants that matter
//! are the ones that reorder an `.or()` chain, drop an `.unwrap_or`, or remove
//! an `.is_empty()` filter. Each field below is therefore exercised at more
//! than one layer, because a test that only checks the default cannot tell a
//! correct chain from one that ignores its overrides.
//!
//! **Environment safety.** These tests mutate process-global state, and Rust
//! runs tests in parallel threads. This follows the idiom already established
//! in `session::cursor::tests`: a module lock held across the whole
//! set/read/restore sequence. [`EnvGuard`] additionally *clears* every variable
//! the constructor reads on entry and restores the previous values on drop —
//! including on panic — so a test asserting "the default applies" cannot be
//! broken by a developer or runner that happens to have `CLAWD_API_URL` set.

use super::*;

/// Every environment variable `DaemonConfig::new` consults.
const CLAWD_ENV_VARS: [&str; 8] = [
    "CLAWD_API_URL",
    "CLAWD_RELAY_URL",
    "CLAWD_REGISTRY_URL",
    "CLAWD_BIND",
    "CLAWD_LICENSE_TOKEN",
    "CLAWD_UPDATE_POLICY",
    "CLAWD_LOG_FORMAT",
    "CLAWD_API_TOKEN",
];

/// Serialises the tests that mutate the `CLAWD_*` environment.
static CONFIG_ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

/// Holds the env lock, clears the `CLAWD_*` variables, and restores them on
/// drop.
///
/// Restoring in `Drop` rather than at the end of each test means a panicking
/// assertion cannot leak a variable into the next test.
struct EnvGuard {
    _lock: std::sync::MutexGuard<'static, ()>,
    saved: Vec<(&'static str, Option<String>)>,
}

impl EnvGuard {
    fn new() -> Self {
        // Tolerate a poisoned mutex: a panic in one test would otherwise make
        // every sibling fail for an unrelated reason and bury the real one.
        let lock = CONFIG_ENV_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());

        let saved = CLAWD_ENV_VARS
            .iter()
            .map(|k| (*k, std::env::var(k).ok()))
            .collect::<Vec<_>>();
        for k in CLAWD_ENV_VARS {
            std::env::remove_var(k);
        }
        Self { _lock: lock, saved }
    }

    fn set(&self, key: &str, value: &str) {
        std::env::set_var(key, value);
    }
}

impl Drop for EnvGuard {
    fn drop(&mut self) {
        for (k, v) in &self.saved {
            match v {
                Some(val) => std::env::set_var(k, val),
                None => std::env::remove_var(k),
            }
        }
    }
}

/// A data dir containing the given `config.toml` body (empty string = no file).
fn data_dir_with_toml(body: &str) -> tempfile::TempDir {
    let dir = tempfile::tempdir().expect("tempdir");
    if !body.is_empty() {
        std::fs::write(dir.path().join("config.toml"), body).expect("write config.toml");
    }
    dir
}

// ─── defaults ───────────────────────────────────────────────────────────────

#[test]
fn built_in_defaults_apply_when_nothing_else_is_set() {
    let _env = EnvGuard::new();
    let dir = data_dir_with_toml("");

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);

    // Exact values, so a mutant returning a different constant dies.
    assert_eq!(cfg.port, 4300);
    assert_eq!(cfg.max_sessions, 10);
    assert_eq!(cfg.max_accounts, 10);
    assert_eq!(cfg.session_prune_days, 30);
    assert_eq!(cfg.log, "info");
    assert_eq!(cfg.api_base_url, "https://api.clawde.io");
    assert_eq!(cfg.relay_url, "wss://api.clawde.io/relay/ws");
    assert_eq!(cfg.registry_url, "https://registry.clawde.io");
    assert_eq!(cfg.bind_address, "127.0.0.1");
    assert_eq!(cfg.update_policy, "auto");
    assert_eq!(cfg.log_format, "pretty");
    assert_eq!(cfg.license_token, None);
    assert_eq!(cfg.api_token, None);
}

#[test]
fn the_default_bind_address_is_loopback_not_all_interfaces() {
    // This one is security-relevant on its own: defaulting to 0.0.0.0 would
    // expose the daemon to the LAN, so it gets an explicit assertion.
    assert_eq!(default_bind_address(), "127.0.0.1");
}

// ─── TOML layer ─────────────────────────────────────────────────────────────

#[test]
fn toml_values_override_the_built_in_defaults() {
    let _env = EnvGuard::new();
    let dir = data_dir_with_toml(
        r#"
port = 5555
max_sessions = 3
max_accounts = 7
session_prune_days = 90
log = "debug"
api_base_url = "https://toml.example"
relay_url = "wss://toml.example/relay"
registry_url = "https://registry.toml.example"
bind_address = "0.0.0.0"
update_policy = "never"
log_format = "json"
license_token = "toml-license"
api_token = "toml-api-token"
"#,
    );

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);

    assert_eq!(cfg.port, 5555);
    assert_eq!(cfg.max_sessions, 3);
    assert_eq!(cfg.max_accounts, 7);
    assert_eq!(cfg.session_prune_days, 90);
    assert_eq!(cfg.log, "debug");
    assert_eq!(cfg.api_base_url, "https://toml.example");
    assert_eq!(cfg.relay_url, "wss://toml.example/relay");
    assert_eq!(cfg.registry_url, "https://registry.toml.example");
    assert_eq!(cfg.bind_address, "0.0.0.0");
    assert_eq!(cfg.update_policy, "never");
    assert_eq!(cfg.log_format, "json");
    assert_eq!(cfg.license_token.as_deref(), Some("toml-license"));
    assert_eq!(cfg.api_token.as_deref(), Some("toml-api-token"));
}

#[test]
fn an_unparseable_toml_falls_back_to_defaults_rather_than_failing() {
    let _env = EnvGuard::new();
    let dir = data_dir_with_toml("this is not = valid toml [[[");

    // load_toml logs and returns None on a parse error; the daemon must still
    // start on its defaults rather than refusing to boot.
    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);
    assert_eq!(cfg.port, 4300);
    assert_eq!(cfg.log, "info");
}

// ─── CLI layer ──────────────────────────────────────────────────────────────

#[test]
fn cli_arguments_win_over_both_toml_and_defaults() {
    let _env = EnvGuard::new();
    let dir = data_dir_with_toml(
        r#"
port = 5555
max_sessions = 3
log = "debug"
bind_address = "0.0.0.0"
"#,
    );

    let cfg = DaemonConfig::new(
        Some(9999),
        Some(dir.path().to_path_buf()),
        Some("trace".to_string()),
        Some(42),
        Some("192.168.1.5".to_string()),
    );

    // Each of these would take the TOML value if the .or() chain were reordered.
    assert_eq!(cfg.port, 9999);
    assert_eq!(cfg.log, "trace");
    assert_eq!(cfg.max_sessions, 42);
    assert_eq!(cfg.bind_address, "192.168.1.5");
}

// ─── environment layer ──────────────────────────────────────────────────────

#[test]
fn environment_variables_override_toml() {
    let env = EnvGuard::new();
    let dir = data_dir_with_toml(
        r#"
api_base_url = "https://toml.example"
relay_url = "wss://toml.example/relay"
registry_url = "https://registry.toml.example"
bind_address = "10.0.0.1"
update_policy = "never"
log_format = "json"
license_token = "toml-license"
api_token = "toml-api-token"
"#,
    );

    env.set("CLAWD_API_URL", "https://env.example");
    env.set("CLAWD_RELAY_URL", "wss://env.example/relay");
    env.set("CLAWD_REGISTRY_URL", "https://registry.env.example");
    env.set("CLAWD_BIND", "172.16.0.1");
    env.set("CLAWD_UPDATE_POLICY", "manual");
    env.set("CLAWD_LOG_FORMAT", "compact");
    env.set("CLAWD_LICENSE_TOKEN", "env-license");
    env.set("CLAWD_API_TOKEN", "env-api-token");

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);

    assert_eq!(cfg.api_base_url, "https://env.example");
    assert_eq!(cfg.relay_url, "wss://env.example/relay");
    assert_eq!(cfg.registry_url, "https://registry.env.example");
    assert_eq!(cfg.bind_address, "172.16.0.1");
    assert_eq!(cfg.update_policy, "manual");
    assert_eq!(cfg.log_format, "compact");
    assert_eq!(cfg.license_token.as_deref(), Some("env-license"));
    assert_eq!(cfg.api_token.as_deref(), Some("env-api-token"));
}

#[test]
fn an_explicit_bind_argument_wins_over_the_bind_environment_variable() {
    let env = EnvGuard::new();
    let dir = data_dir_with_toml("");
    env.set("CLAWD_BIND", "172.16.0.1");

    let cfg = DaemonConfig::new(
        None,
        Some(dir.path().to_path_buf()),
        None,
        None,
        Some("192.168.1.5".to_string()),
    );

    assert_eq!(
        cfg.bind_address, "192.168.1.5",
        "the CLI flag is the highest-priority layer"
    );
}

#[test]
fn empty_environment_variables_are_ignored_for_the_filtered_fields() {
    let env = EnvGuard::new();
    let dir = data_dir_with_toml(
        r#"
registry_url = "https://registry.toml.example"
bind_address = "10.0.0.1"
update_policy = "never"
log_format = "json"
license_token = "toml-license"
api_token = "toml-api-token"
"#,
    );

    // Six fields guard with `.filter(|s| !s.is_empty())`. An empty variable —
    // what an unset shell variable expands to — must fall through to the next
    // layer rather than blanking the setting.
    for k in [
        "CLAWD_REGISTRY_URL",
        "CLAWD_BIND",
        "CLAWD_UPDATE_POLICY",
        "CLAWD_LOG_FORMAT",
        "CLAWD_LICENSE_TOKEN",
        "CLAWD_API_TOKEN",
    ] {
        env.set(k, "");
    }

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);

    assert_eq!(cfg.registry_url, "https://registry.toml.example");
    assert_eq!(cfg.bind_address, "10.0.0.1");
    assert_eq!(cfg.update_policy, "never");
    assert_eq!(cfg.log_format, "json");
    assert_eq!(cfg.license_token.as_deref(), Some("toml-license"));
    assert_eq!(cfg.api_token.as_deref(), Some("toml-api-token"));
}

#[test]
fn empty_api_and_relay_url_variables_are_not_filtered() {
    let env = EnvGuard::new();
    let dir = data_dir_with_toml(
        r#"
api_base_url = "https://toml.example"
relay_url = "wss://toml.example/relay"
"#,
    );

    // This pins CURRENT behaviour and it is INCONSISTENT with the six fields
    // above: `api_base_url` and `relay_url` are the only two env reads without
    // a `.filter(|s| !s.is_empty())`, so `CLAWD_API_URL=""` wins over the TOML
    // value and blanks the URL, where `CLAWD_REGISTRY_URL=""` falls through.
    //
    // The test exists so the asymmetry cannot change silently in either
    // direction. Whether it is intentional is flagged rather than changed here,
    // since adding the filter alters startup behaviour for anyone relying on it.
    env.set("CLAWD_API_URL", "");
    env.set("CLAWD_RELAY_URL", "");

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);

    assert_eq!(
        cfg.api_base_url, "",
        "an empty CLAWD_API_URL currently wins — see the comment above"
    );
    assert_eq!(
        cfg.relay_url, "",
        "an empty CLAWD_RELAY_URL currently wins — see the comment above"
    );
}

// ─── provider profiles ──────────────────────────────────────────────────────

#[test]
fn provider_profiles_are_loaded_from_toml_and_looked_up_by_name() {
    let _env = EnvGuard::new();
    let dir = data_dir_with_toml(
        r#"
[provider.claude]
model = "claude-sonnet-4-6"

[provider.codex]
model = "gpt-5.5"
"#,
    );

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);

    assert!(
        cfg.provider_profile("claude").is_some(),
        "a configured provider must resolve"
    );
    assert!(cfg.provider_profile("codex").is_some());
    assert!(
        cfg.provider_profile("nonexistent").is_none(),
        "an unconfigured provider must resolve to None, both directions pinned"
    );
}

#[test]
fn no_provider_table_yields_an_empty_profile_map() {
    let _env = EnvGuard::new();
    let dir = data_dir_with_toml("port = 4300");

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);
    assert!(cfg.provider_profile("claude").is_none());
}

// ─── data_dir ───────────────────────────────────────────────────────────────

#[test]
fn an_explicit_data_dir_is_used_verbatim_and_is_where_the_toml_is_read_from() {
    let _env = EnvGuard::new();
    let dir = data_dir_with_toml("port = 6001");

    let cfg = DaemonConfig::new(None, Some(dir.path().to_path_buf()), None, None, None);

    assert_eq!(cfg.data_dir, dir.path());
    assert_eq!(
        cfg.port, 6001,
        "the TOML must be read from the data_dir that was passed in"
    );
}
