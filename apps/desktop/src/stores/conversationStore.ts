/**
 * Purpose: Conversation state — messages per session, streaming deltas, submit flow.
 * Inputs:  Session ID, user message text, SSE stream events
 * Outputs: Zustand store consumed by ChatScreen and useConversation hook
 * Constraints: SSE token stream appended in-place; no re-renders on partial tokens
 * SPORT: T-E1-07
 */

import { create } from "zustand";
import type { Message, Session } from "@/types";

interface ConversationState {
  // Active session
  activeSession: Session | null;
  sessions: Session[];
  // Messages keyed by session ID
  messages: Record<string, Message[]>;
  // Streaming state
  isStreaming: boolean;
  streamingContent: string;
  // Error
  error: string | null;
  // Actions
  setActiveSession: (session: Session | null) => void;
  setSessions: (sessions: Session[]) => void;
  appendMessage: (sessionId: string, message: Message) => void;
  setMessages: (sessionId: string, messages: Message[]) => void;
  appendStreamDelta: (delta: string) => void;
  commitStreamedMessage: (sessionId: string, role: "assistant") => void;
  setStreaming: (streaming: boolean) => void;
  setError: (error: string | null) => void;
  clearMessages: (sessionId: string) => void;
}

export const useConversationStore = create<ConversationState>((set, get) => ({
  activeSession: null,
  sessions: [],
  messages: {},
  isStreaming: false,
  streamingContent: "",
  error: null,

  setActiveSession: (session) => set({ activeSession: session }),
  setSessions: (sessions) => set({ sessions }),

  setMessages: (sessionId, messages) =>
    set((state) => ({
      messages: { ...state.messages, [sessionId]: messages },
    })),

  appendMessage: (sessionId, message) =>
    set((state) => {
      const existing = state.messages[sessionId] ?? [];
      return {
        messages: {
          ...state.messages,
          [sessionId]: [...existing, message],
        },
      };
    }),

  appendStreamDelta: (delta) =>
    set((state) => ({ streamingContent: state.streamingContent + delta })),

  commitStreamedMessage: (sessionId, role) => {
    const { streamingContent } = get();
    if (!streamingContent) return;
    const message: Message = {
      id: crypto.randomUUID(),
      session_id: sessionId,
      role,
      content: streamingContent,
      created_at: new Date().toISOString(),
    };
    set((state) => {
      const existing = state.messages[sessionId] ?? [];
      return {
        messages: {
          ...state.messages,
          [sessionId]: [...existing, message],
        },
        streamingContent: "",
        isStreaming: false,
      };
    });
  },

  setStreaming: (isStreaming) => set({ isStreaming }),
  setError: (error) => set({ error }),

  clearMessages: (sessionId) =>
    set((state) => {
      const next = { ...state.messages };
      delete next[sessionId];
      return { messages: next };
    }),
}));
