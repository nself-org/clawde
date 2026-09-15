//! Behavioural tests for span construction and the daemon metric counters.
//!
//! Same discipline as `storage::tests` and `session::tests`: written to kill
//! mutants, not to move a coverage number.
//!
//! The counter tests lean on one specific trick. `snapshot()` copies five
//! atomics into five struct fields, so a mutant that reads the *wrong* atomic
//! into a field is invisible whenever the counters happen to be equal — and
//! they are all equal at zero. Every counter is therefore driven to a
//! **distinct** value (1, 2, 3, 4, 5) before the snapshot is asserted, so any
//! crossed wire changes an observed number. Each `inc_*` is separately asserted
//! to leave the other four alone.

use super::*;

// ─── id helpers ─────────────────────────────────────────────────────────────

#[test]
fn new_trace_id_is_32_lowercase_hex_chars() {
    let id = new_trace_id();
    assert_eq!(id.len(), 32, "a trace id is a simple-form uuid: {id}");
    assert!(
        id.chars()
            .all(|c| c.is_ascii_hexdigit() && !c.is_uppercase()),
        "trace id must be lowercase hex, got {id}"
    );
    assert!(
        !id.contains('-'),
        "simple form must not be hyphenated, got {id}"
    );
}

#[test]
fn new_span_id_is_16_hex_chars_and_zero_padded() {
    // `format!("{:016x}")` — the padding is the part a mutant drops, so assert
    // the exact width across many draws rather than a single lucky one.
    for _ in 0..200 {
        let id = new_span_id();
        assert_eq!(id.len(), 16, "span id must be exactly 16 hex chars: {id}");
        assert!(
            id.chars()
                .all(|c| c.is_ascii_hexdigit() && !c.is_uppercase()),
            "span id must be lowercase hex, got {id}"
        );
    }
}

#[test]
fn ids_are_distinct_across_calls() {
    let traces: std::collections::HashSet<String> = (0..100).map(|_| new_trace_id()).collect();
    assert_eq!(traces.len(), 100, "trace ids must not repeat");

    let spans: std::collections::HashSet<String> = (0..100).map(|_| new_span_id()).collect();
    assert_eq!(spans.len(), 100, "span ids must not repeat");
}

#[test]
fn epoch_ms_is_a_plausible_wall_clock_in_milliseconds() {
    let t = epoch_ms();
    // 2020-01-01 and 2100-01-01 in ms. This is wide on purpose: it catches a
    // unit error (seconds or nanos) or a zero, not clock skew.
    assert!(
        t > 1_577_836_800_000 && t < 4_102_444_800_000,
        "epoch_ms looks wrong for a millisecond clock: {t}"
    );
}

// ─── span construction ──────────────────────────────────────────────────────

#[test]
fn start_session_span_sets_the_root_span_shape_and_every_attribute() {
    let g = start_session_span("sess-1", "claude", Some("task-9"), "ihash", "phash");

    assert_eq!(g.span.name, "clawd.session.run");
    assert_eq!(
        g.span.parent_span_id, None,
        "a session span is the trace root and has no parent"
    );
    assert_eq!(g.span.trace_id.len(), 32);
    assert_eq!(g.span.span_id.len(), 16);
    assert!(
        g.span.duration_ms.is_none(),
        "a started span has no duration"
    );
    assert!(matches!(g.span.status, SpanStatus::Running));

    // Each attribute asserted by exact value, so a mutant writing the wrong
    // source into a key dies.
    assert_eq!(g.span.attributes.get("session_id").unwrap(), "sess-1");
    assert_eq!(g.span.attributes.get("provider").unwrap(), "claude");
    assert_eq!(g.span.attributes.get("instruction_hash").unwrap(), "ihash");
    assert_eq!(g.span.attributes.get("policy_hash").unwrap(), "phash");
    assert_eq!(g.span.attributes.get("task_id").unwrap(), "task-9");
    assert_eq!(g.span.attributes.len(), 5);
}

#[test]
fn start_session_span_omits_task_id_when_absent() {
    let g = start_session_span("sess-1", "claude", None, "ihash", "phash");
    assert!(
        !g.span.attributes.contains_key("task_id"),
        "a None task_id must leave the key out, not write an empty string"
    );
    assert_eq!(g.span.attributes.len(), 4);
}

#[test]
fn start_phase_span_inherits_the_trace_and_parents_onto_the_session_span() {
    let root = start_session_span("sess-1", "claude", None, "i", "p");
    let phase = start_phase_span(&root, "build");

    assert_eq!(phase.span.name, "clawd.phase.build");
    assert_eq!(
        phase.span.trace_id, root.span.trace_id,
        "a child span must stay in the parent's trace"
    );
    assert_eq!(
        phase.span.parent_span_id.as_deref(),
        Some(root.span.span_id.as_str()),
        "the parent link must point at the parent's span id"
    );
    assert_ne!(
        phase.span.span_id, root.span.span_id,
        "a child must get its own span id"
    );
    assert_eq!(phase.span.attributes.get("phase").unwrap(), "build");
    assert!(matches!(phase.span.status, SpanStatus::Running));
}

#[test]
fn start_tool_span_records_decision_and_path() {
    let g = start_tool_span(
        "trace-abc",
        "span-parent",
        "Read",
        Some("/etc/hosts"),
        "allow",
        "agent-7",
    );

    assert_eq!(g.span.name, "clawd.tool.Read");
    assert_eq!(g.span.trace_id, "trace-abc");
    assert_eq!(g.span.parent_span_id.as_deref(), Some("span-parent"));
    assert_eq!(g.span.attributes.get("decision").unwrap(), "allow");
    assert_eq!(g.span.attributes.get("from_agent_id").unwrap(), "agent-7");
    assert_eq!(g.span.attributes.get("path").unwrap(), "/etc/hosts");
    assert!(
        !g.span.attributes.contains_key("error.type"),
        "an allowed call must not be tagged as policy_denied"
    );
}

#[test]
fn start_tool_span_omits_path_when_absent() {
    let g = start_tool_span("t", "s", "Bash", None, "allow", "agent-1");
    assert!(
        !g.span.attributes.contains_key("path"),
        "a None path must leave the key out"
    );
}

#[test]
fn start_tool_span_tags_only_a_deny_decision_as_policy_denied() {
    // Both directions, so neither an always-tag nor a never-tag mutant lives.
    let denied = start_tool_span("t", "s", "Bash", None, "deny", "agent-1");
    assert_eq!(
        denied.span.attributes.get("error.type").unwrap(),
        "policy_denied"
    );

    for decision in ["allow", "ask", "DENY", ""] {
        let g = start_tool_span("t", "s", "Bash", None, decision, "agent-1");
        assert!(
            !g.span.attributes.contains_key("error.type"),
            "decision {decision:?} must not be tagged policy_denied — the check is an exact match on \"deny\""
        );
    }
}

#[test]
fn start_test_run_span_uses_the_fixed_verify_name_and_keeps_the_command() {
    let g = start_test_run_span("trace-1", "span-1", "cargo test --lib");
    assert_eq!(g.span.name, "clawd.verify.tests");
    assert_eq!(g.span.trace_id, "trace-1");
    assert_eq!(g.span.parent_span_id.as_deref(), Some("span-1"));
    assert_eq!(
        g.span.attributes.get("command").unwrap(),
        "cargo test --lib"
    );
}

// ─── SpanGuard finish ───────────────────────────────────────────────────────

#[test]
fn set_attr_adds_and_overwrites_attributes() {
    let mut g = start_session_span("s", "claude", None, "i", "p");
    g.set_attr("custom", "one");
    assert_eq!(g.span.attributes.get("custom").unwrap(), "one");

    g.set_attr("custom", "two");
    assert_eq!(
        g.span.attributes.get("custom").unwrap(),
        "two",
        "setting the same key twice must overwrite"
    );
}

#[test]
fn finish_marks_ok_records_a_duration_and_calls_the_recorder_once() {
    use std::sync::{Arc, Mutex};

    let seen: Arc<Mutex<Vec<Span>>> = Arc::new(Mutex::new(Vec::new()));
    let sink = Arc::clone(&seen);

    let mut g = start_session_span("s", "claude", None, "i", "p");
    g.recorder = Some(Box::new(move |span| sink.lock().unwrap().push(span)));

    g.finish();

    let recorded = seen.lock().unwrap();
    assert_eq!(
        recorded.len(),
        1,
        "the recorder must be invoked exactly once"
    );
    let span = &recorded[0];
    assert!(
        matches!(span.status, SpanStatus::Ok),
        "finish() must mark the span Ok"
    );
    assert!(
        span.duration_ms.is_some(),
        "finish() must record a duration"
    );
    assert!(
        !span.attributes.contains_key("error.message"),
        "a successful span must carry no error message"
    );
}

#[test]
fn finish_with_error_marks_error_and_attaches_the_message() {
    use std::sync::{Arc, Mutex};

    let seen: Arc<Mutex<Vec<Span>>> = Arc::new(Mutex::new(Vec::new()));
    let sink = Arc::clone(&seen);

    let mut g = start_session_span("s", "claude", None, "i", "p");
    g.recorder = Some(Box::new(move |span| sink.lock().unwrap().push(span)));

    g.finish_with_error("provider timed out");

    let recorded = seen.lock().unwrap();
    assert_eq!(recorded.len(), 1);
    let span = &recorded[0];
    assert!(
        matches!(span.status, SpanStatus::Error),
        "finish_with_error() must mark the span Error, not Ok"
    );
    assert!(span.duration_ms.is_some());
    assert_eq!(
        span.attributes.get("error.message").unwrap(),
        "provider timed out"
    );
}

// ─── DaemonMetrics ──────────────────────────────────────────────────────────

#[test]
fn a_new_metrics_set_starts_at_zero_on_every_counter() {
    let snap = DaemonMetrics::new().snapshot();
    assert_eq!(snap.sessions_total, 0);
    assert_eq!(snap.tools_allowed_total, 0);
    assert_eq!(snap.tools_denied_total, 0);
    assert_eq!(snap.tests_passed_total, 0);
    assert_eq!(snap.tests_failed_total, 0);
}

#[test]
fn snapshot_maps_each_counter_to_its_own_field() {
    // Distinct values are the whole point: at equal values a crossed field is
    // invisible. 1/2/3/4/5 makes any swap change an observed number.
    let m = DaemonMetrics::new();
    m.inc_sessions();
    for _ in 0..2 {
        m.inc_tools_allowed();
    }
    for _ in 0..3 {
        m.inc_tools_denied();
    }
    for _ in 0..4 {
        m.inc_tests_passed();
    }
    for _ in 0..5 {
        m.inc_tests_failed();
    }

    let snap = m.snapshot();
    assert_eq!(snap.sessions_total, 1);
    assert_eq!(snap.tools_allowed_total, 2);
    assert_eq!(snap.tools_denied_total, 3);
    assert_eq!(snap.tests_passed_total, 4);
    assert_eq!(snap.tests_failed_total, 5);
}

#[test]
fn each_increment_advances_exactly_one_counter_by_exactly_one() {
    // A mutant that increments the wrong counter, or increments by 0 or 2, dies
    // on one of these five cases.
    type Inc = (
        &'static str,
        fn(&DaemonMetrics),
        fn(&MetricsSnapshot) -> u64,
    );
    let cases: [Inc; 5] = [
        ("sessions", DaemonMetrics::inc_sessions, |s| {
            s.sessions_total
        }),
        ("tools_allowed", DaemonMetrics::inc_tools_allowed, |s| {
            s.tools_allowed_total
        }),
        ("tools_denied", DaemonMetrics::inc_tools_denied, |s| {
            s.tools_denied_total
        }),
        ("tests_passed", DaemonMetrics::inc_tests_passed, |s| {
            s.tests_passed_total
        }),
        ("tests_failed", DaemonMetrics::inc_tests_failed, |s| {
            s.tests_failed_total
        }),
    ];

    for (name, inc, read) in cases {
        let m = DaemonMetrics::new();
        inc(&m);
        let snap = m.snapshot();

        assert_eq!(read(&snap), 1, "{name} must advance by exactly one");

        let total = snap.sessions_total
            + snap.tools_allowed_total
            + snap.tools_denied_total
            + snap.tests_passed_total
            + snap.tests_failed_total;
        assert_eq!(
            total, 1,
            "incrementing {name} must leave the other four counters untouched"
        );
    }
}

#[test]
fn counters_accumulate_rather_than_overwrite() {
    let m = DaemonMetrics::new();
    for _ in 0..10 {
        m.inc_sessions();
    }
    assert_eq!(m.snapshot().sessions_total, 10);
}

// ─── persist_span ───────────────────────────────────────────────────────────

#[tokio::test]
async fn persist_span_round_trips_a_finished_span_into_sqlite() {
    let dir = tempfile::tempdir().expect("tempdir");
    let storage = crate::storage::Storage::new(dir.path())
        .await
        .expect("open storage");

    let mut span = start_session_span("sess-42", "claude", Some("t-1"), "ih", "ph").span;
    span.duration_ms = Some(1234);
    span.status = SpanStatus::Ok;

    persist_span(&storage, &span).await.expect("persist span");

    let row: (
        String,
        Option<String>,
        String,
        String,
        i64,
        Option<i64>,
        String,
    ) = sqlx::query_as(
        "SELECT span_id, parent_span_id, trace_id, name, started_at_ms, duration_ms, status \
         FROM telemetry_spans WHERE span_id = ?",
    )
    .bind(&span.span_id)
    .fetch_one(storage.pool())
    .await
    .expect("span should have been written");

    assert_eq!(row.0, span.span_id);
    assert_eq!(row.1, None, "a root span persists a NULL parent");
    assert_eq!(row.2, span.trace_id);
    assert_eq!(row.3, "clawd.session.run");
    assert_eq!(row.4, span.started_at_ms as i64);
    assert_eq!(
        row.5,
        Some(1234),
        "the duration must survive the round trip"
    );
    assert_eq!(row.6, "ok", "status is stored lowercased");

    // INSERT OR IGNORE: persisting the same span twice must not duplicate it or
    // raise, which is what makes a retry safe.
    persist_span(&storage, &span)
        .await
        .expect("re-persist span");
    let count: (i64,) = sqlx::query_as("SELECT COUNT(*) FROM telemetry_spans WHERE span_id = ?")
        .bind(&span.span_id)
        .fetch_one(storage.pool())
        .await
        .expect("count");
    assert_eq!(count.0, 1, "re-persisting must be idempotent");
}
