/**
 * Purpose: React Query hooks for daemon RPC calls (sessions, messages, tools).
 * Inputs:  daemonClient connection; session/task IDs.
 * Outputs: Typed useQuery / useMutation results with auto-invalidation.
 * Constraints: Query keys must be stable; invalidate on push events.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import * as api from '../lib/api';

// ── Query keys ──────────────────────────────────────────────────────────────

export const KEYS = {
  sessions: ['sessions'] as const,
  messages: (sessionId: string) => ['messages', sessionId] as const,
  toolCalls: (sessionId: string) => ['toolCalls', sessionId] as const,
  tasks: (repoPath: string) => ['tasks', repoPath] as const,
  stats: (repoPath: string) => ['stats', repoPath] as const,
  activity: (repoPath: string) => ['activity', repoPath] as const,
  agents: (repoPath: string) => ['agents', repoPath] as const,
};

// ── Sessions ────────────────────────────────────────────────────────────────

export function useSessions() {
  return useQuery({ queryKey: KEYS.sessions, queryFn: api.listSessions });
}

export function useCreateSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (repoPath: string) => api.createSession(repoPath),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.sessions }),
  });
}

export function usePauseSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (sessionId: string) => api.pauseSession(sessionId),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.sessions }),
  });
}

export function useResumeSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (sessionId: string) => api.resumeSession(sessionId),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.sessions }),
  });
}

export function useCloseSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (sessionId: string) => api.closeSession(sessionId),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.sessions }),
  });
}

// ── Messages ────────────────────────────────────────────────────────────────

export function useMessages(sessionId: string) {
  return useQuery({
    queryKey: KEYS.messages(sessionId),
    queryFn: () => api.listMessages(sessionId),
    enabled: Boolean(sessionId),
  });
}

export function useSendMessage(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (content: string) => api.sendMessage(sessionId, content),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.messages(sessionId) }),
  });
}

// ── Tool calls ──────────────────────────────────────────────────────────────

export function useToolCalls(sessionId: string) {
  return useQuery({
    queryKey: KEYS.toolCalls(sessionId),
    queryFn: () => api.listToolCalls(sessionId),
    enabled: Boolean(sessionId),
  });
}

export function useApproveToolCall(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (toolCallId: string) => api.approveToolCall(toolCallId),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.toolCalls(sessionId) }),
  });
}

export function useRejectToolCall(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (toolCallId: string) => api.rejectToolCall(toolCallId),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.toolCalls(sessionId) }),
  });
}

// ── Tasks ────────────────────────────────────────────────────────────────────

export function useTasks(repoPath: string) {
  return useQuery({
    queryKey: KEYS.tasks(repoPath),
    queryFn: () => api.listTasks(repoPath),
    enabled: Boolean(repoPath),
  });
}

export function useDashboardStats(repoPath: string) {
  return useQuery({
    queryKey: KEYS.stats(repoPath),
    queryFn: () => api.getDashboardStats(repoPath),
    enabled: Boolean(repoPath),
  });
}

export function useActivityFeed(repoPath: string) {
  return useQuery({
    queryKey: KEYS.activity(repoPath),
    queryFn: () => api.listActivityFeed(repoPath),
    enabled: Boolean(repoPath),
  });
}

export function useAgents(repoPath: string) {
  return useQuery({
    queryKey: KEYS.agents(repoPath),
    queryFn: () => api.listAgents(repoPath),
    enabled: Boolean(repoPath),
  });
}

export function useUpdateTaskStatus() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      taskId,
      status,
      notes,
    }: {
      taskId: string;
      status: string;
      notes?: string;
    }) => api.updateTaskStatus(taskId, status, notes),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks'] }),
  });
}
