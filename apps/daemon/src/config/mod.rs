use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use tokio::sync::RwLock;
use tracing::{error, info, warn};

const DEFAULT_PORT: u16 = 4300;
const DEFAULT_MAX_SESSIONS: usize = 10;
const DEFAULT_MAX_ACCOUNTS: usize = 10;
const DEFAULT_API_BASE_URL: &str = "https://api.clawde.io";
const DEFAULT_RELAY_URL: &str = "wss://api.clawde.io/relay/ws";
const DEFAULT_REGISTRY_URL: &str = "https://registry.clawde.io";
const DEFAULT_PRUNE_DAYS: u32 = 30;

fn default_bind_address() -> String {
    "127.0.0.1".to_string()
}

// ─── DiffRiskConfig ───────────────────────────────────────────────────────────

/// Diff risk threshold configuration (`[diff_risk]` in config.toml or `.clawd/project.toml`).
///
/// DR.T02 — risk thresholds can be tuned per-project without restarting the daemon.
#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct DiffRiskConfig {
    /// Risk score at which a warning is emitted (default: 50.0).
    pub warn_threshold: f64,
    /// Risk score at which the diff is blocked (default: 200.0).
    pub block_threshold: f64,
}

impl Default for DiffRiskConfig {
    fn default() -> Self {
        Self {
            warn_threshold: 50.0,
            block_threshold: 200.0,
        }
    }
}

// ─── ConnectivityConfig ───────────────────────────────────────────────────────

/// Daemon connectivity configuration (`[connectivity]` in config.toml).
///
/// Controls how the daemon routes connections: relay (default), direct LAN
/// (mDNS), or VPN/explicit IP.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct ConnectivityConfig {
    /// Try direct LAN connection first; fall back to relay within 2s. Default: false.
    pub prefer_direct: bool,
    /// Connect to daemon on an explicit VPN/LAN IP without relay.
    /// Example: `"10.0.1.5"`. None = not configured.
    pub vpn_host: Option<String>,
    /// Air-gap mode: disable all outbound relay/API calls. Default: false.
    /// In air-gap mode the daemon only accepts local connections.
    pub air_gap: bool,
    /// Path to offline license bundle (Enterprise air-gap). None = online verification.
    /// Example: `"/etc/clawd/license.bundle"`
    pub license_path: Option<String>,
    /// Local pack registry path or URL. Overrides registry.clawde.io for pack installs.
    /// Use in air-gap: `"/var/clawd/packs"` (directory) or `"http://internal.registry:8080"`.
    pub local_registry: Option<String>,
}

// ─── CommunityConfig ─────────────────────────────────────────────────────────

/// Community integration opt-ins (`[community]` in config.toml). Sprint TT DC.3.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct CommunityConfig {
    /// Post session-created events to the ClawDE Discord #dev-activity channel.
    /// Cloud users only — daemon notifies the relay which forwards to Discord.
    /// Default: false (opt-in only).
    pub discord_notify: bool,
}

// ─── LimitsConfig ────────────────────────────────────────────────────────────

/// Cost budget limits (`[limits]` in config.toml). Sprint PP OB.6.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct LimitsConfig {
    /// Maximum daily AI spend in USD. None = no limit. Emits `budget_warning` at 80%.
    pub daily_cost_usd: Option<f64>,
    /// Maximum monthly AI spend in USD. None = no limit. Emits `budget_warning` at 80%.
    pub monthly_cost_usd: Option<f64>,
}

// ─── ObservabilityConfig ─────────────────────────────────────────────────────

/// Daemon observability configuration (`[observability]` in config.toml).
#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct ObservabilityConfig {
    /// Log SQLite queries that exceed this threshold (milliseconds). Default: 100.
    /// Set to 0 to disable slow query logging.
    pub slow_query_threshold_ms: u64,
}

impl Default for ObservabilityConfig {
    fn default() -> Self {
        Self {
            slow_query_threshold_ms: 100,
        }
    }
}

// ─── SecurityConfig ───────────────────────────────────────────────────────────

/// Daemon security configuration (`[security]` in config.toml).
///
/// All fields default to permissive/unlimited — tighten to harden.
#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct SecurityConfig {
    /// If non-empty, only tools in this list are permitted (case-insensitive).
    /// Empty = all tools allowed.
    pub allowed_tools: Vec<String>,
    /// Tools always blocked regardless of `allowed_tools` (case-insensitive).
    pub denied_tools: Vec<String>,
    /// Bash tool cannot operate on paths with these prefixes (`~` is expanded).
    pub denied_paths: Vec<String>,
    /// Max new WebSocket connections per IP per minute (0 = unlimited; default: 10).
    pub max_connections_per_minute_per_ip: u32,
    /// Max RPC method calls per IP per minute (0 = unlimited; default: 300).
    pub max_rpc_calls_per_minute_per_ip: u32,
}

impl Default for SecurityConfig {
    fn default() -> Self {
        Self {
            allowed_tools: vec![],
            denied_tools: vec![],
            denied_paths: vec![],
            max_connections_per_minute_per_ip: 10,
            max_rpc_calls_per_minute_per_ip: 300,
        }
    }
}

// ─── TOML config file ─────────────────────────────────────────────────────────

/// Per-provider configuration profile.
///
/// Parsed from TOML sections like `[provider.claude]`, `[provider.codex]`, etc.
#[derive(Debug, Clone, Deserialize, Default, serde::Serialize)]
pub struct ProviderProfile {
    /// Request timeout in seconds (default: 300).
    pub timeout: Option<u64>,
    /// Maximum tokens for AI responses (default: provider-specific).
    pub max_tokens: Option<u64>,
    /// Prefix prepended to the system prompt for this provider.
    pub system_prompt_prefix: Option<String>,
}

/// `{data_dir}/config.toml` — all fields are optional overrides.
/// Priority: CLI / env var  >  TOML  >  built-in default.
#[derive(Deserialize, Default)]
struct TomlConfig {
    /// WebSocket server port (default: 4300).
    port: Option<u16>,
    /// Maximum concurrent sessions; 0 = unlimited (default: 10).
    max_sessions: Option<usize>,
    /// Maximum registered accounts in the pool; 0 = unlimited (default: 10).
    max_accounts: Option<usize>,
    /// Log level filter string, e.g. "debug", "info,clawd=trace" (default: "info").
    log: Option<String>,
    /// License JWT for verifying relay / auto-switch features. Omit for Free tier.
    license_token: Option<String>,
    /// Override the ClawDE API base URL (default: https://api.clawde.io).
    api_base_url: Option<String>,
    /// Override the relay WebSocket URL (default: wss://api.clawde.io/relay/ws).
    relay_url: Option<String>,
    /// Override the pack registry base URL (default: https://registry.clawde.io).
    registry_url: Option<String>,
    /// Bind address for the WebSocket server (default: "127.0.0.1"; use "0.0.0.0" for LAN access).
    bind_address: Option<String>,
    /// How many days of idle/error sessions to keep before pruning (default: 30; 0 = never).
    session_prune_days: Option<u32>,
    /// Per-provider configuration profiles (e.g. `[provider.claude]`).
    provider: Option<std::collections::HashMap<String, ProviderProfile>>,
    /// Model intelligence configuration (`[model_intelligence]`).
    model_intelligence: Option<ModelIntelligenceConfig>,
    /// Resource governor configuration (`[resources]`).
    resources: Option<ResourceConfig>,
    /// Auto-update policy: "auto" | "manual" | "never".
    update_policy: Option<String>,
    /// Security configuration (`[security]`).
    security: Option<SecurityConfig>,
    /// Log output format: "pretty" (default, human-readable) | "json" (structured for log aggregators).
    log_format: Option<String>,
    /// Observability configuration (`[observability]`).
    observability: Option<ObservabilityConfig>,
    /// Code completion configuration (`[completion]`).
    completion: Option<CompletionConfig>,
    /// Connectivity configuration (`[connectivity]`).
    connectivity: Option<ConnectivityConfig>,
    /// Cost budget limits (`[limits]`).
    limits: Option<LimitsConfig>,
    /// Bearer token for the REST API (Sprint QQ RA.6). None = REST auth disabled.
    api_token: Option<String>,
    /// Community integration opt-ins (`[community]`).
    community: Option<CommunityConfig>,
    /// Diff risk thresholds (`[diff_risk]`).
    diff_risk: Option<DiffRiskConfig>,
}

fn load_toml(data_dir: &Path) -> Option<TomlConfig> {
    let path = data_dir.join("config.toml");
    let contents = std::fs::read_to_string(&path).ok()?;
    match toml::from_str::<TomlConfig>(&contents) {
        Ok(cfg) => Some(cfg),
        Err(e) => {
            error!(path = %path.display(), err = %e, "failed to parse config.toml — using defaults");
            None
        }
    }
}

// ─── CompletionConfig ─────────────────────────────────────────────────────────

/// Code completion configuration (`[completion]` in config.toml).
#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct CompletionConfig {
    /// Enable inline code completions. Default: true.
    pub enabled: bool,
    /// Debounce delay before sending a completion request (milliseconds). Default: 150.
    pub debounce_ms: u64,
    /// Maximum tokens to generate per completion. Default: 64.
    pub max_tokens: u32,
    /// Provider to use for completions: "codex-spark" | "claude-haiku". Default: "codex-spark".
    pub provider: String,
}

impl Default for CompletionConfig {
    fn default() -> Self {
        Self {
            enabled: true,
            debounce_ms: 150,
            max_tokens: 64,
            provider: "codex-spark".to_string(),
        }
    }
}

// ─── ModelIntelligenceConfig ──────────────────────────────────────────────────

/// Sub-struct holding the default model IDs for each tier.
///
/// Configurable via `[model_intelligence.provider_models]` in config.toml.
#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct ProviderModels {
    /// Model ID for Simple tasks. Default: claude-haiku-4-5.
    pub haiku: String,
    /// Model ID for Moderate and Complex tasks. Default: claude-sonnet-4-6.
    pub sonnet: String,
    /// Model ID for DeepReasoning tasks. Default: claude-opus-4-6.
    pub opus: String,
    /// Model ID when routing to Codex provider. Default: gpt-4o.
    pub codex: String,
}

impl Default for ProviderModels {
    fn default() -> Self {
        Self {
            haiku: "claude-haiku-4-5".to_string(),
            sonnet: "claude-sonnet-4-6".to_string(),
            opus: "claude-opus-4-6".to_string(),
            codex: "gpt-4o".to_string(),
        }
    }
}

/// Configuration for the Model Intelligence system (`[model_intelligence]` in config.toml).
///
/// All fields are optional — sensible defaults apply when the section is absent.
#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct ModelIntelligenceConfig {
    /// Automatically select the best model based on task complexity. Default: true.
    /// Set to false to always use `complexity_floor`.
    pub auto_select: bool,
    /// Minimum complexity level to route. Accepted: "Simple" | "Moderate" | "Complex" | "DeepReasoning".
    /// Only effective when `auto_select = false`. Default: "Simple".
    pub complexity_floor: String,
    /// Hard cap on the most powerful model auto-select may choose.
    /// Accepted values: "haiku" | "sonnet" | "opus". Default: "opus" (no cap).
    pub max_model: String,
    /// Monthly USD budget cap. 0.0 = no limit. Default: 0.0.
    pub monthly_budget_usd: f64,
    /// Automatically retry with an upgraded model on poor-quality responses. Default: true.
    pub upgrade_on_failure: bool,
    /// Per-tier model IDs. Override these to use different model versions.
    pub provider_models: ProviderModels,
}

impl Default for ModelIntelligenceConfig {
    fn default() -> Self {
        Self {
            auto_select: true,
            complexity_floor: "Simple".to_string(),
            max_model: "opus".to_string(),
            monthly_budget_usd: 0.0,
            upgrade_on_failure: true,
            provider_models: ProviderModels::default(),
        }
    }
}

// ─── ResourceConfig ───────────────────────────────────────────────────────────

#[derive(Debug, Clone, serde::Deserialize, serde::Serialize)]
#[serde(default)]
pub struct ResourceConfig {
    /// Max percentage of total system RAM the daemon + CLI children may use (10-95).
    pub max_memory_percent: u8,
    /// Hard cap on simultaneous active CLI subprocesses. 0 = auto-calculate.
    pub max_concurrent_active: u8,
    /// Seconds of no user interaction before Active → Warm (SIGSTOP). Default: 120.
    pub idle_to_warm_secs: u64,
    /// Seconds of Warm state before Warm → Cold (kill + save). Default: 300.
    pub warm_to_cold_secs: u64,
    /// Number of pre-warmed pool workers. Default: 1.
    pub process_pool_size: u8,
    /// Emergency threshold: if RAM exceeds this %, aggressively evict. Default: 90.
    pub emergency_memory_percent: u8,
    /// Polling interval in seconds. Default: 5.
    pub poll_interval_secs: u64,
}

impl Default for ResourceConfig {
    fn default() -> Self {
        Self {
            max_memory_percent: 70,
            max_concurrent_active: 0, // 0 = auto
            idle_to_warm_secs: 120,
            warm_to_cold_secs: 300,
            process_pool_size: 1,
            emergency_memory_percent: 90,
            poll_interval_secs: 5,
        }
    }
}

// ─── DaemonConfig ─────────────────────────────────────────────────────────────

#[derive(Debug, Clone)]
pub struct DaemonConfig {
    pub port: u16,
    pub data_dir: PathBuf,
    pub log: String,
    pub max_sessions: usize,
    /// Maximum registered accounts in the pool (0 = unlimited, default: 10).
    pub max_accounts: usize,
    /// How many days before idle/error sessions are pruned (0 = never).
    pub session_prune_days: u32,
    /// JWT for license verification (CLAWD_LICENSE_TOKEN env var).
    /// None means Free tier — no verification attempt.
    pub license_token: Option<String>,
    /// Backend API base URL (CLAWD_API_URL env var, default: https://api.clawde.io).
    pub api_base_url: String,
    /// Relay WebSocket URL (CLAWD_RELAY_URL env var).
    pub relay_url: String,
    /// Pack registry base URL (CLAWD_REGISTRY_URL env var, default: https://registry.clawde.io).
    pub registry_url: String,
    /// Bind address for the WebSocket server (CLAWD_BIND env var, default: "127.0.0.1").
    pub bind_address: String,
    /// Per-provider profiles (e.g. timeout, max_tokens, system_prompt_prefix).
    pub providers: std::collections::HashMap<String, ProviderProfile>,
    /// Resource governance configuration.
    pub resources: ResourceConfig,
    /// Model intelligence — auto model selection, budget caps, upgrade on failure.
    pub model_intelligence: ModelIntelligenceConfig,
    /// Auto-update policy: "auto" (default), "manual", or "never".
    /// - "auto": check + download + apply automatically when idle
    /// - "manual": check + download but require explicit daemon.applyUpdate RPC
    /// - "never": disable update checks entirely
    pub update_policy: String,
    /// Security configuration: tool allowlist/denylist, rate limits.
    pub security: SecurityConfig,
    /// Log output format: "pretty" (default) | "json" (structured for Loki/Elasticsearch).
    pub log_format: String,
    /// Observability: slow query threshold, future metrics settings.
    pub observability: ObservabilityConfig,
    /// Code completion: enable, debounce, max_tokens, provider.
    pub completion: CompletionConfig,
    /// Connectivity: prefer_direct, vpn_host, air_gap.
    pub connectivity: ConnectivityConfig,
    /// Cost budget limits — daily/monthly caps in USD (Sprint PP OB.6).
    pub limits: LimitsConfig,
    /// Bearer token required to call the REST API (Sprint QQ RA.6).
    /// Set via `CLAWD_API_TOKEN` env var or `api_token` in config.toml.
    /// None = REST authentication disabled (local-only, trusted loopback use).
    pub api_token: Option<String>,
    /// Community integration opt-ins (Sprint TT DC.3).
    pub community: CommunityConfig,
    /// Diff risk thresholds (Sprint ZZ DR.T02).
    pub diff_risk: DiffRiskConfig,
}

impl DaemonConfig {
    /// Build config from CLI/env args + optional TOML file.
    ///
    /// Priority (highest to lowest):
    ///   1. CLI / env — passed as `Some(value)` from clap
    ///   2. TOML file at `{data_dir}/config.toml`
    ///   3. Built-in defaults
    pub fn new(
        port: Option<u16>,
        data_dir: Option<PathBuf>,
        log: Option<String>,
        max_sessions: Option<usize>,
        bind_address: Option<String>,
    ) -> Self {
        let data_dir = data_dir.unwrap_or_else(default_data_dir);

        // Load TOML as the lowest-priority override layer
        let toml = load_toml(&data_dir).unwrap_or_default();

        let port = port.or(toml.port).unwrap_or(DEFAULT_PORT);
        let log = log.or(toml.log).unwrap_or_else(|| "info".to_string());
        let max_sessions = max_sessions
            .or(toml.max_sessions)
            .unwrap_or(DEFAULT_MAX_SESSIONS);
        let max_accounts = toml.max_accounts.unwrap_or(DEFAULT_MAX_ACCOUNTS);
        let session_prune_days = toml.session_prune_days.unwrap_or(DEFAULT_PRUNE_DAYS);

        let api_base_url = std::env::var("CLAWD_API_URL")
            .ok()
            .or(toml.api_base_url)
            .unwrap_or_else(|| DEFAULT_API_BASE_URL.to_string());

        let relay_url = std::env::var("CLAWD_RELAY_URL")
            .ok()
            .or(toml.relay_url)
            .unwrap_or_else(|| DEFAULT_RELAY_URL.to_string());

        let registry_url = std::env::var("CLAWD_REGISTRY_URL")
            .ok()
            .filter(|s| !s.is_empty())
            .or(toml.registry_url)
            .unwrap_or_else(|| DEFAULT_REGISTRY_URL.to_string());

        let bind_address = bind_address
            .or(std::env::var("CLAWD_BIND").ok().filter(|s| !s.is_empty()))
            .or(toml.bind_address)
            .unwrap_or_else(default_bind_address);

        let license_token = std::env::var("CLAWD_LICENSE_TOKEN")
            .ok()
            .filter(|t| !t.is_empty())
            .or(toml.license_token);

        let providers = toml.provider.unwrap_or_default();
        let model_intelligence = toml.model_intelligence.unwrap_or_default();
        let resources = toml.resources.unwrap_or_default();

        let update_policy = std::env::var("CLAWD_UPDATE_POLICY")
            .ok()
            .filter(|s| !s.is_empty())
            .or(toml.update_policy)
            .unwrap_or_else(|| "auto".to_string());

        let security = toml.security.unwrap_or_default();

        let log_format = std::env::var("CLAWD_LOG_FORMAT")
            .ok()
            .filter(|s| !s.is_empty())
            .or(toml.log_format)
            .unwrap_or_else(|| "pretty".to_string());

        let observability = toml.observability.unwrap_or_default();
        let completion = toml.completion.unwrap_or_default();
        let connectivity = toml.connectivity.unwrap_or_default();
        let limits = toml.limits.unwrap_or_default();

        let api_token = std::env::var("CLAWD_API_TOKEN")
            .ok()
            .filter(|s| !s.is_empty())
            .or(toml.api_token);

        let community = toml.community.unwrap_or_default();
        let diff_risk = toml.diff_risk.unwrap_or_default();

        Self {
            port,
            data_dir,
            log,
            max_sessions,
            max_accounts,
            session_prune_days,
            license_token,
            api_base_url,
            relay_url,
            registry_url,
            bind_address,
            providers,
            resources,
            model_intelligence,
            update_policy,
            security,
            log_format,
            observability,
            completion,
            connectivity,
            limits,
            api_token,
            community,
            diff_risk,
        }
    }

    /// Get the provider profile for a specific provider name, if configured.
    pub fn provider_profile(&self, name: &str) -> Option<&ProviderProfile> {
        self.providers.get(name)
    }
}

impl Default for DaemonConfig {
    fn default() -> Self {
        Self::new(None, None, None, None, None)
    }
}

// ─── Hot-reloadable config subset ─────────────────────────────────────────────

/// Non-critical config fields that can be changed without restarting the daemon.
#[derive(Debug, Clone)]
pub struct HotConfig {
    pub log_level: String,
    pub session_prune_days: u32,
}

/// Watches `config.toml` for changes and reloads non-critical fields.
///
/// The watcher uses the `notify` crate (kqueue on macOS, inotify on Linux)
/// to detect file modifications. Only `log_level` and `session_prune_days`
/// are reloaded; port, max_sessions, and other startup-only fields require
/// a full restart.
pub struct ConfigWatcher {
    pub hot: Arc<RwLock<HotConfig>>,
    // Hold the watcher alive; dropping it stops the file watch.
    _watcher: notify_debouncer_full::Debouncer<
        notify_debouncer_full::notify::RecommendedWatcher,
        notify_debouncer_full::FileIdMap,
    >,
}

impl ConfigWatcher {
    /// Start watching `{data_dir}/config.toml` for changes.
    ///
    /// Returns `None` if the watcher could not be created (non-fatal; the
    /// daemon runs fine without hot-reload).
    pub fn start(data_dir: &Path) -> Option<Self> {
        let config_path = data_dir.join("config.toml");
        let initial = load_hot_config(&config_path);
        let hot = Arc::new(RwLock::new(initial));

        let hot_clone = hot.clone();
        let config_path_clone = config_path.clone();
        let rt_handle = tokio::runtime::Handle::current();

        let watcher = notify_debouncer_full::new_debouncer(
            std::time::Duration::from_secs(2),
            None,
            move |result: notify_debouncer_full::DebounceEventResult| {
                if let Ok(events) = result {
                    // Only act on modify/create events
                    let relevant = events.iter().any(|e| {
                        use notify_debouncer_full::notify::EventKind;
                        matches!(e.event.kind, EventKind::Modify(_) | EventKind::Create(_))
                    });
                    if relevant {
                        let hot = hot_clone.clone();
                        let path = config_path_clone.clone();
                        rt_handle.spawn(async move {
                            let new_config = load_hot_config(&path);
                            let mut guard = hot.write().await;
                            if guard.log_level != new_config.log_level
                                || guard.session_prune_days != new_config.session_prune_days
                            {
                                info!(
                                    log_level = %new_config.log_level,
                                    prune_days = new_config.session_prune_days,
                                    "config.toml reloaded"
                                );
                                *guard = new_config;
                            }
                        });
                    }
                }
            },
        );

        match watcher {
            Ok(mut debouncer) => {
                use notify_debouncer_full::notify::Watcher as _;
                // Watch the data_dir (parent of config.toml) since watching a
                // non-existent file fails on some platforms.
                let watch_path = config_path.parent().unwrap_or_else(|| Path::new("."));
                if let Err(e) = debouncer.watcher().watch(
                    watch_path,
                    notify_debouncer_full::notify::RecursiveMode::NonRecursive,
                ) {
                    warn!("config watcher failed to start: {e} — hot-reload disabled");
                    return None;
                }
                info!(path = %config_path.display(), "config hot-reload watcher started");
                Some(Self {
                    hot,
                    _watcher: debouncer,
                })
            }
            Err(e) => {
                warn!("config watcher creation failed: {e} — hot-reload disabled");
                None
            }
        }
    }
}

/// Load only the hot-reloadable fields from config.toml.
fn load_hot_config(path: &Path) -> HotConfig {
    let toml = std::fs::read_to_string(path)
        .ok()
        .and_then(|s| toml::from_str::<TomlConfig>(&s).ok())
        .unwrap_or_default();
    HotConfig {
        log_level: toml.log.unwrap_or_else(|| "info".to_string()),
        session_prune_days: toml.session_prune_days.unwrap_or(DEFAULT_PRUNE_DAYS),
    }
}

fn default_data_dir() -> PathBuf {
    #[cfg(target_os = "macos")]
    {
        // ~/Library/Application Support/clawd
        if let Ok(home) = std::env::var("HOME") {
            return PathBuf::from(home)
                .join("Library")
                .join("Application Support")
                .join("clawd");
        }
    }
    #[cfg(target_os = "linux")]
    {
        // $XDG_DATA_HOME/clawd or ~/.local/share/clawd
        if let Ok(xdg) = std::env::var("XDG_DATA_HOME") {
            return PathBuf::from(xdg).join("clawd");
        }
        if let Ok(home) = std::env::var("HOME") {
            return PathBuf::from(home)
                .join(".local")
                .join("share")
                .join("clawd");
        }
    }
    #[cfg(target_os = "windows")]
    {
        // %APPDATA%\clawd
        if let Ok(appdata) = std::env::var("APPDATA") {
            return PathBuf::from(appdata).join("clawd");
        }
    }
    // Fallback
    PathBuf::from(".clawd")
}

// Kept in its own file so the tests do not need to be read alongside the
// config structs.
#[cfg(test)]
mod tests;
