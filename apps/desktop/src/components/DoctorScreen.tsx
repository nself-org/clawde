/**
 * Purpose: Doctor screen — system health checks (daemon, auth, deps, claude CLI).
 * Inputs:  Tauri daemonStatus + healthCheck commands; shell for CLI checks
 * Outputs: Pass/fail indicator per check with actionable advice
 * Constraints: Matches Flutter Doctor screen; runs on mount and on manual refresh
 * SPORT: T-E1-07
 */

import { useEffect, useState } from "react";
import { CheckCircle2, XCircle, RefreshCw, Loader2 } from "lucide-react";
import { Command } from "@tauri-apps/plugin-shell";
import { healthCheck } from "@/lib/tauriApi";
import { useAppStore } from "@/stores/appStore";
import type { DoctorCheck } from "@/types";

async function checkClaudeCLI(): Promise<DoctorCheck> {
  try {
    const cmd = Command.create("claude", ["--version"]);
    const result = await cmd.execute();
    const ok = result.code === 0;
    return {
      name: "Claude CLI",
      ok,
      message: ok ? result.stdout.trim() : "claude binary not found — install from anthropic.com",
    };
  } catch {
    return {
      name: "Claude CLI",
      ok: false,
      message: "claude binary not found or not executable",
    };
  }
}

async function checkGitAvailable(): Promise<DoctorCheck> {
  try {
    const cmd = Command.create("git", ["--version"]);
    const result = await cmd.execute();
    const ok = result.code === 0;
    return {
      name: "Git",
      ok,
      message: ok ? result.stdout.trim() : "git not found",
    };
  } catch {
    return { name: "Git", ok: false, message: "git not found in PATH" };
  }
}

export function DoctorScreen() {
  const daemonStatus = useAppStore((s) => s.daemonStatus);
  const daemonError = useAppStore((s) => s.daemonError);
  const refreshDaemon = useAppStore((s) => s.refreshDaemon);

  const [checks, setChecks] = useState<DoctorCheck[]>([]);
  const [loading, setLoading] = useState(false);

  const runChecks = async () => {
    setLoading(true);
    const results: DoctorCheck[] = [];

    // Daemon running
    await refreshDaemon();
    results.push({
      name: "Daemon",
      ok: !!daemonStatus?.running,
      message: daemonError
        ? daemonError
        : daemonStatus?.running
        ? `Running (REST :${daemonStatus.port_rest}, WS :${daemonStatus.port_ws})`
        : "Daemon not running",
    });

    // Auth token
    results.push({
      name: "Auth Token",
      ok: !!daemonStatus?.has_token,
      message: daemonStatus?.has_token ? "Token present" : "No auth token found",
    });

    // Health check
    try {
      const health = await healthCheck();
      results.push({
        name: "API Health",
        ok: health.ok,
        message: `clawd v${health.version}`,
      });
    } catch {
      results.push({ name: "API Health", ok: false, message: "API unreachable" });
    }

    // Claude CLI
    results.push(await checkClaudeCLI());

    // Git
    results.push(await checkGitAvailable());

    setChecks(results);
    setLoading(false);
  };

  useEffect(() => { runChecks(); }, []);

  const allOk = checks.length > 0 && checks.every((c) => c.ok);

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <h1 className="text-lg font-semibold text-gray-100">Doctor</h1>
          {!loading && (
            <span
              className={[
                "text-xs px-2 py-0.5 rounded-full",
                allOk
                  ? "bg-green-900 text-green-300"
                  : "bg-yellow-900 text-yellow-300",
              ].join(" ")}
            >
              {allOk ? "All systems OK" : "Issues found"}
            </span>
          )}
        </div>
        <button
          onClick={runChecks}
          disabled={loading}
          className="text-gray-400 hover:text-gray-200 p-1.5 rounded transition-colors"
          title="Re-run checks"
        >
          <RefreshCw size={16} className={loading ? "animate-spin" : ""} />
        </button>
      </div>

      {loading && (
        <div className="flex items-center gap-2 text-gray-500 text-sm">
          <Loader2 size={14} className="animate-spin" />
          Running checks…
        </div>
      )}

      <div className="space-y-3">
        {checks.map((check, i) => (
          <div
            key={i}
            className="flex items-start gap-3 px-4 py-3 rounded-xl border"
            style={{
              borderColor: check.ok ? "#14532d" : "#7f1d1d",
              background: check.ok ? "#052e1640" : "#3b000040",
            }}
          >
            <div className="flex-shrink-0 mt-0.5">
              {check.ok ? (
                <CheckCircle2 size={18} className="text-green-400" />
              ) : (
                <XCircle size={18} className="text-red-400" />
              )}
            </div>
            <div>
              <div className="text-sm font-medium text-gray-200">{check.name}</div>
              {check.message && (
                <div className="text-xs text-gray-500 mt-0.5">{check.message}</div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
