use anyhow::Result;
use std::path::{Path, PathBuf};
use tokio::{fs::OpenOptions, io::AsyncWriteExt, sync::Mutex};

/// Append-only JSONL event log for a session.
///
/// The file handle is opened lazily on first write and cached for the session
/// lifetime to avoid the overhead of opening the file on every event.
pub struct EventLog {
    path: PathBuf,
    file: Mutex<Option<tokio::fs::File>>,
}

impl EventLog {
    pub fn new(data_dir: &Path, session_id: &str) -> Self {
        let path = data_dir
            .join("sessions")
            .join(format!("{}.jsonl", session_id));
        Self {
            path,
            file: Mutex::new(None),
        }
    }

    pub async fn append(&self, event: &serde_json::Value) -> Result<()> {
        let mut guard = self.file.lock().await;
        if guard.is_none() {
            if let Some(parent) = self.path.parent() {
                tokio::fs::create_dir_all(parent).await?;
            }
            let f = OpenOptions::new()
                .create(true)
                .append(true)
                .open(&self.path)
                .await?;
            *guard = Some(f);
        }
        let file = guard.as_mut().unwrap();
        let line = serde_json::to_string(event)? + "\n";
        file.write_all(line.as_bytes()).await?;

        // `write_all` on a tokio File can return before the bytes reach the OS:
        // the write is handed to the blocking pool and may still be in flight.
        // tokio's own docs say "a file will not be closed immediately when it
        // goes out of scope if there are any IO operations that have not yet
        // completed", so without this an event that `append` reported as
        // written could be missing when another process tails the log, and a
        // log dropped at session end could lose its last events entirely.
        //
        // `flush` (not `sync_all`) is the right level here: it guarantees the
        // bytes are with the OS and visible to other readers, without paying
        // for an fsync on every event.
        file.flush().await?;
        Ok(())
    }
}

// Kept in its own file so the tests do not need to be read alongside the
// implementation.
#[cfg(test)]
mod tests;
