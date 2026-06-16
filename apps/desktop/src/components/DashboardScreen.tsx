/**
 * Purpose: Dashboard screen — daemon metrics (sessions, tokens, uptime) + memory.
 * Inputs:  getMetrics and getMemory Tauri commands
 * Outputs: Stat cards and memory entry list
 * Constraints: Auto-refreshes every 30s; shows daemonVersion and status
 * SPORT: T-E1-07
 */

import React, { useEffect, useState } from "react";
import { RefreshCw, Activity, Database, Clock, Key } from "lucide-react";
import { getMetrics, getMemory } from "@/lib/tauriApi";
import { useAppStore } from "@/stores/appStore";
import type { Metrics, MemoryEntry } from "@/types";

function StatCard({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode;
  label: string;
  value: string | number;
}) {
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

export function DashboardScreen() {
  const daemonStatus = useAppStore((s) => s.daemonStatus);
  const daemonVersion = useAppStore((s) => s.daemonVersion);
  const refreshDaemon = useAppStore((s) => s.refreshDaemon);

  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [memory, setMemory] = useState<MemoryEntry[]>([]);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const [m, mem] = await Promise.allSettled([getMetrics(), getMemory()]);
      if (m.status === "fulfilled") setMetrics(m.value);
      if (mem.status === "fulfilled") setMemory(mem.value);
      await refreshDaemon();
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    const interval = setInterval(load, 30_000);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="flex flex-col h-full p-4 overflow-y-auto" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-lg font-semibold text-gray-100">Dashboard</h1>
          {daemonVersion && (
            <div className="text-xs text-gray-500">clawd v{daemonVersion}</div>
          )}
        </div>
        <button
          onClick={load}
          disabled={loading}
          className="text-gray-400 hover:text-gray-200 p-1.5 rounded transition-colors"
          title="Refresh"
        >
          <RefreshCw size={16} className={loading ? "animate-spin" : ""} />
        </button>
      </div>

      {/* Status */}
      <div className="mb-4">
        <div
          className={[
            "inline-flex items-center gap-1.5 text-xs px-2 py-1 rounded-full",
            daemonStatus?.running
              ? "bg-green-900 text-green-300"
              : "bg-red-900 text-red-300",
          ].join(" ")}
        >
          <span
            className={[
              "w-1.5 h-1.5 rounded-full",
              daemonStatus?.running ? "bg-green-400" : "bg-red-400",
            ].join(" ")}
          />
          {daemonStatus?.running ? "Daemon running" : "Daemon stopped"}
        </div>
      </div>

      {/* Stat grid */}
      {metrics && (
        <div className="grid grid-cols-3 gap-3 mb-6">
          <StatCard
            icon={<Activity size={20} />}
            label="Sessions"
            value={metrics.session_count}
          />
          <StatCard
            icon={<Database size={20} />}
            label="Total Tokens"
            value={metrics.total_tokens.toLocaleString()}
          />
          <StatCard
            icon={<Clock size={20} />}
            label="Uptime"
            value={formatUptime(metrics.uptime_seconds)}
          />
        </div>
      )}

      {/* Memory entries */}
      {memory.length > 0 && (
        <div>
          <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
            Memory ({memory.length})
          </div>
          <div className="space-y-2">
            {memory.slice(0, 20).map((entry, i) => (
              <div
                key={i}
                className="px-3 py-2 rounded-lg border text-xs"
                style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
              >
                <div className="flex items-center gap-1.5 mb-1">
                  <Key size={10} className="text-blue-400" />
                  <span className="font-mono text-blue-300">{entry.key}</span>
                  <span className="ml-auto text-gray-600 bg-gray-800 px-1.5 rounded">
                    {entry.scope}
                  </span>
                </div>
                <div className="text-gray-400 truncate">{entry.value}</div>
              </div>
            ))}
          </div>
        </div>
      )}

      {!metrics && !loading && (
        <div className="text-gray-600 text-sm text-center mt-16">
          Daemon not reachable — metrics unavailable
        </div>
      )}
    </div>
  );
}
