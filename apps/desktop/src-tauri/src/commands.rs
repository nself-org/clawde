// commands.rs — Tauri command handlers bridging the React frontend to the clawd daemon.
//
// Purpose: Expose daemon REST (port 4301) and WebSocket IPC (port 4300) to the
//          frontend via Tauri invoke(). All heavy logic stays in the daemon.
// Inputs:  Tauri AppHandle + per-command arguments
// Outputs: JSON-serialisable response structs or string errors
// Constraints: auth token from token file; daemon must be running on 4300/4301
// SPORT: T-E1-07 — Tauri 2 migration

use crate::daemon::DaemonState;
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, State};

const REST_BASE: &str = "http://127.0.0.1:4301/api/v1";

// ── Shared HTTP client ────────────────────────────────────────────────────────

fn client() -> reqwest::Client {
    reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(30))
        .build()
        .expect("failed to build reqwest client")
}

// ── Response types ────────────────────────────────────────────────────────────

#[derive(Debug, Serialize, Deserialize)]
pub struct SessionSummary {
    pub id: String,
    pub title: Option<String>,
    pub status: String,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct CreateSessionRequest {
    pub project_path: String,
    pub title: Option<String>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct SubmitTaskRequest {
    pub message: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct HealthResponse {
    pub ok: bool,
    pub version: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct MetricsResponse {
    pub session_count: u32,
    pub total_tokens: u64,
    pub uptime_seconds: u64,
}

// ── Helper: read daemon auth token ───────────────────────────────────────────

fn read_token(state: &State<DaemonState>) -> Result<String, String> {
    state
        .token()
        .ok_or_else(|| "daemon not ready — token unavailable".to_string())
}

// ── Commands ──────────────────────────────────────────────────────────────────

/// Check daemon health.
#[tauri::command]
pub async fn health_check(state: State<'_, DaemonState>) -> Result<HealthResponse, String> {
    let token = read_token(&state)?;
    let resp = client()
        .get(format!("{REST_BASE}/health"))
        .bearer_auth(&token)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json::<serde_json::Value>()
        .await
        .map_err(|e| e.to_string())?;
    Ok(HealthResponse {
        ok: resp["ok"].as_bool().unwrap_or(false),
        version: resp["version"]
            .as_str()
            .unwrap_or("unknown")
            .to_string(),
    })
}

/// List all sessions from the daemon.
#[tauri::command]
pub async fn list_sessions(
    state: State<'_, DaemonState>,
) -> Result<Vec<SessionSummary>, String> {
    let token = read_token(&state)?;
    let resp = client()
        .get(format!("{REST_BASE}/sessions"))
        .bearer_auth(&token)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json::<Vec<SessionSummary>>()
        .await
        .map_err(|e| e.to_string())?;
    Ok(resp)
}

/// Get a single session by ID.
#[tauri::command]
pub async fn get_session(
    id: String,
    state: State<'_, DaemonState>,
) -> Result<serde_json::Value, String> {
    let token = read_token(&state)?;
    let resp = client()
        .get(format!("{REST_BASE}/sessions/{id}"))
        .bearer_auth(&token)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json::<serde_json::Value>()
        .await
        .map_err(|e| e.to_string())?;
    Ok(resp)
}

/// Create a new session.
#[tauri::command]
pub async fn create_session(
    request: CreateSessionRequest,
    state: State<'_, DaemonState>,
) -> Result<SessionSummary, String> {
    let token = read_token(&state)?;
    let resp = client()
        .post(format!("{REST_BASE}/sessions"))
        .bearer_auth(&token)
        .json(&request)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json::<SessionSummary>()
        .await
        .map_err(|e| e.to_string())?;
    Ok(resp)
}

/// Submit a task (message) to a session.
#[tauri::command]
pub async fn submit_task(
    session_id: String,
    request: SubmitTaskRequest,
    state: State<'_, DaemonState>,
) -> Result<serde_json::Value, String> {
    let token = read_token(&state)?;
    let resp = client()
        .post(format!("{REST_BASE}/sessions/{session_id}/tasks"))
        .bearer_auth(&token)
        .json(&request)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json::<serde_json::Value>()
        .await
        .map_err(|e| e.to_string())?;
    Ok(resp)
}

/// Get daemon metrics (token usage, session count, uptime).
#[tauri::command]
pub async fn get_metrics(state: State<'_, DaemonState>) -> Result<MetricsResponse, String> {
    let token = read_token(&state)?;
    let resp = client()
        .get(format!("{REST_BASE}/metrics"))
        .bearer_auth(&token)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json::<serde_json::Value>()
        .await
        .map_err(|e| e.to_string())?;
    Ok(MetricsResponse {
        session_count: resp["session_count"].as_u64().unwrap_or(0) as u32,
        total_tokens: resp["total_tokens"].as_u64().unwrap_or(0),
        uptime_seconds: resp["uptime_seconds"].as_u64().unwrap_or(0),
    })
}

/// Get memory entries from the daemon.
#[tauri::command]
pub async fn get_memory(state: State<'_, DaemonState>) -> Result<serde_json::Value, String> {
    let token = read_token(&state)?;
    let resp = client()
        .get(format!("{REST_BASE}/memory"))
        .bearer_auth(&token)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json::<serde_json::Value>()
        .await
        .map_err(|e| e.to_string())?;
    Ok(resp)
}

/// Return the current daemon state (running, stopped, token present).
#[tauri::command]
pub async fn daemon_status(state: State<'_, DaemonState>) -> Result<serde_json::Value, String> {
    Ok(serde_json::json!({
        "running": state.is_running(),
        "has_token": state.token().is_some(),
        "port_ws": 4300,
        "port_rest": 4301,
    }))
}

/// Open a file/directory chooser and return the selected path.
#[tauri::command]
pub async fn pick_project_folder(app: AppHandle) -> Result<Option<String>, String> {
    use tauri_plugin_dialog::DialogExt;
    let path = app.dialog().file().pick_folder().await;
    Ok(path.map(|p| p.to_string_lossy().to_string()))
}
