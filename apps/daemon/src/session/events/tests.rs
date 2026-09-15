//! Behavioural tests for the per-session JSONL event log.
//!
//! The contract that matters is *append-only*: the file handle is opened once
//! and cached for the session lifetime, so the bug this guards against is a
//! second write truncating the first. Every test that writes more than once
//! reads the whole file back and counts lines, rather than checking only that
//! the last event is present.

use super::*;

/// Read the log file back as parsed JSON lines.
fn read_lines(path: &Path) -> Vec<serde_json::Value> {
    let raw = std::fs::read_to_string(path).expect("read event log");
    raw.lines()
        .filter(|l| !l.is_empty())
        .map(|l| serde_json::from_str(l).expect("each line must be valid JSON"))
        .collect()
}

#[test]
fn new_places_the_log_at_sessions_slash_session_id_dot_jsonl() {
    let log = EventLog::new(Path::new("/data"), "sess-1");
    assert_eq!(log.path, PathBuf::from("/data/sessions/sess-1.jsonl"));
}

#[tokio::test]
async fn append_creates_the_parent_directory_and_writes_one_json_line() {
    let dir = tempfile::tempdir().expect("tempdir");
    let log = EventLog::new(dir.path(), "sess-1");

    // The sessions/ directory does not exist yet — append must create it
    // rather than failing on the first event of a session.
    assert!(!dir.path().join("sessions").exists());

    log.append(&serde_json::json!({"type": "start", "n": 1}))
        .await
        .expect("append");

    let path = dir.path().join("sessions").join("sess-1.jsonl");
    assert!(path.exists(), "the log file must have been created");

    let lines = read_lines(&path);
    assert_eq!(lines.len(), 1);
    assert_eq!(lines[0]["type"], "start");
    assert_eq!(lines[0]["n"], 1);
}

#[tokio::test]
async fn append_is_append_only_across_many_events() {
    let dir = tempfile::tempdir().expect("tempdir");
    let log = EventLog::new(dir.path(), "sess-1");

    for i in 0..5 {
        log.append(&serde_json::json!({"n": i}))
            .await
            .expect("append");
    }

    let path = dir.path().join("sessions").join("sess-1.jsonl");
    let lines = read_lines(&path);

    // Counting is the point: a truncating write leaves exactly one line, which
    // a "the last event is present" assertion would happily accept.
    assert_eq!(lines.len(), 5, "every event must be retained");
    for (i, line) in lines.iter().enumerate() {
        assert_eq!(line["n"], i, "events must stay in write order");
    }
}

#[tokio::test]
async fn append_reopens_and_still_appends_to_an_existing_file() {
    let dir = tempfile::tempdir().expect("tempdir");

    // First log instance writes two events, then drops — closing its handle.
    {
        let log = EventLog::new(dir.path(), "sess-1");
        log.append(&serde_json::json!({"n": 0}))
            .await
            .expect("append");
        log.append(&serde_json::json!({"n": 1}))
            .await
            .expect("append");
    }

    // A fresh instance for the same session must extend the file, not replace
    // it — this is what a daemon restart mid-session looks like.
    {
        let log = EventLog::new(dir.path(), "sess-1");
        log.append(&serde_json::json!({"n": 2}))
            .await
            .expect("append");
    }

    let path = dir.path().join("sessions").join("sess-1.jsonl");
    let lines = read_lines(&path);
    assert_eq!(
        lines.len(),
        3,
        "reopening the log must append, not truncate"
    );
    assert_eq!(lines[0]["n"], 0);
    assert_eq!(lines[2]["n"], 2);
}

#[tokio::test]
async fn each_session_writes_to_its_own_file() {
    let dir = tempfile::tempdir().expect("tempdir");

    let a = EventLog::new(dir.path(), "sess-a");
    let b = EventLog::new(dir.path(), "sess-b");
    a.append(&serde_json::json!({"who": "a"}))
        .await
        .expect("append");
    b.append(&serde_json::json!({"who": "b"}))
        .await
        .expect("append");

    let la = read_lines(&dir.path().join("sessions").join("sess-a.jsonl"));
    let lb = read_lines(&dir.path().join("sessions").join("sess-b.jsonl"));

    assert_eq!(la.len(), 1);
    assert_eq!(lb.len(), 1);
    assert_eq!(la[0]["who"], "a", "sessions must not cross-contaminate");
    assert_eq!(lb[0]["who"], "b");
}

#[tokio::test]
async fn append_writes_exactly_one_newline_terminated_line_per_event() {
    let dir = tempfile::tempdir().expect("tempdir");
    let log = EventLog::new(dir.path(), "sess-1");

    // A nested value would break JSONL if the serializer ever pretty-printed,
    // so assert the raw byte shape rather than only the parsed result.
    log.append(&serde_json::json!({"a": {"b": [1, 2, 3]}}))
        .await
        .expect("append");
    log.append(&serde_json::json!({"a": {"b": [4]}}))
        .await
        .expect("append");

    let path = dir.path().join("sessions").join("sess-1.jsonl");
    let raw = std::fs::read_to_string(&path).expect("read");

    assert_eq!(
        raw.matches('\n').count(),
        2,
        "one newline per event, got:\n{raw}"
    );
    assert!(raw.ends_with('\n'), "the file must end with a newline");
    assert!(
        !raw.contains("\n  "),
        "events must be compact JSON, not pretty-printed: {raw}"
    );

    let lines = read_lines(&path);
    assert_eq!(lines.len(), 2);
    assert_eq!(lines[0]["a"]["b"][2], 3, "nested values must round-trip");
}
