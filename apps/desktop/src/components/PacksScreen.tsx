/**
 * Purpose: Packs screen — browse and manage Claude Code extension packs.
 * Inputs:  None (placeholder; pack management API TBD in clawd v2)
 * Outputs: Pack list UI stub ready for API integration
 * Constraints: v1 stub; real pack management requires clawd pack endpoints
 * SPORT: T-E1-07
 */

import React from "react";
import { Package, Plus } from "lucide-react";

const PLACEHOLDER_PACKS = [
  { id: "core", name: "Core Tools", description: "File operations, shell, search", enabled: true },
  { id: "git", name: "Git Integration", description: "Commit, diff, branch management", enabled: true },
  { id: "web", name: "Web Tools", description: "HTTP fetch, browser, scraping", enabled: false },
  { id: "db", name: "Database Tools", description: "SQL query, schema inspection", enabled: false },
];

export function PacksScreen() {
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

      <div className="space-y-3">
        {PLACEHOLDER_PACKS.map((pack) => (
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
              <span
                className={[
                  "text-xs px-2 py-0.5 rounded-full",
                  pack.enabled
                    ? "bg-green-900 text-green-300"
                    : "bg-gray-800 text-gray-500",
                ].join(" ")}
              >
                {pack.enabled ? "Enabled" : "Disabled"}
              </span>
            </div>
          </div>
        ))}
      </div>

      <div className="mt-4 text-xs text-gray-600 text-center">
        Pack management requires clawd v2 API. Coming soon.
      </div>
    </div>
  );
}
