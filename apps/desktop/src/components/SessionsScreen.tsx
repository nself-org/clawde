/**
 * Purpose: Sessions list screen — all sessions with status, timestamps, controls.
 * Inputs:  Sessions from Tauri listSessions command
 * Outputs: Table of sessions with clickable rows that navigate to chat
 * Constraints: Polls on mount; click sets active session and navigates to chat
 * SPORT: T-E1-07
 */

import React, { useEffect, useState } from "react";
import { RefreshCw, MessageSquare, Trash2 } from "lucide-react";
import { listSessions } from "@/lib/tauriApi";
import { useConversationStore } from "@/stores/conversationStore";
import { useAppStore } from "@/stores/appStore";
import type { Session } from "@/types";

function statusColor(status: Session["status"]) {
  switch (status) {
    case "running": return "text-blue-400";
    case "error": return "text-red-400";
    case "completed": return "text-green-400";
    case "paused": return "text-yellow-400";
    case "cancelled": return "text-gray-500";
    default: return "text-gray-400";
  }
}

export function SessionsScreen() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const setActiveSession = useConversationStore((s) => s.setActiveSession);
  const setRoute = useAppStore((s) => s.setRoute);

  const load = async () => {
    setLoading(true);
    try {
      const data = await listSessions();
      setSessions(data);
    } catch {
      setSessions([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const handleOpen = (session: Session) => {
    setActiveSession(session);
    setRoute("chat");
  };

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-100">Sessions</h1>
        <button
          onClick={load}
          disabled={loading}
          className="text-gray-400 hover:text-gray-200 p-1.5 rounded transition-colors"
          title="Refresh"
        >
          <RefreshCw size={16} className={loading ? "animate-spin" : ""} />
        </button>
      </div>

      {sessions.length === 0 && !loading ? (
        <div className="text-gray-600 text-sm text-center mt-16">
          No sessions found. Start chatting to create one.
        </div>
      ) : (
        <div className="space-y-2 overflow-y-auto">
          {sessions.map((s) => (
            <div
              key={s.id}
              className="flex items-center gap-3 px-4 py-3 rounded-xl border cursor-pointer hover:bg-gray-900 transition-colors"
              style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
              onClick={() => handleOpen(s)}
            >
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium text-gray-200 truncate">
                  {s.title ?? `Session ${s.id.slice(0, 12)}`}
                </div>
                <div className="flex items-center gap-2 mt-0.5 text-xs text-gray-500">
                  <span className={statusColor(s.status)}>{s.status}</span>
                  <span>•</span>
                  <span>{new Date(s.updated_at).toLocaleString()}</span>
                  {s.project_path && (
                    <>
                      <span>•</span>
                      <span className="truncate">{s.project_path.split("/").pop()}</span>
                    </>
                  )}
                </div>
              </div>
              <button
                onClick={(e) => { e.stopPropagation(); handleOpen(s); }}
                className="text-gray-500 hover:text-blue-400 p-1 rounded"
                title="Open session"
              >
                <MessageSquare size={14} />
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
