// SPDX-License-Identifier: MIT
//! Analytics storage — reads from existing SQLite tables and manages the
//! `analytics_achievements` table (created on first use).
//!
//! No mandatory migration file is needed: the achievements table is created
//! via `CREATE TABLE IF NOT EXISTS` on the first call that touches it.

use anyhow::{Context as _, Result};
use chrono::Utc;
use sqlx::SqlitePool;
use std::collections::HashMap;

use super::model::{
    Achievement, DailyCount, PersonalAnalytics, ProviderBreakdown, SessionAnalytics,
};

/// Analytics query + write layer.
pub struct AnalyticsStorage {
    pool: SqlitePool,
}

impl AnalyticsStorage {
    pub fn new(pool: SqlitePool) -> Self {
        Self { pool }
    }

    // ─── Schema bootstrap ─────────────────────────────────────────────────────

    /// Ensure the `analytics_achievements` table exists.
    /// Called lazily on first achievement access so it does not block startup.
    async fn ensure_achievements_table(&self) -> Result<()> {
        sqlx::query(
            "CREATE TABLE IF NOT EXISTS analytics_achievements (
                id          TEXT PRIMARY KEY,
                unlocked_at TEXT NOT NULL
            )",
        )
        .execute(&self.pool)
        .await
        .context("create analytics_achievements table")?;
        Ok(())
    }

    // ─── Personal Analytics ───────────────────────────────────────────────────

    /// Compute personal usage analytics from `from` (ISO 8601) to now.
    ///
    /// Aggregates session counts, message counts, and provider usage from the
    /// `sessions` and `messages` tables. Lines written and language breakdown
    /// are not yet available from SQLite alone (they require git diff parsing),
    /// so they are returned as zero-values — the Flutter layer can enhance them
    /// via `repo.diff` calls if desired.
    pub async fn get_personal_analytics(&self, from: &str) -> Result<PersonalAnalytics> {
        // Daily session counts for the last 30 days.
        let rows: Vec<(String, i64)> = sqlx::query_as(
            "SELECT date(created_at) AS day, COUNT(*) AS cnt
               FROM sessions
              WHERE created_at >= ?
           GROUP BY day
           ORDER BY day ASC",
        )
        .bind(from)
        .fetch_all(&self.pool)
        .await
        .context("daily session counts")?;

        let sessions_per_day: Vec<DailyCount> = rows
            .into_iter()
            .map(|(date, count)| DailyCount {
                date,
                count: count as u64,
            })
            .collect();

        // Total session count in the window.
        let total_sessions: i64 =
            sqlx::query_scalar("SELECT COUNT(*) FROM sessions WHERE created_at >= ?")
                .bind(from)
                .fetch_one(&self.pool)
                .await
                .context("total session count")?;

        // Total sessions with at least one AI message (provider is not empty).
        let ai_sessions: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM sessions WHERE created_at >= ? AND provider != ''",
        )
        .bind(from)
        .fetch_one(&self.pool)
        .await
        .context("AI session count")?;

        let ai_assist_percent = if total_sessions > 0 {
            (ai_sessions as f32 / total_sessions as f32) * 100.0
        } else {
            0.0
        };

        Ok(PersonalAnalytics {
            // Lines written and language breakdown require git diff parsing;
            // they are computed in the Flutter layer using `repo.diff` results.
            lines_written: 0,
            ai_assist_percent,
            languages: HashMap::new(),
            sessions_per_day,
        })
    }

    // ─── Provider Breakdown ───────────────────────────────────────────────────

    /// Return per-provider session + token + cost breakdown from `from` to now.
    pub async fn get_provider_breakdown(&self, from: &str) -> Result<Vec<ProviderBreakdown>> {
        // Session counts per provider.
        let session_rows: Vec<(String, i64)> = sqlx::query_as(
            "SELECT COALESCE(routed_provider, provider) AS prov, COUNT(*) AS cnt
               FROM sessions
              WHERE created_at >= ?
           GROUP BY prov",
        )
        .bind(from)
        .fetch_all(&self.pool)
        .await
        .context("provider session counts")?;

        let mut map: HashMap<String, ProviderBreakdown> = HashMap::new();
        for (provider, count) in session_rows {
            if provider.is_empty() {
                continue;
            }
            map.entry(provider.clone())
                .or_insert(ProviderBreakdown {
                    provider,
                    sessions: 0,
                    tokens: 0,
                    cost_usd: 0.0,
                    win_rate: None,
                })
                .sessions = count as u64;
        }

        // Token totals per provider from token_usage table (if it exists).
        // The table may not exist on older daemons — we use a best-effort query.
        let token_rows: Vec<(String, i64, f64)> = sqlx::query_as(
            "SELECT provider, SUM(input_tokens + output_tokens) AS tokens,
                    SUM(estimated_cost_usd) AS cost
               FROM token_usage
              WHERE recorded_at >= ?
           GROUP BY provider",
        )
        .bind(from)
        .fetch_all(&self.pool)
        .await
        .unwrap_or_default();

        for (provider, tokens, cost) in token_rows {
            let entry = map.entry(provider.clone()).or_insert(ProviderBreakdown {
                provider,
                sessions: 0,
                tokens: 0,
                cost_usd: 0.0,
                win_rate: None,
            });
            entry.tokens = tokens as u64;
            entry.cost_usd = cost;
        }

        let mut result: Vec<ProviderBreakdown> = map.into_values().collect();
        result.sort_by_key(|b| std::cmp::Reverse(b.sessions));
        Ok(result)
    }

    // ─── Session Analytics ────────────────────────────────────────────────────

    /// Return per-session analytics for the given session.
    pub async fn get_session_analytics(&self, session_id: &str) -> Result<SessionAnalytics> {
        let row: (String, String, String, i64) = sqlx::query_as(
            "SELECT id, provider, created_at, message_count FROM sessions WHERE id = ?",
        )
        .bind(session_id)
        .fetch_one(&self.pool)
        .await
        .context("session not found for analytics")?;

        let (id, provider, created_at_str, message_count) = row;

        // Compute duration from created_at to now.
        let duration_secs = chrono::DateTime::parse_from_rfc3339(&created_at_str)
            .map(|t| (Utc::now() - t.with_timezone(&Utc)).num_seconds().max(0) as u64)
            .unwrap_or(0);

        Ok(SessionAnalytics {
            session_id: id,
            duration_secs,
            message_count: message_count as u64,
            provider,
            lines_written: 0, // Enhanced by Flutter via repo.diff
        })
    }

    // ─── Achievements ─────────────────────────────────────────────────────────

    /// Return all defined achievements, with unlock status from the DB.
    pub async fn list_achievements(&self) -> Result<Vec<Achievement>> {
        self.ensure_achievements_table().await?;

        // Load unlocked rows.
        let unlocked: Vec<(String, String)> =
            sqlx::query_as("SELECT id, unlocked_at FROM analytics_achievements")
                .fetch_all(&self.pool)
                .await
                .context("load achievements")?;

        let mut unlock_map: HashMap<String, String> = unlocked.into_iter().collect();

        let definitions = super::achievements::all_definitions();
        let result = definitions
            .into_iter()
            .map(|(id, name, description)| {
                let unlocked_at = unlock_map.remove(id);
                Achievement {
                    id: id.to_string(),
                    name: name.to_string(),
                    description: description.to_string(),
                    unlocked: unlocked_at.is_some(),
                    unlocked_at,
                }
            })
            .collect();

        Ok(result)
    }

    /// Unlock an achievement by ID. No-op if already unlocked.
    /// Returns `true` if this was a new unlock (not previously unlocked).
    pub async fn unlock_achievement(&self, id: &str) -> Result<bool> {
        self.ensure_achievements_table().await?;

        let now = Utc::now().to_rfc3339();
        let rows_affected = sqlx::query(
            "INSERT OR IGNORE INTO analytics_achievements (id, unlocked_at) VALUES (?, ?)",
        )
        .bind(id)
        .bind(&now)
        .execute(&self.pool)
        .await
        .context("unlock achievement")?
        .rows_affected();

        Ok(rows_affected > 0)
    }

    /// Return the total number of sessions created since the daemon was installed.
    pub async fn total_session_count(&self) -> Result<u64> {
        let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM sessions")
            .fetch_one(&self.pool)
            .await
            .context("total session count")?;
        Ok(count as u64)
    }
}
