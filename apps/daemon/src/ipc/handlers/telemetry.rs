//! RPC handlers for observability / trace data.
//!
//! Registered methods:
//!   - `traces.query`   — query recent trace events
//!   - `traces.summary` — aggregate daily metrics

use anyhow::Result;
use chrono::{DateTime, Utc};
use serde_json::{json, Value};

use crate::telemetry::metrics::aggregate_daily;
use crate::telemetry::traces::TracesWriter;
use crate::AppContext;

// ─── traces.query ─────────────────────────────────────────────────────────────

/// Query trace events from the active JSONL file.
///
/// Params (all optional):
/// ```json
/// {
///   "task_id": "string",
///   "since":   "ISO-8601 datetime string",
///   "limit":   1000
/// }
/// ```
pub async fn query_traces(params: Value, ctx: &AppContext) -> Result<Value> {
    let task_id = params
        .get("task_id")
        .and_then(|v| v.as_str())
        .map(String::from);
    let since: Option<DateTime<Utc>> = params
        .get("since")
        .and_then(|v| v.as_str())
        .and_then(|s| s.parse::<DateTime<Utc>>().ok());
    let limit = params
        .get("limit")
        .and_then(|v| v.as_u64())
        .map(|n| n as usize);

    let writer = get_writer(ctx)?;
    let events = writer.query(task_id.as_deref(), since, limit).await?;

    Ok(json!({
        "events": events,
        "count": events.len(),
    }))
}

// ─── traces.summary ───────────────────────────────────────────────────────────

/// Return aggregated daily metrics for the last N days.
///
/// Params (all optional):
/// ```json
/// { "days": 7 }
/// ```
pub async fn summary(params: Value, ctx: &AppContext) -> Result<Value> {
    let days = params.get("days").and_then(|v| v.as_u64()).unwrap_or(7) as i64;

    let data_dir = &ctx.config.data_dir;
    let today = Utc::now().date_naive();

    let mut daily_metrics = Vec::new();
    let mut total_cost: f64 = 0.0;
    let mut total_tasks: u64 = 0;

    for offset in 0..days {
        let date = today - chrono::Duration::days(offset);
        match aggregate_daily(data_dir, date).await {
            Ok(m) => {
                total_cost += m.total_cost_usd;
                total_tasks += m.tasks_completed;
                daily_metrics.push(m);
            }
            Err(e) => {
                tracing::warn!(date = %date, err = %e, "traces.summary: failed to aggregate day");
            }
        }
    }

    // Most recent day first.
    daily_metrics.sort_by_key(|b| std::cmp::Reverse(b.date));

    Ok(json!({
        "daily_metrics": daily_metrics,
        "total_cost_usd": total_cost,
        "total_tasks": total_tasks,
        "period_days": days,
    }))
}

// ─── Private helpers ──────────────────────────────────────────────────────────

/// Retrieve the shared `TracesWriter` from `AppContext`, or open a transient one.
///
/// Currently the daemon wires a `TracesWriter` into `AppContext` at startup.
/// Until that plumbing exists, fall back to opening a short-lived writer.
fn get_writer(ctx: &AppContext) -> Result<std::sync::Arc<TracesWriter>> {
    // The writer is stored in AppContext once the telemetry traces module is
    // integrated into the startup sequence.  For now, create a per-request
    // writer from the data directory.  This is safe because `TracesWriter`
    // opens the file in append mode.
    let data_dir = ctx.config.data_dir.clone();
    // We need a sync wrapper; use a oneshot to bridge the async constructor.
    // Since this fn is called from an already-async context, use block_in_place.
    let writer = tokio::task::block_in_place(|| {
        tokio::runtime::Handle::current().block_on(TracesWriter::new(&data_dir))
    })?;
    Ok(std::sync::Arc::new(writer))
}
