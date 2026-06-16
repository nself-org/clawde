/**
 * Purpose: Packs / plugin-status screen — browse and manage Claude Code extension packs.
 *          Wires AsyncScreen for all 7 UI states: loading skeleton, empty CTA,
 *          error+retry, offline/daemon, permission-denied, rate-limited, populated.
 * Inputs:  Static placeholder packs (real pack API TBD in clawd v2); useDaemonStatus
 * Outputs: Pack list with status badges
 * Constraints: v1 stub; real pack management requires clawd pack endpoints
 * SPORT: T-P3-E5-W1-S2-T01
 */

import { Package, Plus } from "lucide-react";
import { useDaemonStatus } from "@/hooks/useDaemonStatus";
import { useAsyncResult } from "@/hooks/useAsyncResult";
import { err } from "@nself/errors";
import type { AppError } from "@nself/errors";
import { AsyncScreen } from "@nself/ui";

// ── Data ──────────────────────────────────────────────────────────────────────

interface Pack {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
}

const PLACEHOLDER_PACKS: Pack[] = [
  { id: "core", name: "Core Tools", description: "File operations, shell, search", enabled: true },
  { id: "git", name: "Git Integration", description: "Commit, diff, branch management", enabled: true },
  { id: "web", name: "Web Tools", description: "HTTP fetch, browser, scraping", enabled: false },
  { id: "db", name: "Database Tools", description: "SQL query, schema inspection", enabled: false },
];

async function fetchPacks(): Promise<Pack[]> {
  // In v1, returns static packs. Real pack API wires in clawd v2.
  return PLACEHOLDER_PACKS;
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function PacksSkeleton() {
  return (
    <div className="space-y-3" aria-hidden="true">
      {[0, 1, 2, 3].map((i) => (
        <div
          key={i}
          className="flex items-start gap-3 px-4 py-3 rounded-xl border animate-pulse"
          style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
        >
          <div className="h-5 w-5 rounded bg-gray-800 mt-0.5 flex-shrink-0" />
          <div className="flex-1 space-y-1.5">
            <div className="h-3.5 w-28 rounded bg-gray-800" />
            <div className="h-3 w-40 rounded bg-gray-800" />
          </div>
          <div className="h-5 w-14 rounded-full bg-gray-800 flex-shrink-0" />
        </div>
      ))}
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function PacksScreen() {
  const { isConnected, licensed, retry: retryDaemon } = useDaemonStatus();

  const { result, reload } = useAsyncResult(fetchPacks, [isConnected]);

  const effectiveResult = !isConnected
    ? err({ code: "not_found", message: "ClawDE daemon offline", status: 404 } as AppError)
    : !licensed
    ? err({ code: "license_required", message: "ClawDE bundle required", status: 402 } as AppError)
    : result;

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-100">Packs</h1>
        <button
          className="flex items-center gap-1 text-xs text-gray-400 hover:text-blue-400 px-2 py-1 rounded border transition-colors"
          style={{ borderColor: "#1e2638" }}
        >
          <Plus size={12} />
          Browse
        </button>
      </div>

      <AsyncScreen
        result={effectiveResult}
        renderData={(packs: Pack[]) => (
          <>
            <div className="space-y-3">
              {packs.map((pack) => (
                <div
                  key={pack.id}
                  className="flex items-start gap-3 px-4 py-3 rounded-xl border"
                  style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
                >
                  <div className="mt-0.5 text-blue-400 flex-shrink-0">
                    <Package size={18} />
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="text-sm font-medium text-gray-200">{pack.name}</div>
                    <div className="text-xs text-gray-500 mt-0.5">{pack.description}</div>
                  </div>
                  <div className="flex-shrink-0">
                    <span className={[
                      "text-xs px-2 py-0.5 rounded-full",
                      pack.enabled ? "bg-green-900 text-green-300" : "bg-gray-800 text-gray-500",
                    ].join(" ")}>
                      {pack.enabled ? "Enabled" : "Disabled"}
                    </span>
                  </div>
                </div>
              ))}
            </div>
            <div className="mt-4 text-xs text-gray-600 text-center">
              Pack management requires clawd v2 API. Coming soon.
            </div>
          </>
        )}
        emptyCheck={(packs: Pack[]) => packs.length === 0}
        onRetry={reload}
        slots={{
          loading: <PacksSkeleton />,
          empty: (
            <div className="flex flex-col items-center justify-center h-full gap-4 mt-16">
              <Package size={40} className="text-gray-700" />
              <p className="text-gray-500 text-sm">No packs installed yet.</p>
              <button
                className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-500 transition-colors"
              >
                Browse packs
              </button>
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
