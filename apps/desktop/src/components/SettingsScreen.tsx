/**
 * Purpose: Settings screen — app preferences (theme, shortcut config, daemon port).
 *          Wires AsyncScreen for all 7 UI states: loading skeleton, empty (no prefs —
 *          not applicable but covered), error+retry, offline/daemon, permission-denied,
 *          rate-limited, populated.
 * Inputs:  localStorage for persisted prefs; daemonStatus for port info; useDaemonStatus
 * Outputs: Settings form with save; links to daemon config
 * Constraints: v1 subset of Flutter settings; non-destructive persisted to localStorage
 * SPORT: T-P3-E5-W1-S2-T01
 */

import { useState } from "react";
import { Settings, ExternalLink } from "lucide-react";
import { open as openUrl } from "@tauri-apps/plugin-shell";
import { useAppStore } from "@/stores/appStore";
import type { DaemonStatus } from "@/types";
import { useDaemonStatus } from "@/hooks/useDaemonStatus";
import { useAsyncResult } from "@/hooks/useAsyncResult";
import { err } from "@nself/errors";
import type { AppError } from "@nself/errors";
import { AsyncScreen } from "@nself/ui";

// ── Prefs ─────────────────────────────────────────────────────────────────────

type Theme = "dark" | "system";

interface AppPrefs {
  theme: Theme;
  showTokenCount: boolean;
  autoScrollChat: boolean;
}

function loadPrefs(): AppPrefs {
  try {
    const raw = localStorage.getItem("clawde-prefs");
    if (raw) return JSON.parse(raw) as AppPrefs;
  } catch {}
  return { theme: "dark", showTokenCount: false, autoScrollChat: true };
}

function savePrefsToStorage(prefs: AppPrefs) {
  try {
    localStorage.setItem("clawde-prefs", JSON.stringify(prefs));
  } catch {}
}

// ── Toggle ────────────────────────────────────────────────────────────────────

function Toggle({ checked, onChange, label, description }: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  description?: string;
}) {
  return (
    <div className="flex items-start justify-between gap-4 py-3">
      <div>
        <div className="text-sm text-gray-200">{label}</div>
        {description && <div className="text-xs text-gray-500 mt-0.5">{description}</div>}
      </div>
      <button
        onClick={() => onChange(!checked)}
        className={[
          "relative inline-flex h-5 w-9 flex-shrink-0 rounded-full border-2 border-transparent",
          "transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500",
          checked ? "bg-blue-600" : "bg-gray-700",
        ].join(" ")}
        role="switch"
        aria-checked={checked}
      >
        <span className={[
          "pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow",
          "transition duration-200 ease-in-out",
          checked ? "translate-x-4" : "translate-x-0",
        ].join(" ")} />
      </button>
    </div>
  );
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function SettingsSkeleton() {
  return (
    <div className="space-y-6" aria-hidden="true">
      <div className="space-y-2">
        <div className="h-3 w-20 rounded bg-gray-800 animate-pulse" />
        <div className="rounded-xl border divide-y animate-pulse" style={{ borderColor: "#1e2638", background: "#0a0e1a" }}>
          {[0, 1].map((i) => (
            <div key={i} className="flex items-center justify-between px-4 py-3">
              <div className="space-y-1">
                <div className="h-3.5 w-32 rounded bg-gray-800" />
                <div className="h-2.5 w-44 rounded bg-gray-800" />
              </div>
              <div className="h-5 w-9 rounded-full bg-gray-800" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// ── Populated settings ────────────────────────────────────────────────────────

function SettingsContent({ prefs, onUpdate, saved, daemonStatus, daemonVersion }: {
  prefs: AppPrefs;
  onUpdate: (p: Partial<AppPrefs>) => void;
  saved: boolean;
  daemonStatus: DaemonStatus | null;
  daemonVersion: string | null;
}) {
  return (
    <>
      <section className="mb-6">
        <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">Preferences</div>
        <div className="rounded-xl border divide-y" style={{ borderColor: "#1e2638", background: "#0a0e1a" }}>
          <div className="px-4">
            <Toggle label="Show token count" description="Display token usage per message" checked={prefs.showTokenCount} onChange={(v) => onUpdate({ showTokenCount: v })} />
          </div>
          <div className="px-4">
            <Toggle label="Auto-scroll chat" description="Automatically scroll to newest messages" checked={prefs.autoScrollChat} onChange={(v) => onUpdate({ autoScrollChat: v })} />
          </div>
        </div>
      </section>

      <section className="mb-6">
        <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">Daemon</div>
        <div className="rounded-xl border px-4 py-3 space-y-2" style={{ borderColor: "#1e2638", background: "#0a0e1a" }}>
          {daemonVersion && (
            <div className="flex justify-between text-sm">
              <span className="text-gray-500">Version</span>
              <span className="text-gray-200 font-mono">v{daemonVersion}</span>
            </div>
          )}
          {daemonStatus && (
            <>
              <div className="flex justify-between text-sm">
                <span className="text-gray-500">REST Port</span>
                <span className="text-gray-200 font-mono">{daemonStatus.port_rest}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-gray-500">WS Port</span>
                <span className="text-gray-200 font-mono">{daemonStatus.port_ws}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-gray-500">Auth Token</span>
                <span className={daemonStatus.has_token ? "text-green-400" : "text-red-400"}>
                  {daemonStatus.has_token ? "Present" : "Missing"}
                </span>
              </div>
            </>
          )}
        </div>
      </section>

      <section>
        <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">About</div>
        <div className="rounded-xl border px-4 py-3" style={{ borderColor: "#1e2638", background: "#0a0e1a" }}>
          <div className="flex items-center gap-2 mb-2">
            <Settings size={14} className="text-blue-400" />
            <span className="text-sm font-medium text-gray-200">ClawDE</span>
            <span className="text-xs text-gray-500 ml-auto">v0.3.4</span>
          </div>
          <div className="text-xs text-gray-500">A desktop interface for the clawd daemon.</div>
          <button onClick={() => openUrl("https://github.com/nself-org/clawde")} className="flex items-center gap-1 text-xs text-blue-400 hover:underline mt-2">
            <ExternalLink size={10} />
            GitHub
          </button>
        </div>
      </section>

      {saved && <div className="text-xs text-green-400 text-center mt-2">Saved</div>}
    </>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function SettingsScreen() {
  const daemonStatus = useAppStore((s) => s.daemonStatus);
  const daemonVersion = useAppStore((s) => s.daemonVersion);
  const { isConnected, licensed, retry: retryDaemon } = useDaemonStatus();

  const [prefs, setPrefs] = useState<AppPrefs>(loadPrefs);
  const [saved, setSaved] = useState(false);

  // Settings loads its own preferences (synchronous localStorage — always succeeds)
  const { result, reload } = useAsyncResult(
    async () => loadPrefs(),
    []
  );

  const effectiveResult = !isConnected
    ? err({ code: "not_found", message: "ClawDE daemon offline", status: 404 } as AppError)
    : !licensed
    ? err({ code: "license_required", message: "ClawDE bundle required", status: 402 } as AppError)
    : result;

  const handleUpdate = (partial: Partial<AppPrefs>) => {
    const next = { ...prefs, ...partial };
    setPrefs(next);
    savePrefsToStorage(next);
    setSaved(true);
    setTimeout(() => setSaved(false), 1200);
  };

  return (
    <div className="flex flex-col h-full p-4 overflow-y-auto" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-100">Settings</h1>
      </div>

      <AsyncScreen
        result={effectiveResult}
        renderData={(_loadedPrefs: AppPrefs) => (
          <SettingsContent
            prefs={prefs}
            onUpdate={handleUpdate}
            saved={saved}
            daemonStatus={daemonStatus}
            daemonVersion={daemonVersion}
          />
        )}
        emptyCheck={() => false}
        onRetry={reload}
        slots={{
          loading: <SettingsSkeleton />,
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
