//! Provider capability definitions and selection logic.
//!
//! Each supported AI provider has a `ProviderCapabilities` struct that describes
//! what it can and cannot do.  The `select_provider` function uses these
//! capabilities together with a `SelectionContext` to pick the best provider
//! for a given role/complexity combination.

use serde::{Deserialize, Serialize};

// ─── Provider enum ────────────────────────────────────────────────────────────

/// Supported AI provider identifiers.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum Provider {
    Claude,
    Codex,
    Unknown(String),
}

// ─── ProviderSpeed ────────────────────────────────────────────────────────────

/// Speed/quality tier for a Codex request.
///
/// `Fast` maps to `codex-spark` (low latency, classification/review workloads).
/// `Full` maps to `gpt-5.3-codex` (highest quality, planning/implementation).
/// Claude always uses `Full` quality — the Claude Router role uses a cheaper
/// Haiku model instead, so the speed distinction is Codex-specific.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum ProviderSpeed {
    Fast,
    Full,
}

// ─── ProviderCapabilities ─────────────────────────────────────────────────────

/// Static capability matrix for a single AI provider.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProviderCapabilities {
    pub provider: Provider,
    /// Can fork/branch sessions for parallel exploration.
    pub supports_fork: bool,
    /// Can resume a prior session by its ID.
    pub supports_resume: bool,
    /// Speaks the MCP (Model Context Protocol) wire protocol.
    pub supports_mcp: bool,
    /// Has built-in filesystem / network sandboxing.
    pub supports_sandbox: bool,
    /// Can pause for human approval before continuing.
    pub supports_approval_gates: bool,
    /// Can be bound to a dedicated Git worktree.
    pub supports_worktree: bool,
    /// Maximum context window in tokens.
    pub max_context_tokens: u32,
    /// Input cost in USD per 1 000 tokens.
    pub cost_per_1k_tokens_in: f64,
    /// Output cost in USD per 1 000 tokens.
    pub cost_per_1k_tokens_out: f64,
    /// Preferred speed tier for this provider's default routing.
    /// Codex defaults to `Fast`; Claude defaults to `Full`.
    pub preferred_speed: ProviderSpeed,
}

impl ProviderCapabilities {
    /// Capabilities for Claude Code (Sonnet pricing).
    pub fn claude() -> Self {
        Self {
            provider: Provider::Claude,
            supports_fork: true,
            supports_resume: true,
            supports_mcp: true,
            // Claude Code has no built-in sandbox; the daemon provides isolation.
            supports_sandbox: false,
            supports_approval_gates: true,
            supports_worktree: true,
            max_context_tokens: 200_000,
            cost_per_1k_tokens_in: 3.0 / 1000.0,
            cost_per_1k_tokens_out: 15.0 / 1000.0,
            preferred_speed: ProviderSpeed::Full,
        }
    }

    /// Capabilities for Codex CLI.
    pub fn codex() -> Self {
        Self {
            provider: Provider::Codex,
            supports_fork: true,
            supports_resume: true,
            // Codex exposes `codex mcp-server`.
            supports_mcp: true,
            // Codex has built-in network/filesystem sandboxing.
            supports_sandbox: true,
            supports_approval_gates: true,
            supports_worktree: true,
            max_context_tokens: 128_000,
            cost_per_1k_tokens_in: 1.5 / 1000.0,
            cost_per_1k_tokens_out: 6.0 / 1000.0,
            // Codex Fast (codex-spark) is the default; Full (gpt-5.3-codex) is
            // selected when the role is Planner or Implementer.
            preferred_speed: ProviderSpeed::Fast,
        }
    }

    /// Return capabilities for any `Provider` variant.
    pub fn for_provider(provider: &Provider) -> Self {
        match provider {
            Provider::Claude => Self::claude(),
            Provider::Codex => Self::codex(),
            Provider::Unknown(_) => Self {
                provider: provider.clone(),
                supports_fork: false,
                supports_resume: false,
                supports_mcp: false,
                supports_sandbox: false,
                supports_approval_gates: false,
                supports_worktree: false,
                max_context_tokens: 4096,
                cost_per_1k_tokens_in: 0.0,
                cost_per_1k_tokens_out: 0.0,
                preferred_speed: ProviderSpeed::Full,
            },
        }
    }
}

// ─── Simple role-based recommendation ────────────────────────────────────────

/// Given a task role and rough complexity, recommend the best provider.
///
/// | role | rationale |
/// |------|-----------|
/// | `router` | fast + cheap → Codex |
/// | `reviewer` | cross-model by default (see `select_provider`) → Codex |
/// | `planner` (high) | deep reasoning → Claude |
/// | `implementer` | best code generation → Claude |
/// | `qa` | tool-driven + Codex sandbox → Codex |
pub fn recommend_provider(role: &str, complexity: &str) -> Provider {
    match (role, complexity) {
        ("router", _) => Provider::Codex,
        ("reviewer", _) => Provider::Codex,
        ("planner", "high") => Provider::Claude,
        ("implementer", _) => Provider::Claude,
        ("qa", _) => Provider::Codex,
        _ => Provider::Claude,
    }
}

// ─── SelectionContext ─────────────────────────────────────────────────────────

/// All inputs needed to pick a provider for an agent role.
#[derive(Debug, Clone)]
pub struct SelectionContext {
    /// One of: `"router"`, `"planner"`, `"implementer"`, `"reviewer"`, `"qa"`.
    pub role: String,
    /// One of: `"low"`, `"medium"`, `"high"`.
    pub complexity: String,
    /// Optional cost ceiling for this selection (USD).  Not enforced yet —
    /// reserved for future cost-aware routing.
    pub cost_budget_usd: Option<f64>,
    /// The providers that are currently available (have valid credentials, etc.).
    pub available_providers: Vec<Provider>,
    /// The provider used by the immediately preceding agent in the pipeline.
    /// When present and `role == "reviewer"`, we prefer a *different* provider
    /// for cross-model verification.
    pub previous_provider: Option<Provider>,
}

/// Select the best provider given a `SelectionContext`.
///
/// Cross-model rule: if the role is `"reviewer"` and the previous agent used
/// Claude, prefer Codex (and vice-versa) so the review comes from a different
/// model than the implementation.
pub fn select_provider(ctx: &SelectionContext) -> Provider {
    // Cross-model reviewer: use a different provider than the implementer.
    if ctx.role == "reviewer" {
        if let Some(Provider::Claude) = &ctx.previous_provider {
            if ctx.available_providers.contains(&Provider::Codex) {
                return Provider::Codex;
            }
        }
        if let Some(Provider::Codex) = &ctx.previous_provider {
            if ctx.available_providers.contains(&Provider::Claude) {
                return Provider::Claude;
            }
        }
    }

    // Otherwise use the simple role-based recommendation.
    let recommended = recommend_provider(&ctx.role, &ctx.complexity);
    if ctx.available_providers.contains(&recommended) {
        return recommended;
    }

    // Fall back to the first available provider, defaulting to Claude.
    ctx.available_providers
        .first()
        .cloned()
        .unwrap_or(Provider::Claude)
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// Tests live in capabilities/tests.rs.
#[cfg(test)]
mod tests;
