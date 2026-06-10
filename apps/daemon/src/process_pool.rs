// SPDX-License-Identifier: MIT
//! Process Pool — maintains pre-warmed CLI processes for fast cold session resume.
//!
//! Pool workers are initialized (Node.js/CLI runtime loaded) but have no conversation
//! context. When a cold session resumes, a pool worker is assigned and context is fed
//! from SQLite, reducing resume latency from ~3-5s to ~1.5-2.5s.

use std::collections::VecDeque;
use std::sync::Arc;
use tokio::sync::Mutex;
use tracing::{debug, info};

/// State of a pool worker process.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WorkerState {
    /// Warming up — CLI process is being initialized.
    Warming,
    /// Ready — initialized and SIGSTOP'd, available for assignment.
    Ready,
    /// Assigned — being used for a session resume.
    Assigned,
}

/// A pre-warmed CLI process held in the pool.
#[derive(Debug)]
pub struct PoolWorker {
    pub pid: u32,
    pub state: WorkerState,
}

/// Manages a pool of pre-warmed CLI processes.
pub struct ProcessPool {
    workers: Mutex<VecDeque<PoolWorker>>,
    target_size: usize,
    cli_binary: String,
}

impl ProcessPool {
    pub fn new(target_size: usize, cli_binary: impl Into<String>) -> Arc<Self> {
        Arc::new(Self {
            workers: Mutex::new(VecDeque::new()),
            target_size,
            cli_binary: cli_binary.into(),
        })
    }

    /// Attempt to acquire a ready pool worker.
    /// Returns the PID if a worker is available, or None if pool is empty.
    pub async fn acquire(&self) -> Option<u32> {
        let mut workers = self.workers.lock().await;
        // Find a ready worker
        if let Some(pos) = workers.iter().position(|w| w.state == WorkerState::Ready) {
            let mut worker = workers.remove(pos).unwrap();
            worker.state = WorkerState::Assigned;
            let pid = worker.pid;
            debug!(pid, "pool worker acquired");
            return Some(pid);
        }
        None
    }

    /// Release a worker back to the pool (or remove it if the process has exited).
    pub async fn release(&self, pid: u32) {
        let mut workers = self.workers.lock().await;
        // Check if process is still alive
        if is_process_alive(pid) {
            workers.push_back(PoolWorker {
                pid,
                state: WorkerState::Ready,
            });
            debug!(pid, "pool worker released back to pool");
        } else {
            debug!(
                pid,
                "pool worker process has exited — not returning to pool"
            );
        }
    }

    /// Get current pool size (ready workers only).
    pub async fn ready_count(&self) -> usize {
        self.workers
            .lock()
            .await
            .iter()
            .filter(|w| w.state == WorkerState::Ready)
            .count()
    }

    /// Get total worker count (all states).
    pub async fn total_count(&self) -> usize {
        self.workers.lock().await.len()
    }

    /// Spawn a replacement worker in the background after one is assigned.
    /// This keeps the pool at `target_size`.
    pub async fn replenish(self: &Arc<Self>) {
        let ready = self.ready_count().await;
        let total = self.total_count().await;
        if ready < self.target_size && total < self.target_size * 2 {
            info!(
                "replenishing process pool (ready={ready}, target={})",
                self.target_size
            );
            // Process pool replenishment is deferred to the E4 Build wave.
            // Full implementation spawns a CLI process and SIGSTOP's it for fast session
            // resume. Requires provider integration and PTY allocation (P1-E4-W8-S08-T01).
        }
    }

    /// Expose the configured CLI binary name (for diagnostics).
    pub fn cli_binary(&self) -> &str {
        &self.cli_binary
    }
}

/// Check if a process with the given PID is alive.
#[cfg(unix)]
fn is_process_alive(pid: u32) -> bool {
    // POSIX: kill(pid, 0) returns 0 if process exists and we have permission
    let result = unsafe { libc::kill(pid as libc::pid_t, 0) };
    result == 0
}

/// Windows implementation: probe liveness via OpenProcess with
/// PROCESS_QUERY_LIMITED_INFORMATION (0x1000). A null handle means the
/// process has exited or the PID was never valid; close the handle immediately
/// after the check to avoid resource leaks.
///
/// This replaces the prior conservative `true` stub (PTY pool zombie risk —
/// see E4 PTY allocation spec, SPORT REGISTRY-FUNCTIONS.md, and
/// `.claude/phases/current/p1/evidence/pty-permission-stub-audit.md`).
#[cfg(windows)]
fn is_process_alive(pid: u32) -> bool {
    use windows_sys::Win32::Foundation::CloseHandle;
    use windows_sys::Win32::System::Threading::{OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION};
    // SAFETY: Win32 API call. OpenProcess returns NULL on failure (PID gone or
    // permission denied); we treat both as "not alive" — the process is unusable
    // either way. We close the handle immediately on success to avoid leaks.
    unsafe {
        let handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
        if handle.is_null() {
            return false;
        }
        CloseHandle(handle);
        true
    }
}

/// Fallback for non-Unix, non-Windows platforms (e.g. wasm32, bare-metal targets).
/// Returns true (conservative) — these targets are not supported for PTY allocation.
/// Windows and Unix each have accurate implementations above.
#[cfg(not(any(unix, windows)))]
fn is_process_alive(_pid: u32) -> bool {
    true
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_pool_empty_acquire() {
        let pool = ProcessPool::new(1, "claude");
        // Empty pool — acquire returns None
        assert_eq!(pool.acquire().await, None);
    }

    #[tokio::test]
    async fn test_pool_ready_count() {
        let pool = ProcessPool::new(2, "claude");
        assert_eq!(pool.ready_count().await, 0);
        assert_eq!(pool.total_count().await, 0);
    }
}
