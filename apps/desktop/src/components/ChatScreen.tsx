/**
 * Purpose: Main chat screen — session sidebar + message list + input bar.
 * Inputs:  Active session from conversationStore, message stream from useConversation
 * Outputs: Full chat UI with session list, message bubbles, text input
 * Constraints: Sidebar toggle; auto-scroll to latest message; streaming indicator
 * SPORT: T-E1-07
 */

import React, { useRef, useEffect, useState } from "react";
import {
  Plus, ChevronLeft, ChevronRight, Send, Square,
  Loader2, FolderOpen, Clock,
} from "lucide-react";
import { useConversationStore } from "@/stores/conversationStore";
import { useAppStore } from "@/stores/appStore";
import { useConversation } from "@/hooks/useConversation";
import { useSidebar } from "@/hooks/useSidebar";
import { listSessions, createSession, pickProjectFolder } from "@/lib/tauriApi";
import type { Session, Message } from "@/types";

// ── Session List Panel ─────────────────────────────────────────────────────────

function SessionItem({
  session,
  active,
  onClick,
}: {
  session: Session;
  active: boolean;
  onClick: () => void;
}) {
  const title = session.title ?? `Session ${session.id.slice(0, 8)}`;
  return (
    <button
      onClick={onClick}
      className={[
        "w-full text-left px-3 py-2 rounded-lg text-sm truncate",
        "transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500",
        active
          ? "bg-blue-700 text-white"
          : "text-gray-300 hover:bg-gray-800",
      ].join(" ")}
      title={title}
    >
      <div className="font-medium truncate">{title}</div>
      <div className="text-xs text-gray-500 flex items-center gap-1 mt-0.5">
        <Clock size={10} />
        {new Date(session.updated_at).toLocaleDateString()}
        <span
          className={[
            "ml-auto px-1.5 rounded text-xs",
            session.status === "running"
              ? "bg-blue-900 text-blue-300"
              : session.status === "error"
              ? "bg-red-900 text-red-300"
              : "bg-gray-800 text-gray-400",
          ].join(" ")}
        >
          {session.status}
        </span>
      </div>
    </button>
  );
}

function SessionSidebar({
  isOpen,
  toggle,
}: {
  isOpen: boolean;
  toggle: () => void;
}) {
  const { sessions, activeSession, setActiveSession, setSessions } =
    useConversationStore((s) => ({
      sessions: s.sessions,
      activeSession: s.activeSession,
      setActiveSession: s.setActiveSession,
      setSessions: s.setSessions,
    }));
  const { activeProjectPath } = useAppStore((s) => ({
    activeProjectPath: s.activeProjectPath,
  }));
  const setProjectPath = useAppStore((s) => s.setProjectPath);

  useEffect(() => {
    listSessions()
      .then(setSessions)
      .catch(() => {});
  }, [setSessions]);

  const handleNewSession = async () => {
    let projectPath = activeProjectPath;
    if (!projectPath) {
      projectPath = await pickProjectFolder();
      if (projectPath) setProjectPath(projectPath);
    }
    if (!projectPath) return;
    try {
      const session = await createSession(projectPath);
      setSessions([session, ...sessions]);
      setActiveSession(session);
    } catch {
      // ignore
    }
  };

  if (!isOpen) {
    return (
      <div
        className="flex flex-col items-center py-2 gap-1 flex-shrink-0"
        style={{
          width: 36,
          background: "#0d1117",
          borderRight: "1px solid #1e2638",
        }}
      >
        <button
          onClick={toggle}
          className="text-gray-400 hover:text-gray-200 p-1 rounded"
          title="Open sessions"
        >
          <ChevronRight size={16} />
        </button>
      </div>
    );
  }

  return (
    <div
      className="flex flex-col flex-shrink-0"
      style={{
        width: 220,
        background: "#0d1117",
        borderRight: "1px solid #1e2638",
      }}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-2 py-2">
        <span className="text-xs font-semibold text-gray-400 uppercase tracking-wide">
          Sessions
        </span>
        <div className="flex items-center gap-1">
          <button
            onClick={handleNewSession}
            className="text-gray-400 hover:text-blue-400 p-1 rounded"
            title="New session"
          >
            <Plus size={14} />
          </button>
          <button
            onClick={toggle}
            className="text-gray-400 hover:text-gray-200 p-1 rounded"
            title="Collapse"
          >
            <ChevronLeft size={14} />
          </button>
        </div>
      </div>

      {/* Project path */}
      {activeProjectPath && (
        <div className="px-2 pb-1">
          <div className="flex items-center gap-1 text-xs text-gray-500 truncate">
            <FolderOpen size={10} />
            <span className="truncate">
              {activeProjectPath.split("/").pop()}
            </span>
          </div>
        </div>
      )}

      {/* Session list */}
      <div className="flex-1 overflow-y-auto px-1 pb-2 space-y-0.5">
        {sessions.length === 0 ? (
          <div className="text-xs text-gray-600 text-center py-8">
            No sessions yet
          </div>
        ) : (
          sessions.map((s) => (
            <SessionItem
              key={s.id}
              session={s}
              active={activeSession?.id === s.id}
              onClick={() => setActiveSession(s)}
            />
          ))
        )}
      </div>
    </div>
  );
}

// ── Message Bubble ─────────────────────────────────────────────────────────────

function MessageBubble({ message }: { message: Message }) {
  const isUser = message.role === "user";
  const isTool = message.role === "tool";

  if (isTool) {
    return (
      <div className="px-4 py-2 max-w-2xl mx-auto w-full">
        <div className="text-xs text-gray-500 font-mono bg-gray-900 rounded p-2 border border-gray-800">
          <div className="text-blue-400 mb-1">
            🔧 {message.tool_name ?? "tool"}
          </div>
          {message.tool_output != null && (
            <div className="text-gray-400 whitespace-pre-wrap text-xs">
              {typeof message.tool_output === "string"
                ? message.tool_output
                : JSON.stringify(message.tool_output, null, 2)}
            </div>
          )}
        </div>
      </div>
    );
  }

  return (
    <div
      className={[
        "px-4 py-2 max-w-2xl mx-auto w-full",
        isUser ? "flex justify-end" : "flex justify-start",
      ].join(" ")}
    >
      <div
        className={[
          "rounded-2xl px-4 py-2 text-sm max-w-lg whitespace-pre-wrap",
          isUser
            ? "bg-blue-600 text-white rounded-br-sm"
            : "bg-gray-800 text-gray-100 rounded-bl-sm",
        ].join(" ")}
      >
        {message.content}
      </div>
    </div>
  );
}

// ── Message Input ──────────────────────────────────────────────────────────────

function MessageInput({
  onSubmit,
  isStreaming,
  disabled,
}: {
  onSubmit: (text: string) => void;
  isStreaming: boolean;
  disabled: boolean;
}) {
  const [value, setValue] = useState("");

  const handleSubmit = (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!value.trim() || disabled) return;
    onSubmit(value);
    setValue("");
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="px-4 py-3 border-t"
      style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
    >
      <div
        className="flex items-end gap-2 rounded-xl border px-3 py-2"
        style={{ borderColor: "#2d3748", background: "#111827" }}
      >
        <textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              handleSubmit();
            }
          }}
          placeholder={
            disabled ? "Select a session to start" : "Ask Claude anything…"
          }
          disabled={disabled}
          rows={1}
          className={[
            "flex-1 resize-none bg-transparent text-sm text-gray-100 placeholder-gray-600",
            "focus:outline-none min-h-[24px] max-h-40",
          ].join(" ")}
          style={{ height: "auto" }}
        />
        <button
          type="submit"
          disabled={disabled || !value.trim() || isStreaming}
          className={[
            "flex-shrink-0 p-1.5 rounded-lg transition-colors",
            disabled || !value.trim() || isStreaming
              ? "text-gray-600 cursor-not-allowed"
              : "text-blue-400 hover:text-blue-300 hover:bg-blue-900/40",
          ].join(" ")}
        >
          {isStreaming ? (
            <Square size={16} className="text-yellow-400" />
          ) : (
            <Send size={16} />
          )}
        </button>
      </div>
    </form>
  );
}

// ── Chat Screen ────────────────────────────────────────────────────────────────

export function ChatScreen() {
  const { isOpen, toggle } = useSidebar();
  const activeSession = useConversationStore((s) => s.activeSession);
  const { messages, isStreaming, streamingContent, submit } =
    useConversation(activeSession?.id ?? null);

  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, streamingContent]);

  return (
    <div className="flex h-full">
      <SessionSidebar isOpen={isOpen} toggle={toggle} />

      <div className="flex flex-col flex-1 min-w-0">
        {/* Session header */}
        <div
          className="flex items-center gap-2 px-4 py-2 text-sm text-gray-400 border-b flex-shrink-0"
          style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
        >
          {activeSession ? (
            <>
              <span className="font-medium text-gray-200 truncate">
                {activeSession.title ?? `Session ${activeSession.id.slice(0, 8)}`}
              </span>
              <span
                className={[
                  "text-xs px-1.5 py-0.5 rounded ml-auto",
                  activeSession.status === "running"
                    ? "bg-blue-900 text-blue-300"
                    : "bg-gray-800 text-gray-500",
                ].join(" ")}
              >
                {activeSession.status}
              </span>
              {isStreaming && (
                <Loader2 size={14} className="animate-spin text-blue-400" />
              )}
            </>
          ) : (
            <span className="text-gray-600">No session selected</span>
          )}
        </div>

        {/* Messages */}
        <div className="flex-1 overflow-y-auto py-4 space-y-1">
          {messages.map((m) => (
            <MessageBubble key={m.id} message={m} />
          ))}

          {/* Streaming assistant response */}
          {isStreaming && streamingContent && (
            <div className="px-4 py-2 max-w-2xl mx-auto w-full flex justify-start">
              <div className="rounded-2xl rounded-bl-sm px-4 py-2 text-sm max-w-lg bg-gray-800 text-gray-100 whitespace-pre-wrap">
                {streamingContent}
                <span className="ml-1 inline-block w-2 h-4 bg-blue-400 animate-pulse" />
              </div>
            </div>
          )}

          {/* Empty state */}
          {!activeSession && messages.length === 0 && (
            <div className="flex flex-col items-center justify-center h-full text-gray-600 gap-2">
              <div className="text-4xl">✦</div>
              <div className="text-sm">Select or create a session to start</div>
            </div>
          )}

          <div ref={bottomRef} />
        </div>

        <MessageInput
          onSubmit={submit}
          isStreaming={isStreaming}
          disabled={!activeSession}
        />
      </div>
    </div>
  );
}

// Re-export for use by tests
export { useConversation };
export type { Message };
