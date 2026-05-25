/**
 * Purpose: Shared TypeScript types mirroring the clawd daemon JSON shapes.
 * Inputs:  clawd REST API responses (port 4301) + WS event shapes (port 4300)
 * Outputs: Type-safe DTOs for stores, hooks, and components
 * SPORT: T-E1-07
 */

export type SessionStatus =
  | "idle"
  | "running"
  | "paused"
  | "completed"
  | "error"
  | "cancelled";

export interface Session {
  id: string;
  title: string | null;
  status: SessionStatus;
  created_at: string;
  updated_at: string;
  project_path?: string;
}

export interface Message {
  id: string;
  session_id: string;
  role: "user" | "assistant" | "tool";
  content: string;
  tool_name?: string;
  tool_input?: unknown;
  tool_output?: unknown;
  created_at: string;
  tokens?: number;
}

export interface DaemonStatus {
  running: boolean;
  has_token: boolean;
  port_ws: number;
  port_rest: number;
}

export interface HealthResponse {
  ok: boolean;
  version: string;
}

export interface Metrics {
  session_count: number;
  total_tokens: number;
  uptime_seconds: number;
}

export interface MemoryEntry {
  key: string;
  value: string;
  scope: string;
  created_at: string;
}

export interface Project {
  id: string;
  name: string;
  path: string;
  created_at: string;
}

export type NavRoute =
  | "chat"
  | "sessions"
  | "files"
  | "git"
  | "dashboard"
  | "search"
  | "packs"
  | "doctor"
  | "instructions"
  | "settings";

export interface DoctorCheck {
  name: string;
  ok: boolean;
  message?: string;
}

export interface DoctorResult {
  healthy: boolean;
  checks: DoctorCheck[];
}
