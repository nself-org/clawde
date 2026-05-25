//! Logging initialisation for the clawd binary.
//!
//! Purpose: Set up the `tracing` subscriber (file + stdout, plain or JSON).
//! Inputs:  Log level string, optional log-file path, format string (`"pretty"` | `"json"`).
//! Outputs: An optional `WorkerGuard` that must be kept alive for the process lifetime.
//! Constraints: Must be called exactly once, before any `tracing::*` calls.

/// Initialize the tracing subscriber.
/// If `log_file` is set, logs go to both stdout and a daily-rolling file.
/// Returns a `WorkerGuard` that must stay alive for the process lifetime.
///
/// `log_format` may be `"pretty"` (default, human-readable compact format) or
/// `"json"` (structured JSON for log aggregators like Loki/Elasticsearch).
///
/// If the log directory cannot be created, falls back to stdout-only logging
/// with a warning — never panics.
pub fn setup_logging(
    log_level: &str,
    log_file: Option<&std::path::Path>,
    log_format: &str,
) -> Option<tracing_appender::non_blocking::WorkerGuard> {
    use tracing_subscriber::{fmt, layer::SubscriberExt, util::SubscriberInitExt, EnvFilter};

    let use_json = log_format == "json";

    if let Some(path) = log_file {
        let dir = path.parent().unwrap_or_else(|| std::path::Path::new("."));
        let filename = path
            .file_name()
            .unwrap_or_else(|| std::ffi::OsStr::new("clawd.log"));

        // Ensure the directory exists before tracing-appender tries to open it.
        if let Err(e) = std::fs::create_dir_all(dir) {
            // Fall back to stdout-only — don't panic on a bad log path.
            eprintln!(
                "warn: could not create log directory '{}': {e} — falling back to stdout",
                dir.display()
            );
            if use_json {
                tracing_subscriber::fmt()
                    .json()
                    .with_env_filter(log_level)
                    .init();
            } else {
                tracing_subscriber::fmt()
                    .with_env_filter(log_level)
                    .compact()
                    .init();
            }
            return None;
        }

        let appender = tracing_appender::rolling::daily(dir, filename);
        let (non_blocking, guard) = tracing_appender::non_blocking(appender);

        if use_json {
            tracing_subscriber::registry()
                .with(EnvFilter::new(log_level))
                .with(fmt::layer().json())
                .with(fmt::layer().json().with_writer(non_blocking))
                .init();
        } else {
            tracing_subscriber::registry()
                .with(EnvFilter::new(log_level))
                .with(fmt::layer().compact())
                .with(fmt::layer().with_writer(non_blocking))
                .init();
        }

        Some(guard)
    } else if use_json {
        tracing_subscriber::fmt()
            .json()
            .with_env_filter(log_level)
            .init();
        None
    } else {
        tracing_subscriber::fmt()
            .with_env_filter(log_level)
            .compact()
            .init();
        None
    }
}

/// Format uptime seconds as "2h 14m" or "45m 3s".
///
/// Purpose: Human-readable uptime string for `clawd status` output.
/// Inputs:  Elapsed seconds since daemon start.
/// Outputs: Formatted string e.g. `"2h 14m"`.
/// Constraints: Pure function, no I/O.
pub fn format_uptime(secs: u64) -> String {
    let h = secs / 3600;
    let m = (secs % 3600) / 60;
    let s = secs % 60;
    if h > 0 {
        format!("{h}h {m}m")
    } else if m > 0 {
        format!("{m}m {s}s")
    } else {
        format!("{s}s")
    }
}

/// Return all log levels at or above `min_level` (for line filtering).
///
/// Purpose: Determine which level tokens to match when filtering log lines.
/// Inputs:  Minimum level string (e.g. `"warn"`).
/// Outputs: Slice of static level strings at or above the given level.
/// Constraints: Pure, no I/O.
pub fn log_level_order(min_level: &str) -> Vec<&'static str> {
    match min_level {
        "error" => vec!["error"],
        "warn" | "warning" => vec!["warn", "error"],
        "info" => vec!["info", "warn", "error"],
        "debug" => vec!["debug", "info", "warn", "error"],
        _ => vec!["trace", "debug", "info", "warn", "error"],
    }
}
