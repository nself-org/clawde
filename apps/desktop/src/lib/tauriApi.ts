/**
 * Purpose: Typed wrappers over Tauri invoke() for all daemon commands.
 * Inputs:  Arguments per command; token/auth managed by Rust layer
 * Outputs: Promise-based typed responses matching clawd REST shapes
 * Constraints: All calls must succeed in dev mode (mocked) + prod (real Tauri)
 * SPORT: T-E1-07
 */

import { invoke } from "@tauri-apps/api/core";
import type {
  DaemonStatus,
  HealthResponse,
  Metrics,
  OAuthAccount,
  Session,
  MemoryEntry,
} from "@/types";

// ── Session commands ──────────────────────────────────────────────────────────

export async function listSessions(): Promise<Session[]> {
  return invoke<Session[]>("list_sessions");
}

export async function getSession(id: string): Promise<Session> {
  return invoke<Session>("get_session", { id });
}

export async function createSession(
  projectPath: string,
  title?: string
): Promise<Session> {
  return invoke<Session>("create_session", {
    request: { project_path: projectPath, title: title ?? null },
  });
}

export async function submitTask(
  sessionId: string,
  message: string
): Promise<void> {
  return invoke("submit_task", {
    sessionId,
    request: { message },
  });
}

// ── Daemon info commands ───────────────────────────────────────────────────────

export async function healthCheck(): Promise<HealthResponse> {
  return invoke<HealthResponse>("health_check");
}

export async function daemonStatus(): Promise<DaemonStatus> {
  return invoke<DaemonStatus>("daemon_status");
}

export async function getMetrics(): Promise<Metrics> {
  return invoke<Metrics>("get_metrics");
}

export async function getMemory(): Promise<MemoryEntry[]> {
  return invoke<MemoryEntry[]>("get_memory");
}

// ── Utility commands ──────────────────────────────────────────────────────────

export async function pickProjectFolder(): Promise<string | null> {
  return invoke<string | null>("pick_project_folder");
}

// ── OAuth account commands ─────────────────────────────────────────────────────

/** List all OAuth accounts registered in the daemon. */
export async function listOAuthAccounts(): Promise<OAuthAccount[]> {
  return invoke<OAuthAccount[]>("list_oauth_accounts");
}

/**
 * Initiate the OAuth flow for a provider.
 * The daemon opens a system browser popup and handles the callback.
 * Returns the newly added account on success.
 */
export async function addOAuthAccount(
  provider: string
): Promise<OAuthAccount> {
  return invoke<OAuthAccount>("add_oauth_account", { provider });
}

/** Remove an OAuth account by ID. */
export async function removeOAuthAccount(id: string): Promise<void> {
  return invoke("remove_oauth_account", { id });
}
