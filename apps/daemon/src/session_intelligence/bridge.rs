// SPDX-License-Identifier: MIT
//! Cross-conversation context bridging (SI.T08).
//!
//! When a session is unhealthy (health score < 40) the session layer can
//! migrate to a fresh session while preserving the key context the model needs
//! to continue work.  This module builds that "bridge" — a compact snapshot
//! that can be injected as a system-level context message in the new session.
//!
//! ## What is bridged
//!
//!   * The original system prompt (if any).
//!   * All pinned messages.
//!   * A brief summary of the session: repo path, turn count, last user intent.
//!   * The most recent assistant message (so the new session knows where we left off).
//!
//! ## What is NOT bridged
//!
//!   * The full message history (too long).
//!   * Tool call outputs (volatile; the new session will re-run them if needed).
//!   * Internal session metadata (session ID changes by design).

use crate::storage::Storage;
use anyhow::Result;
use serde::Serialize;
use sqlx::FromRow;

/// Maximum number of pinned messages to include in a bridge.
const MAX_PINNED: usize = 10;

/// Compact context snapshot for a new session bridged from an old one.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BridgeContext {
    /// ID of the session this context was taken from.
    pub source_session_id: String,
    /// System prompt from the original session, if any.
    pub system_prompt: Option<String>,
    /// All pinned messages (up to `MAX_PINNED`), oldest first.
    pub pinned_messages: Vec<BridgeMessage>,
    /// The last message the user sent (their intent / goal).
    pub last_user_message: Option<String>,
    /// The last assistant reply (context about current progress).
    pub last_assistant_message: Option<String>,
    /// Total turns in the original session (metadata / context clue).
    pub source_turn_count: usize,
    /// Optional repo path attached to the session.
    pub repo_path: Option<String>,
}

/// A single message included in the bridge.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BridgeMessage {
    pub role: String,
    pub content: String,
}

impl BridgeContext {
    /// Build a natural-language context injection string for the new session's
    /// system prompt.
    ///
    /// The returned text is suitable for prepending to — or replacing — the
    /// system prompt of the new session.
    pub fn to_injection_text(&self) -> String {
        let mut parts: Vec<String> = Vec::new();

        parts.push(format!(
            "## Context from previous session\n\
             This session continues work from session `{}` ({} turns).",
            self.source_session_id, self.source_turn_count
        ));

        if let Some(repo) = &self.repo_path {
            parts.push(format!("**Working repository:** `{repo}`"));
        }

        if let Some(sys) = &self.system_prompt {
            parts.push(format!("**Original system prompt:**\n{sys}"));
        }

        if !self.pinned_messages.is_empty() {
            let pinned_text = self
                .pinned_messages
                .iter()
                .map(|m| format!("[{}]: {}", m.role, m.content))
                .collect::<Vec<_>>()
                .join("\n");
            parts.push(format!("**Pinned context:**\n{pinned_text}"));
        }

        if let Some(last_user) = &self.last_user_message {
            parts.push(format!("**Last user message:**\n{last_user}"));
        }

        if let Some(last_asst) = &self.last_assistant_message {
            parts.push(format!(
                "**Where the previous session left off:**\n{last_asst}"
            ));
        }

        parts.join("\n\n")
    }
}

/// Build a `BridgeContext` from the stored messages of a session.
///
/// Loads the last 200 messages from storage to find pinned messages,
/// the last user/assistant messages, and the system prompt.
/// Row type for reading messages in the bridge query.
#[derive(FromRow)]
struct MsgRow {
    role: String,
    content: String,
    pinned: i64,
}

/// Row type for reading session metadata.
#[derive(FromRow)]
struct SessionMetaRow {
    message_count: i64,
    repo_path: Option<String>,
}

pub async fn build_bridge(storage: &Storage, session_id: &str) -> Result<BridgeContext> {
    let pool = storage.clone_pool();

    // Load messages for this session in chronological order.
    let rows: Vec<MsgRow> = sqlx::query_as(
        "SELECT role, content, pinned FROM messages WHERE session_id = ?
         ORDER BY created_at ASC LIMIT 200",
    )
    .bind(session_id)
    .fetch_all(&pool)
    .await?;

    let mut system_prompt: Option<String> = None;
    let mut pinned_messages: Vec<BridgeMessage> = Vec::new();
    let mut last_user: Option<String> = None;
    let mut last_assistant: Option<String> = None;

    for row in &rows {
        match row.role.as_str() {
            "system" if system_prompt.is_none() => {
                system_prompt = Some(row.content.clone());
            }
            "system" => {}
            "user" => {
                last_user = Some(row.content.clone());
                if row.pinned != 0 && pinned_messages.len() < MAX_PINNED {
                    pinned_messages.push(BridgeMessage {
                        role: "user".to_owned(),
                        content: row.content.clone(),
                    });
                }
            }
            "assistant" => {
                last_assistant = Some(row.content.clone());
                if row.pinned != 0 && pinned_messages.len() < MAX_PINNED {
                    pinned_messages.push(BridgeMessage {
                        role: "assistant".to_owned(),
                        content: row.content.clone(),
                    });
                }
            }
            _ => {}
        }
    }

    // Retrieve session metadata for turn count + repo path.
    let session_row: Option<SessionMetaRow> =
        sqlx::query_as("SELECT message_count, repo_path FROM sessions WHERE id = ?")
            .bind(session_id)
            .fetch_optional(&pool)
            .await?;

    let (source_turn_count, repo_path) = session_row
        .map(|r| (r.message_count as usize, r.repo_path))
        .unwrap_or((rows.len(), None));

    Ok(BridgeContext {
        source_session_id: session_id.to_owned(),
        system_prompt,
        pinned_messages,
        last_user_message: last_user,
        last_assistant_message: last_assistant,
        source_turn_count,
        repo_path,
    })
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    fn make_bridge(turns: usize) -> BridgeContext {
        BridgeContext {
            source_session_id: "sess_abc".to_owned(),
            system_prompt: Some("You are a helpful assistant.".to_owned()),
            pinned_messages: vec![BridgeMessage {
                role: "user".to_owned(),
                content: "Keep this in mind: the project uses Rust.".to_owned(),
            }],
            last_user_message: Some("Now add tests.".to_owned()),
            last_assistant_message: Some("I have implemented the feature.".to_owned()),
            source_turn_count: turns,
            repo_path: Some("/Users/dev/myproject".to_owned()),
        }
    }

    #[test]
    fn test_injection_text_contains_session_id() {
        let b = make_bridge(42);
        let text = b.to_injection_text();
        assert!(text.contains("sess_abc"));
        assert!(text.contains("42 turns"));
    }

    #[test]
    fn test_injection_text_contains_repo() {
        let b = make_bridge(10);
        let text = b.to_injection_text();
        assert!(text.contains("/Users/dev/myproject"));
    }

    #[test]
    fn test_injection_text_contains_pinned() {
        let b = make_bridge(5);
        let text = b.to_injection_text();
        assert!(text.contains("uses Rust"));
    }

    #[test]
    fn test_injection_text_contains_last_messages() {
        let b = make_bridge(5);
        let text = b.to_injection_text();
        assert!(text.contains("Now add tests."));
        assert!(text.contains("I have implemented the feature."));
    }
}
