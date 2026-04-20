//! Daily metrics aggregation from trace files.
//!
//! Reads the active `traces.jsonl` and produces per-day summaries of task
//! completions, cost, latency, errors, and approvals.

use std::path::Path;

use anyhow::{Context, Result};
use chrono::NaiveDate;
use serde::{Deserialize, Serialize};

use super::schema::{TraceEvent, TraceKind};

// ─── DailyMetrics ────────────────────────────────────────────────────────────

/// Aggregated metrics for a single calendar day.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DailyMetrics {
    /// The calendar date these metrics cover.
    pub date: NaiveDate,
    /// Number of tasks that transitioned to a completed state.
    pub tasks_completed: u64,
    /// Sum of estimated cost in USD across all provider events.
    pub total_cost_usd: f64,
    /// Average latency in milliseconds across all timed events.
    pub avg_latency_ms: u64,
    /// Number of events where `ok == false`.
    pub error_count: u64,
    /// Number of approval events (requested + granted + denied combined).
    pub approval_count: u64,
}

// ─── Aggregation ─────────────────────────────────────────────────────────────

/// Aggregate metrics from the active trace file for the given calendar date.
///
/// Reads the entire `traces.jsonl` in memory.  Suitable for a single file up to
/// 50 MB; larger archives should be pre-filtered.
pub async fn aggregate_daily(data_dir: &Path, date: NaiveDate) -> Result<DailyMetrics> {
    let file_path = data_dir.join("telemetry").join("traces.jsonl");

    let content = match tokio::fs::read_to_string(&file_path).await {
        Ok(c) => c,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            return Ok(DailyMetrics {
                date,
                ..Default::default()
            });
        }
        Err(e) => return Err(e).context("read traces.jsonl for metrics"),
    };

    aggregate_from_lines(content.lines(), date)
}

/// Pure function for easier testing — accepts an iterator of raw JSONL lines.
pub fn aggregate_from_lines<'a>(
    lines: impl Iterator<Item = &'a str>,
    date: NaiveDate,
) -> Result<DailyMetrics> {
    let mut metrics = DailyMetrics {
        date,
        ..Default::default()
    };
    let mut latency_sum: u64 = 0;
    let mut latency_count: u64 = 0;

    for line in lines {
        if line.is_empty() {
            continue;
        }
        let event: TraceEvent = match serde_json::from_str(line) {
            Ok(e) => e,
            Err(_) => continue,
        };

        // Filter to the requested date (UTC).
        if event.ts.date_naive() != date {
            continue;
        }

        // Cost accumulation.
        if let Some(cost) = event.cost_usd {
            metrics.total_cost_usd += cost;
        }

        // Error counting.
        if !event.ok {
            metrics.error_count += 1;
        }

        // Latency.
        if let Some(ms) = event.latency_ms {
            latency_sum += ms;
            latency_count += 1;
        }

        // Task completions — count transitions that end in "completed".
        if matches!(event.kind, TraceKind::TaskTransition) {
            if let Some(ref tool) = event.tool {
                if tool.ends_with("completed") || tool.ends_with("done") {
                    metrics.tasks_completed += 1;
                }
            }
        }

        // Approval events.
        if matches!(
            event.kind,
            TraceKind::ApprovalRequested | TraceKind::ApprovalGranted | TraceKind::ApprovalDenied
        ) {
            metrics.approval_count += 1;
        }
    }

    metrics.avg_latency_ms = latency_sum.checked_div(latency_count).unwrap_or(0);

    Ok(metrics)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn empty_lines_returns_zero_metrics() {
        let date = NaiveDate::from_ymd_opt(2026, 2, 23).unwrap();
        let result = aggregate_from_lines(std::iter::empty(), date).unwrap();
        assert_eq!(result.tasks_completed, 0);
        assert_eq!(result.total_cost_usd, 0.0);
        assert_eq!(result.error_count, 0);
    }
}
