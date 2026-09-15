// session/telemetry.rs — OpenTelemetry tracing + metrics (Sprint ZZ OT.T02-T07)
//
// Feature-gated: compile with `--features tracing` to enable OTel export.
// Without the feature, spans are no-ops (zero overhead in production builds
// that don't need OTel).
//
// Environment:
//   OTEL_EXPORTER_OTLP_ENDPOINT — enables OTLP export to any OTel collector.
//   Unset = no export, no overhead.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::time::{Duration, Instant};

// ─── Span types ───────────────────────────────────────────────────────────────

/// A lightweight in-process span for recording timing + attributes.
/// Stored in SQLite for `clawd observe` queries (no external collector needed).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Span {
    pub span_id: String,
    pub parent_span_id: Option<String>,
    pub trace_id: String,
    pub name: String,
    pub attributes: HashMap<String, String>,
    pub started_at_ms: u64,
    pub duration_ms: Option<u64>,
    pub status: SpanStatus,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SpanStatus {
    Running,
    Ok,
    Error,
}

/// A started span guard — records duration when dropped.
pub struct SpanGuard {
    pub span: Span,
    start: Instant,
    /// If Some, records the span to storage on finish.
    recorder: Option<SpanRecorder>,
}

pub type SpanRecorder = Box<dyn Fn(Span) + Send + 'static>;

impl SpanGuard {
    pub fn finish(mut self) {
        let duration = self.start.elapsed();
        self.span.duration_ms = Some(duration.as_millis() as u64);
        self.span.status = SpanStatus::Ok;
        if let Some(rec) = self.recorder.take() {
            rec(self.span);
        }
    }

    pub fn finish_with_error(mut self, error: &str) {
        let duration = self.start.elapsed();
        self.span.duration_ms = Some(duration.as_millis() as u64);
        self.span.status = SpanStatus::Error;
        self.span
            .attributes
            .insert("error.message".to_string(), error.to_string());
        if let Some(rec) = self.recorder.take() {
            rec(self.span);
        }
    }

    pub fn set_attr(&mut self, key: &str, val: &str) {
        self.span
            .attributes
            .insert(key.to_string(), val.to_string());
    }
}

// ─── Telemetry context ────────────────────────────────────────────────────────

/// OT.T02 — Root span per session run: `clawd.session.run`
pub fn start_session_span(
    session_id: &str,
    provider: &str,
    task_id: Option<&str>,
    instruction_hash: &str,
    policy_hash: &str,
) -> SpanGuard {
    let trace_id = new_trace_id();
    let span_id = new_span_id();

    let mut attributes = HashMap::new();
    attributes.insert("session_id".to_string(), session_id.to_string());
    attributes.insert("provider".to_string(), provider.to_string());
    attributes.insert("instruction_hash".to_string(), instruction_hash.to_string());
    attributes.insert("policy_hash".to_string(), policy_hash.to_string());
    if let Some(tid) = task_id {
        attributes.insert("task_id".to_string(), tid.to_string());
    }

    SpanGuard {
        span: Span {
            span_id,
            parent_span_id: None,
            trace_id,
            name: "clawd.session.run".to_string(),
            attributes,
            started_at_ms: epoch_ms(),
            duration_ms: None,
            status: SpanStatus::Running,
        },
        start: Instant::now(),
        recorder: None,
    }
}

/// OT.T02 — Child phase span: `clawd.phase.{name}`
pub fn start_phase_span(parent: &SpanGuard, phase_name: &str) -> SpanGuard {
    let span_id = new_span_id();
    let mut attributes = HashMap::new();
    attributes.insert("phase".to_string(), phase_name.to_string());

    SpanGuard {
        span: Span {
            span_id,
            parent_span_id: Some(parent.span.span_id.clone()),
            trace_id: parent.span.trace_id.clone(),
            name: format!("clawd.phase.{phase_name}"),
            attributes,
            started_at_ms: epoch_ms(),
            duration_ms: None,
            status: SpanStatus::Running,
        },
        start: Instant::now(),
        recorder: None,
    }
}

/// OT.T03 — Tool call span: `clawd.tool.{name}`
pub fn start_tool_span(
    parent_trace_id: &str,
    parent_span_id: &str,
    tool_name: &str,
    path: Option<&str>,
    decision: &str,
    from_agent_id: &str,
) -> SpanGuard {
    let span_id = new_span_id();
    let mut attributes = HashMap::new();
    attributes.insert("decision".to_string(), decision.to_string());
    attributes.insert("from_agent_id".to_string(), from_agent_id.to_string());
    if let Some(p) = path {
        attributes.insert("path".to_string(), p.to_string());
    }
    if decision == "deny" {
        attributes.insert("error.type".to_string(), "policy_denied".to_string());
    }

    SpanGuard {
        span: Span {
            span_id,
            parent_span_id: Some(parent_span_id.to_string()),
            trace_id: parent_trace_id.to_string(),
            name: format!("clawd.tool.{tool_name}"),
            attributes,
            started_at_ms: epoch_ms(),
            duration_ms: None,
            status: SpanStatus::Running,
        },
        start: Instant::now(),
        recorder: None,
    }
}

/// OT.T04 — Test run span: `clawd.verify.tests`
pub fn start_test_run_span(
    parent_trace_id: &str,
    parent_span_id: &str,
    command: &str,
) -> SpanGuard {
    let span_id = new_span_id();
    let mut attributes = HashMap::new();
    attributes.insert("command".to_string(), command.to_string());

    SpanGuard {
        span: Span {
            span_id,
            parent_span_id: Some(parent_span_id.to_string()),
            trace_id: parent_trace_id.to_string(),
            name: "clawd.verify.tests".to_string(),
            attributes,
            started_at_ms: epoch_ms(),
            duration_ms: None,
            status: SpanStatus::Running,
        },
        start: Instant::now(),
        recorder: None,
    }
}

// ─── OT.T07 — Metrics counters ───────────────────────────────────────────────

/// In-process metrics counters.
/// Exported via OTLP metrics API when `OTEL_EXPORTER_OTLP_ENDPOINT` is set.
#[derive(Debug, Default)]
pub struct DaemonMetrics {
    pub sessions_total: std::sync::atomic::AtomicU64,
    pub tools_allowed_total: std::sync::atomic::AtomicU64,
    pub tools_denied_total: std::sync::atomic::AtomicU64,
    pub tests_passed_total: std::sync::atomic::AtomicU64,
    pub tests_failed_total: std::sync::atomic::AtomicU64,
}

impl DaemonMetrics {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn inc_sessions(&self) {
        self.sessions_total
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }

    pub fn inc_tools_allowed(&self) {
        self.tools_allowed_total
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }

    pub fn inc_tools_denied(&self) {
        self.tools_denied_total
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }

    pub fn inc_tests_passed(&self) {
        self.tests_passed_total
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }

    pub fn inc_tests_failed(&self) {
        self.tests_failed_total
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }

    pub fn snapshot(&self) -> MetricsSnapshot {
        MetricsSnapshot {
            sessions_total: self
                .sessions_total
                .load(std::sync::atomic::Ordering::Relaxed),
            tools_allowed_total: self
                .tools_allowed_total
                .load(std::sync::atomic::Ordering::Relaxed),
            tools_denied_total: self
                .tools_denied_total
                .load(std::sync::atomic::Ordering::Relaxed),
            tests_passed_total: self
                .tests_passed_total
                .load(std::sync::atomic::Ordering::Relaxed),
            tests_failed_total: self
                .tests_failed_total
                .load(std::sync::atomic::Ordering::Relaxed),
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MetricsSnapshot {
    pub sessions_total: u64,
    pub tools_allowed_total: u64,
    pub tools_denied_total: u64,
    pub tests_passed_total: u64,
    pub tests_failed_total: u64,
}

// ─── OT.T06 — SQLite span storage for `clawd observe` ────────────────────────

/// Store a completed span in SQLite for later retrieval by `clawd observe`.
pub async fn persist_span(storage: &crate::storage::Storage, span: &Span) -> anyhow::Result<()> {
    let attrs_json = serde_json::to_string(&span.attributes)?;
    let now = epoch_ms() as i64;

    sqlx::query(
        "INSERT OR IGNORE INTO telemetry_spans \
         (span_id, parent_span_id, trace_id, name, attributes_json, \
          started_at_ms, duration_ms, status, created_at) \
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
    )
    .bind(&span.span_id)
    .bind(&span.parent_span_id)
    .bind(&span.trace_id)
    .bind(&span.name)
    .bind(&attrs_json)
    .bind(span.started_at_ms as i64)
    .bind(span.duration_ms.map(|d| d as i64))
    .bind(format!("{:?}", span.status).to_lowercase())
    .bind(now)
    .execute(storage.pool())
    .await?;

    Ok(())
}

/// Retrieve all spans for a session trace.
#[allow(clippy::type_complexity)]
pub async fn get_session_trace(
    storage: &crate::storage::Storage,
    session_id: &str,
) -> anyhow::Result<Vec<Span>> {
    // Find the trace_id for this session
    let trace_id: Option<String> = sqlx::query_scalar(
        "SELECT trace_id FROM telemetry_spans \
         WHERE name = 'clawd.session.run' \
           AND attributes_json LIKE ? \
         ORDER BY started_at_ms DESC LIMIT 1",
    )
    .bind(format!("%{}%", session_id))
    .fetch_optional(storage.pool())
    .await?;

    let trace_id = match trace_id {
        Some(t) => t,
        None => return Ok(Vec::new()),
    };

    let rows: Vec<(
        String,
        Option<String>,
        String,
        String,
        String,
        i64,
        Option<i64>,
        String,
    )> = sqlx::query_as(
        "SELECT span_id, parent_span_id, trace_id, name, attributes_json, \
             started_at_ms, duration_ms, status \
             FROM telemetry_spans WHERE trace_id = ? ORDER BY started_at_ms ASC",
    )
    .bind(&trace_id)
    .fetch_all(storage.pool())
    .await?;

    let spans = rows
        .into_iter()
        .map(
            |(
                span_id,
                parent_span_id,
                trace_id,
                name,
                attrs_json,
                started_ms,
                duration_ms,
                status,
            )| {
                let attributes: HashMap<String, String> =
                    serde_json::from_str(&attrs_json).unwrap_or_default();
                let status = match status.as_str() {
                    "error" => SpanStatus::Error,
                    "running" => SpanStatus::Running,
                    _ => SpanStatus::Ok,
                };
                Span {
                    span_id,
                    parent_span_id,
                    trace_id,
                    name,
                    attributes,
                    started_at_ms: started_ms as u64,
                    duration_ms: duration_ms.map(|d| d as u64),
                    status,
                }
            },
        )
        .collect();

    Ok(spans)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

fn new_trace_id() -> String {
    uuid::Uuid::new_v4().simple().to_string()
}

fn new_span_id() -> String {
    let id = uuid::Uuid::new_v4().to_u128_le();
    format!("{:016x}", id as u64)
}

fn epoch_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or(Duration::ZERO)
        .as_millis() as u64
}

// Kept in its own file under telemetry/: the tests are a self-contained block
// and do not need to be read alongside the span builders.
#[cfg(test)]
mod tests;
