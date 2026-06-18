/**
 * Purpose: Sessions list screen — all sessions with status, timestamps, controls.
 *          Wires AsyncScreen for all 7 UI states (loading skeleton, empty CTA,
 *          error+retry, offline/daemon, permission-denied, rate-limited, populated).
 * Inputs:  Sessions from Tauri listSessions command
 * Outputs: Table of sessions with clickable rows that navigate to chat
 * Constraints: Polls on mount; click sets active session and navigates to chat
 * SPORT: T-P3-E5-W1-S2-T01
 */

import { useEffect } from "react";
import { RefreshCw, MessageSquare, List } from "lucide-react";
import { listSessions } from "@/lib/tauriApi";
import { useConversationStore } from "@/stores/conversationStore";
import { useAppStore } from "@/stores/appStore";
import { useDaemonStatus } from "@/hooks/useDaemonStatus";
import { useAsyncResult } from "@/hooks/useAsyncResult";
import { err } from "@nself/errors";
import type { AppError } from "@nself/errors";
import { AsyncScreen } from "@nself/ui";
import type { Session } from "@/types";

// ── Skeleton ──────────────────────────────────────────────────────────────────

function SessionsSkeleton() {
  return (
    <div className="space-y-2 overflow-y-auto" aria-hidden="true">
      {Array.from({ length: 4 }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 px-4 py-3 rounded-xl border animate-pulse"
          style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
        >
          <div className="flex-1 space-y-1.5">
            <div className="h-3.5 w-48 rounded bg-gray-800" />
            <div className="h-3 w-32 rounded bg-gray-800" />
          </div>
          <div className="h-5 w-5 rounded bg-gray-800" />
        </div>
      ))}
    </div>
  );
}

// ── Status helpers ────────────────────────────────────────────────────────────

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

// ── Main component ────────────────────────────────────────────────────────────

export function SessionsScreen() {
  const setActiveSession = useConversationStore((s) => s.setActiveSession);
  const setRoute = useAppStore((s) => s.setRoute);
  const { isConnected, licensed, retry: retryDaemon } = useDaemonStatus();

  const { result, reload } = useAsyncResult(
    () => listSessions(),
    [isConnected]
  );

  // Override result with offline/permission-denied if daemon is unavailable
  const effectiveResult = !isConnected
    ? err({ code: "not_found", message: "ClawDE daemon offline", status: 404 } as AppError)
    : !licensed
    ? err({ code: "license_required", message: "ClawDE bundle required", status: 402 } as AppError)
    : result;

  useEffect(() => {
    if (isConnected) reload();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isConnected]);

  const handleOpen = (session: Session) => {
    setActiveSession(session);
    setRoute("chat");
  };

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-100">Sessions</h1>
        <button
          onClick={reload}
          disabled={effectiveResult === "loading"}
          className="text-gray-400 hover:text-gray-200 p-1.5 rounded transition-colors"
          title="Refresh"
        >
          <RefreshCw size={16} className={effectiveResult === "loading" ? "animate-spin" : ""} />
        </button>
      </div>

      <AsyncScreen
        result={effectiveResult}
        renderData={(sessions: Session[]) => (
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
        emptyCheck={(sessions: Session[]) => sessions.length === 0}
        onRetry={reload}
        slots={{
          loading: <SessionsSkeleton />,
          empty: (
            <div className="flex flex-col items-center justify-center h-full gap-4 mt-16">
              <List size={40} className="text-gray-700" />
              <p className="text-gray-500 text-sm">No sessions yet.</p>
              <button
                onClick={() => setRoute("chat")}
                className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-500 transition-colors"
              >
                Start a conversation
              </button>
            </div>
          ),
          offline: (
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-16">
              <p className="text-yellow-400 text-sm font-medium">ClawDE daemon offline</p>
              <p className="text-gray-500 text-xs">Run <code className="font-mono">nself start</code> to reconnect</p>
              <button
                onClick={retryDaemon}
                className="px-3 py-1.5 text-xs bg-gray-800 text-gray-200 rounded-lg hover:bg-gray-700 transition-colors"
              >
                Reconnect
              </button>
            </div>
          ),
          permissionDenied: (
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-16">
              <p className="text-red-400 text-sm font-medium">ClawDE bundle required</p>
              <p className="text-gray-500 text-xs">Desktop is always free. Mobile + team features require the ClawDE bundle.</p>
              <a
                href="https://cloud.nself.org"
                target="_blank"
                rel="noreferrer"
                className="px-3 py-1.5 text-xs bg-blue-700 text-white rounded-lg hover:bg-blue-600 transition-colors"
              >
                Upgrade at cloud.nself.org
              </a>
            </div>
          ),
          rateLimited: (
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-16">
              <p className="text-orange-400 text-sm font-medium">Rate limit reached</p>
              <p className="text-gray-500 text-xs">AI provider is throttling requests. Please wait a moment.</p>
              <button
                onClick={reload}
                className="px-3 py-1.5 text-xs bg-gray-800 text-gray-200 rounded-lg hover:bg-gray-700 transition-colors"
              >
                Retry
              </button>
            </div>
          ),
        }}
      />
    </div>
  );
}
