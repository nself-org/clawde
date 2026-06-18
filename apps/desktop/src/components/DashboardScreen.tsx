/**
 * Purpose: Dashboard screen — daemon metrics (sessions, tokens, uptime) + memory.
 *          Wires AsyncScreen for all 7 UI states: loading skeleton, empty (no data),
 *          error+retry, offline/daemon, permission-denied, rate-limited, populated.
 * Inputs:  getMetrics and getMemory Tauri commands; useDaemonStatus for connection
 * Outputs: Stat cards and memory entry list
 * Constraints: Auto-refreshes every 30s; shows daemonVersion and status
 * SPORT: T-P3-E5-W1-S2-T01
 */

import React, { useEffect, useCallback } from "react";
import { RefreshCw, Activity, Database, Clock, Key, LayoutDashboard } from "lucide-react";
import { getMetrics, getMemory } from "@/lib/tauriApi";
import { useAppStore } from "@/stores/appStore";
import { useDaemonStatus } from "@/hooks/useDaemonStatus";
import { useAsyncResult } from "@/hooks/useAsyncResult";
import { err } from "@nself/errors";
import type { AppError } from "@nself/errors";
import { AsyncScreen } from "@nself/ui";
import type { Metrics, MemoryEntry } from "@/types";

// ── Dashboard data shape ──────────────────────────────────────────────────────

interface DashboardData {
  metrics: Metrics;
  memory: MemoryEntry[];
}

// ── Sub-components ────────────────────────────────────────────────────────────

function StatCard({ icon, label, value }: { icon: React.ReactNode; label: string; value: string | number }) {
  return (
    <div
      className="flex items-center gap-3 px-4 py-3 rounded-xl border"
      style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
    >
      <div className="text-blue-400">{icon}</div>
      <div>
        <div className="text-xs text-gray-500">{label}</div>
        <div className="text-lg font-semibold text-gray-100">{value}</div>
      </div>
    </div>
  );
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function DashboardSkeleton() {
  return (
    <div className="space-y-4" aria-hidden="true">
      <div className="grid grid-cols-3 gap-3">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex items-center gap-3 px-4 py-3 rounded-xl border animate-pulse" style={{ borderColor: "#1e2638", background: "#0a0e1a" }}>
            <div className="h-5 w-5 rounded bg-gray-800 flex-shrink-0" />
            <div className="space-y-1 flex-1">
              <div className="h-2.5 w-14 rounded bg-gray-800" />
              <div className="h-5 w-10 rounded bg-gray-800" />
            </div>
          </div>
        ))}
      </div>
      <div className="space-y-2">
        {[0, 1, 2].map((i) => (
          <div key={i} className="h-12 rounded-lg animate-pulse" style={{ background: "#0a0e1a" }} />
        ))}
      </div>
    </div>
  );
}

// ── Populated dashboard ───────────────────────────────────────────────────────

function DashboardContent({ data }: { data: DashboardData }) {
  return (
    <>
      <div className="grid grid-cols-3 gap-3 mb-6">
        <StatCard icon={<Activity size={20} />} label="Sessions" value={data.metrics.session_count} />
        <StatCard icon={<Database size={20} />} label="Total Tokens" value={data.metrics.total_tokens.toLocaleString()} />
        <StatCard icon={<Clock size={20} />} label="Uptime" value={formatUptime(data.metrics.uptime_seconds)} />
      </div>

      {data.memory.length > 0 && (
        <div>
          <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
            Memory ({data.memory.length})
          </div>
          <div className="space-y-2">
            {data.memory.slice(0, 20).map((entry, i) => (
              <div key={i} className="px-3 py-2 rounded-lg border text-xs" style={{ borderColor: "#1e2638", background: "#0a0e1a" }}>
                <div className="flex items-center gap-1.5 mb-1">
                  <Key size={10} className="text-blue-400" />
                  <span className="font-mono text-blue-300">{entry.key}</span>
                  <span className="ml-auto text-gray-600 bg-gray-800 px-1.5 rounded">{entry.scope}</span>
                </div>
                <div className="text-gray-400 truncate">{entry.value}</div>
              </div>
            ))}
          </div>
        </div>
      )}
    </>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function DashboardScreen() {
  const daemonVersion = useAppStore((s) => s.daemonVersion);
  const daemonStatus = useAppStore((s) => s.daemonStatus);
  const { isConnected, licensed, retry: retryDaemon } = useDaemonStatus();

  const fetchDashboard = useCallback(async (): Promise<DashboardData> => {
    const [m, mem] = await Promise.all([getMetrics(), getMemory()]);
    return { metrics: m, memory: mem };
  }, []);

  const { result, reload } = useAsyncResult(fetchDashboard, [isConnected]);

  const effectiveResult = !isConnected
    ? err({ code: "not_found", message: "ClawDE daemon offline", status: 404 } as AppError)
    : !licensed
    ? err({ code: "license_required", message: "ClawDE bundle required", status: 402 } as AppError)
    : result;

  // Auto-refresh every 30s when connected
  useEffect(() => {
    if (!isConnected) return;
    const interval = setInterval(reload, 30_000);
    return () => clearInterval(interval);
  }, [isConnected, reload]);

  return (
    <div className="flex flex-col h-full p-4 overflow-y-auto" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-lg font-semibold text-gray-100">Dashboard</h1>
          {daemonVersion && <div className="text-xs text-gray-500">clawd v{daemonVersion}</div>}
        </div>
        <button
          onClick={reload}
          disabled={effectiveResult === "loading"}
          className="text-gray-400 hover:text-gray-200 p-1.5 rounded transition-colors"
          title="Refresh"
        >
          <RefreshCw size={16} className={effectiveResult === "loading" ? "animate-spin" : ""} />
        </button>
      </div>

      <div className="mb-4">
        <div className={[
          "inline-flex items-center gap-1.5 text-xs px-2 py-1 rounded-full",
          daemonStatus?.running ? "bg-green-900 text-green-300" : "bg-red-900 text-red-300",
        ].join(" ")}>
          <span className={["w-1.5 h-1.5 rounded-full", daemonStatus?.running ? "bg-green-400" : "bg-red-400"].join(" ")} />
          {daemonStatus?.running ? "Daemon running" : "Daemon stopped"}
        </div>
      </div>

      <AsyncScreen
        result={effectiveResult}
        renderData={(data: DashboardData) => <DashboardContent data={data} />}
        emptyCheck={() => false}
        onRetry={reload}
        slots={{
          loading: <DashboardSkeleton />,
          empty: (
            <div className="flex flex-col items-center justify-center h-full gap-4 mt-16">
              <LayoutDashboard size={40} className="text-gray-700" />
              <p className="text-gray-500 text-sm">No metrics available yet.</p>
            </div>
          ),
          offline: (
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-16">
              <p className="text-yellow-400 text-sm font-medium">ClawDE daemon offline</p>
              <p className="text-gray-500 text-xs">Run <code className="font-mono">nself start</code> to reconnect</p>
              <button onClick={retryDaemon} className="px-3 py-1.5 text-xs bg-gray-800 text-gray-200 rounded-lg hover:bg-gray-700 transition-colors">
                Reconnect
              </button>
            </div>
          ),
          permissionDenied: (
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-16">
              <p className="text-red-400 text-sm font-medium">ClawDE bundle required</p>
              <p className="text-gray-500 text-xs">Desktop is always free. Mobile + team features require the ClawDE bundle.</p>
              <a href="https://cloud.nself.org" target="_blank" rel="noreferrer" className="px-3 py-1.5 text-xs bg-blue-700 text-white rounded-lg hover:bg-blue-600 transition-colors">
                Upgrade at cloud.nself.org
              </a>
            </div>
          ),
          rateLimited: (
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-16">
              <p className="text-orange-400 text-sm font-medium">Rate limit reached</p>
              <p className="text-gray-500 text-xs">Please wait a moment before trying again.</p>
              <button onClick={reload} className="px-3 py-1.5 text-xs bg-gray-800 text-gray-200 rounded-lg hover:bg-gray-700 transition-colors">
                Retry
              </button>
            </div>
          ),
        }}
      />
    </div>
  );
}
