/**
 * Purpose: SSE event stream client for real-time session events from the daemon.
 * Inputs:  sessionId, auth token (retrieved via Tauri command)
 * Outputs: EventSource wrapping GET /api/v1/sessions/{id}/events
 * Constraints: port 4301; token fetched from daemon_status command
 * SPORT: T-E1-07
 */

import { daemonStatus } from "./tauriApi";

const REST_BASE = "http://127.0.0.1:4301/api/v1";

// ── Event types emitted by the SSE stream ─────────────────────────────────────

export type SessionEventType =
  | "message_start"
  | "message_delta"
  | "message_stop"
  | "tool_use"
  | "tool_result"
  | "error";

export interface SessionEvent {
  type: SessionEventType;
  session_id: string;
  data: unknown;
}

// ── SSE stream subscription ───────────────────────────────────────────────────

/**
 * Subscribe to a daemon session's event stream.
 * Returns an unsubscribe function.
 */
export function subscribeToSession(
  sessionId: string,
  token: string,
  onEvent: (event: SessionEvent) => void,
  onError?: (err: Event) => void
): () => void {
  // EventSource doesn't support Authorization header, so we pass token as query param.
  const url = `${REST_BASE}/sessions/${sessionId}/events?token=${encodeURIComponent(token)}`;
  const es = new EventSource(url);

  es.onmessage = (e) => {
    try {
      const event = JSON.parse(e.data) as SessionEvent;
      onEvent(event);
    } catch {
      // ignore malformed frames
    }
  };

  if (onError) {
    es.onerror = onError;
  }

  return () => es.close();
}

// ── One-shot REST helpers (direct fetch, bypasses Tauri invoke) ───────────────

/** Fetch via REST with a bearer token. Used for streaming and ad-hoc calls. */
export async function restFetch<T>(
  path: string,
  token: string,
  options?: RequestInit
): Promise<T> {
  const resp = await fetch(`${REST_BASE}${path}`, {
    ...options,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      ...(options?.headers ?? {}),
    },
  });
  if (!resp.ok) {
    throw new Error(`REST ${path} → ${resp.status} ${resp.statusText}`);
  }
  return resp.json() as Promise<T>;
}

export { daemonStatus };
