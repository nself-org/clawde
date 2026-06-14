/**
 * Purpose: Settings screen — app preferences (theme, shortcut config, daemon port).
 * Inputs:  localStorage for persisted prefs; daemonStatus for port info
 * Outputs: Settings form with save; links to daemon config
 * Constraints: v1 subset of Flutter settings; non-destructive persisted to localStorage
 * SPORT: T-E1-07
 */

import { useState } from "react";
import { Settings, ExternalLink } from "lucide-react";
import { open as openUrl } from "@tauri-apps/plugin-shell";
import { useAppStore } from "@/stores/appStore";

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

function savePrefs(prefs: AppPrefs) {
  try {
    localStorage.setItem("clawde-prefs", JSON.stringify(prefs));
  } catch {}
}

function Toggle({
  checked,
  onChange,
  label,
  description,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  description?: string;
}) {
  return (
    <div className="flex items-start justify-between gap-4 py-3">
      <div>
        <div className="text-sm text-gray-200">{label}</div>
        {description && (
          <div className="text-xs text-gray-500 mt-0.5">{description}</div>
        )}
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
        <span
          className={[
            "pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow",
            "transition duration-200 ease-in-out",
            checked ? "translate-x-4" : "translate-x-0",
          ].join(" ")}
        />
      </button>
    </div>
  );
}

export function SettingsScreen() {
  const { daemonStatus, daemonVersion } = useAppStore((s) => ({
    daemonStatus: s.daemonStatus,
    daemonVersion: s.daemonVersion,
  }));

  const [prefs, setPrefs] = useState<AppPrefs>(loadPrefs);
  const [saved, setSaved] = useState(false);

  const update = (partial: Partial<AppPrefs>) => {
    const next = { ...prefs, ...partial };
    setPrefs(next);
    savePrefs(next);
    setSaved(true);
    setTimeout(() => setSaved(false), 1200);
  };

  return (
    <div className="flex flex-col h-full p-4 overflow-y-auto" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-100">Settings</h1>
        {saved && <span className="text-xs text-green-400">Saved</span>}
      </div>

      {/* Preferences */}
      <section className="mb-6">
        <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
          Preferences
        </div>
        <div
          className="rounded-xl border divide-y"
          style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
        >
          <div className="px-4">
            <Toggle
              label="Show token count"
              description="Display token usage per message"
              checked={prefs.showTokenCount}
              onChange={(v) => update({ showTokenCount: v })}
            />
          </div>
          <div className="px-4">
            <Toggle
              label="Auto-scroll chat"
              description="Automatically scroll to newest messages"
              checked={prefs.autoScrollChat}
              onChange={(v) => update({ autoScrollChat: v })}
            />
          </div>
        </div>
      </section>

      {/* Daemon info */}
      <section className="mb-6">
        <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
          Daemon
        </div>
        <div
          className="rounded-xl border px-4 py-3 space-y-2"
          style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
        >
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
                <span
                  className={
                    daemonStatus.has_token ? "text-green-400" : "text-red-400"
                  }
                >
                  {daemonStatus.has_token ? "Present" : "Missing"}
                </span>
              </div>
            </>
          )}
        </div>
      </section>

      {/* About */}
      <section>
        <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
          About
        </div>
        <div
          className="rounded-xl border px-4 py-3"
          style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
        >
          <div className="flex items-center gap-2 mb-2">
            <Settings size={14} className="text-blue-400" />
            <span className="text-sm font-medium text-gray-200">ClawDE</span>
            <span className="text-xs text-gray-500 ml-auto">v0.3.2</span>
          </div>
          <div className="text-xs text-gray-500">
            A desktop interface for the clawd daemon.
          </div>
          <button
            onClick={() => openUrl("https://github.com/nself-org/clawde")}
            className="flex items-center gap-1 text-xs text-blue-400 hover:underline mt-2"
          >
            <ExternalLink size={10} />
            GitHub
          </button>
        </div>
      </section>
    </div>
  );
}
