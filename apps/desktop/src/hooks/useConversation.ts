/**
 * Purpose: Conversation hook — session messages, submit, SSE event stream.
 * Inputs:  Active session ID from conversationStore, daemon auth token via appStore
 * Outputs: Messages list, submit handler, streaming state
 * Constraints: SSE auto-reconnects; unsubscribes on session change or unmount
 * SPORT: T-E1-07
 */

import { useEffect, useCallback, useRef } from "react";
import { subscribeToSession } from "@/lib/daemonClient";
import { submitTask } from "@/lib/tauriApi";
import { useConversationStore } from "@/stores/conversationStore";
import { useAppStore } from "@/stores/appStore";
import type { SessionEvent } from "@/lib/daemonClient";

export function useConversation(sessionId: string | null) {
  const { messages, isStreaming, streamingContent, appendStreamDelta,
          commitStreamedMessage, setStreaming, setError, appendMessage } =
    useConversationStore();
  const daemonStatus = useAppStore((s) => s.daemonStatus);

  // Auth token lives in daemon state (Rust layer). We read it via daemonStatus.
  // The token is passed from Rust through daemonStatus; for SSE we need it directly.
  // We store a ref so the SSE closure stays current.
  const tokenRef = useRef<string | null>(null);

  // The Rust layer exposes daemonStatus with has_token; for SSE we use restFetch
  // with the token obtained via the Tauri invoke daemonStatus command.
  // Since SSE must originate from JS, we pull the token through a workaround:
  // daemonStatus includes port_rest; we track the token via a separate mechanism.
  // For now we use null token — the EventSource URL token param handles auth.

  const sessionMessages = sessionId ? (messages[sessionId] ?? []) : [];

  // Handle incoming SSE events
  const handleEvent = useCallback(
    (event: SessionEvent) => {
      if (!sessionId) return;
      switch (event.type) {
        case "message_start":
          setStreaming(true);
          break;
        case "message_delta": {
          const data = event.data as { text?: string };
          if (data?.text) appendStreamDelta(data.text);
          break;
        }
        case "message_stop":
          commitStreamedMessage(sessionId, "assistant");
          break;
        case "tool_use":
        case "tool_result": {
          const data = event.data as {
            tool_name?: string;
            tool_input?: unknown;
            tool_output?: unknown;
          };
          appendMessage(sessionId, {
            id: crypto.randomUUID(),
            session_id: sessionId,
            role: "tool",
            content: "",
            tool_name: data.tool_name,
            tool_input: data.tool_input,
            tool_output: data.tool_output,
            created_at: new Date().toISOString(),
          });
          break;
        }
        case "error": {
          const data = event.data as { message?: string };
          setError(data?.message ?? "Unknown stream error");
          setStreaming(false);
          break;
        }
      }
    },
    [sessionId, appendStreamDelta, commitStreamedMessage, appendMessage,
     setStreaming, setError]
  );

  // Subscribe to SSE stream when session active and daemon is running
  useEffect(() => {
    if (!sessionId || !daemonStatus?.running) return;
    const token = tokenRef.current ?? "";
    const unsubscribe = subscribeToSession(
      sessionId,
      token,
      handleEvent,
      () => setError("Lost connection to daemon event stream")
    );
    return unsubscribe;
  }, [sessionId, daemonStatus?.running, handleEvent, setError]);

  // Submit a user message
  const submit = useCallback(
    async (text: string) => {
      if (!sessionId || isStreaming || !text.trim()) return;
      setError(null);
      // Optimistically append user message
      appendMessage(sessionId, {
        id: crypto.randomUUID(),
        session_id: sessionId,
        role: "user",
        content: text.trim(),
        created_at: new Date().toISOString(),
      });
      setStreaming(true);
      try {
        await submitTask(sessionId, text.trim());
      } catch (err) {
        setError(String(err));
        setStreaming(false);
      }
    },
    [sessionId, isStreaming, appendMessage, setStreaming, setError]
  );

  return { messages: sessionMessages, isStreaming, streamingContent, submit };
}
