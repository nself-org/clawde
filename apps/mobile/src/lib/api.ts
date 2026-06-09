/**
 * Purpose: Typed API façade over the daemon WebSocket client.
 * Inputs:  daemonClient instance; typed method names and params.
 * Outputs: Strongly-typed results for sessions, messages, tool calls, tasks.
 * Constraints: Port 4300 default; all calls are JSON-RPC 2.0 over WebSocket.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import { daemonClient } from './daemon';
import type {
  Session,
  Message,
  ToolCall,
  Agent,
  AgentTask,
  DashboardStats,
  ActivityEntry,
  DaemonInfo,
} from '../types/api';

// ── Sessions ────────────────────────────────────────────────────────────────

export async function listSessions(): Promise<Session[]> {
  return daemonClient.call<Session[]>('session.list');
}

export async function createSession(repoPath: string): Promise<Session> {
  return daemonClient.call<Session>('session.create', { repoPath });
}

export async function pauseSession(sessionId: string): Promise<void> {
  await daemonClient.call('session.pause', { sessionId });
}

export async function resumeSession(sessionId: string): Promise<void> {
  await daemonClient.call('session.resume', { sessionId });
}

export async function closeSession(sessionId: string): Promise<void> {
  await daemonClient.call('session.close', { sessionId });
}

export async function cancelSession(sessionId: string): Promise<void> {
  await daemonClient.call('session.cancel', { sessionId });
}

// ── Messages ────────────────────────────────────────────────────────────────

export async function listMessages(
  sessionId: string,
  opts: { before?: string; limit?: number } = {},
): Promise<Message[]> {
  return daemonClient.call<Message[]>('message.list', { sessionId, ...opts });
}

export async function sendMessage(sessionId: string, content: string): Promise<Message> {
  return daemonClient.call<Message>('message.send', { sessionId, content });
}

// ── Tool calls ──────────────────────────────────────────────────────────────

export async function listToolCalls(sessionId: string): Promise<ToolCall[]> {
  return daemonClient.call<ToolCall[]>('toolCall.list', { sessionId });
}

export async function approveToolCall(toolCallId: string): Promise<void> {
  await daemonClient.call('toolCall.approve', { toolCallId });
}

export async function rejectToolCall(toolCallId: string): Promise<void> {
  await daemonClient.call('toolCall.reject', { toolCallId });
}

// ── Tasks / Agents ──────────────────────────────────────────────────────────

export async function listTasks(repoPath: string): Promise<AgentTask[]> {
  return daemonClient.call<AgentTask[]>('task.list', { repoPath });
}

export async function updateTaskStatus(
  taskId: string,
  status: string,
  notes?: string,
): Promise<void> {
  await daemonClient.call('task.updateStatus', { taskId, status, notes });
}

export async function getDashboardStats(repoPath: string): Promise<DashboardStats> {
  return daemonClient.call<DashboardStats>('task.dashboardStats', { repoPath });
}

export async function listActivityFeed(repoPath: string): Promise<ActivityEntry[]> {
  return daemonClient.call<ActivityEntry[]>('task.activityFeed', { repoPath });
}

export async function listAgents(repoPath: string): Promise<Agent[]> {
  return daemonClient.call<Agent[]>('agent.list', { repoPath });
}

// ── Daemon info ─────────────────────────────────────────────────────────────

export async function getDaemonInfo(): Promise<DaemonInfo> {
  return daemonClient.call<DaemonInfo>('daemon.info');
}
