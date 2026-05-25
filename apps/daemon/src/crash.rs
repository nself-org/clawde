//! Panic hook and crash-log management for the clawd binary.
//!
//! Purpose: Install a custom panic hook that writes `crash.log` on panic (DC.T51),
//!          and check/clear that log on the next daemon startup.
//! Inputs:  `data_dir` — the daemon's data directory where `crash.log` is written.
//! Outputs: Side-effects only (panic hook registration, file writes/reads).
//! Constraints: `install_panic_hook` must be called before the Tokio runtime starts
//!              multi-threaded work; `check_crash_log` requires the tracing subscriber
//!              to already be initialised.

/// Install a custom panic hook that writes panic info + backtrace to `{data_dir}/crash.log`.
///
/// The crash log is checked and removed on the next startup (`check_crash_log`).
/// This works alongside the rollback sentinel — the sentinel handles binary corruption;
/// the crash log captures application-level panics.
pub fn install_panic_hook(data_dir: std::path::PathBuf) {
    let original = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        // Call the original hook first (prints to stderr).
        original(info);

        let crash_path = data_dir.join("crash.log");
        let msg = info
            .payload()
            .downcast_ref::<&str>()
            .copied()
            .or_else(|| info.payload().downcast_ref::<String>().map(|s| s.as_str()))
            .unwrap_or("unknown panic");

        let location = info
            .location()
            .map(|l| format!("{}:{}", l.file(), l.line()))
            .unwrap_or_else(|| "unknown location".to_string());

        let backtrace = std::backtrace::Backtrace::capture();
        let content = format!(
            "clawd panic at {location}\n\
             message: {msg}\n\
             version: {}\n\
             backtrace:\n{backtrace:#}\n",
            env!("CARGO_PKG_VERSION")
        );

        // Best-effort write — if this fails, we can't do much.
        let _ = std::fs::write(&crash_path, &content);
    }));
}

/// Check for a crash log from the previous run, log it at error level, then delete it.
///
/// Called early in `run_server()` after logging is initialized.
///
/// Purpose: Surface panics from previous runs so they appear in the current run's logs.
/// Inputs:  `data_dir` — daemon data directory.
/// Outputs: Logs the crash report at error level; deletes `crash.log`.
/// Constraints: Requires `tracing` subscriber to be initialized.
pub fn check_crash_log(data_dir: &std::path::Path) {
    let crash_path = data_dir.join("crash.log");
    match std::fs::read_to_string(&crash_path) {
        Ok(content) => {
            tracing::error!(
                crash_report = %content.trim(),
                "previous daemon run ended with a panic — see crash report above"
            );
            let _ = std::fs::remove_file(&crash_path);
        }
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
        Err(e) => {
            tracing::warn!(err = %e, "could not read crash.log");
        }
    }
}
