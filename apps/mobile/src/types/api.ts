/**
 * Purpose: Shared API types matching ClawDE Rust daemon JSON responses.
 * Inputs:  Daemon WebSocket / HTTP responses on port 4300.
 * Outputs: Typed models for all app layers.
 * Constraints: Must stay in sync with daemon's JSON-RPC protocol.
 * SPORT: T-E1-06 — React Native Expo migration
 */

export type SessionStatus = 'running' | 'paused' | 'completed' | 'error';

export interface Session {
  id: string;
  repoPath: string;
  status: SessionStatus;
  provider?: string;
  createdAt: string;
  updatedAt: string;
  metadata?: Record<string, unknown>;
}

export type ToolCallStatus = 'pending' | 'approved' | 'rejected' | 'completed' | 'error';

export interface ToolCall {
  id: string;
  sessionId: string;
  messageId?: string;
  tool: string;
  input: Record<string, unknown>;
  output?: string;
  status: ToolCallStatus;
  createdAt: string;
}

export type MessageRole = 'user' | 'assistant' | 'system';

export interface FileEdit {
  path: string;
  operation: 'create' | 'edit' | 'delete';
  linesAdded: number;
  linesRemoved: number;
  diff?: string;
}

export interface Message {
  id: string;
  sessionId: string;
  role: MessageRole;
  content: string;
  metadata: {
    files?: FileEdit[];
    [key: string]: unknown;
  };
  createdAt: string;
}

export type AgentStatus = 'active' | 'idle';

export interface Agent {
  agentId: string;
  agentType: string;
  status: AgentStatus;
}

export type TaskStatus = 'pending' | 'in_progress' | 'done' | 'blocked' | 'cancelled';

export interface AgentTask {
  id: string;
  title: string;
  description?: string;
  status: TaskStatus;
  repoPath: string;
  createdAt: string;
  updatedAt: string;
  notes?: string;
}

export interface DashboardStats {
  total: number;
  byStatus: Record<TaskStatus, number>;
}

export interface ActivityEntry {
  id: string;
  type: string;
  description: string;
  sessionId?: string;
  taskId?: string;
  timestamp: string;
}

export interface DaemonHost {
  id: string;
  name: string;
  url: string;
  isPaired: boolean;
}

export type ConnectionMode = 'lan' | 'relay' | 'offline';

export interface DaemonPushEvent {
  method: string;
  params?: Record<string, unknown>;
}

export interface DaemonInfo {
  version: string;
  platform: string;
}
