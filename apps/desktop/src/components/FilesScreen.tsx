/**
 * Purpose: Files screen — project directory browser for the active project.
 *          Wires AsyncScreen for all 7 UI states: loading skeleton (file tree),
 *          empty CTA (no project open / empty folder), error+retry, offline/daemon,
 *          permission-denied, rate-limited, populated.
 * Inputs:  activeProjectPath from appStore; Tauri fs plugin readDir
 * Outputs: Directory listing with file/folder nodes
 * Constraints: Uses Tauri fs plugin for directory reads; read-only in v1
 * SPORT: T-P3-E5-W1-S2-T01
 */

import { readDir, type DirEntry } from "@tauri-apps/plugin-fs";
import { FolderOpen, File, FolderClosed } from "lucide-react";
import { useAppStore } from "@/stores/appStore";
import { pickProjectFolder } from "@/lib/tauriApi";
import { useDaemonStatus } from "@/hooks/useDaemonStatus";
import { useAsyncResult } from "@/hooks/useAsyncResult";
import { err, ok } from "@nself/errors";
import type { AppError } from "@nself/errors";
import { AsyncScreen } from "@nself/ui";

// ── Skeleton ──────────────────────────────────────────────────────────────────

function FileTreeSkeleton() {
  const depths = [0, 1, 1, 0, 1, 2];
  return (
    <div className="space-y-1 overflow-y-auto" aria-hidden="true">
      {depths.map((depth, i) => (
        <div
          key={i}
          className="flex items-center gap-2 py-1.5 rounded animate-pulse"
          style={{ paddingLeft: `${12 + depth * 12}px` }}
        >
          <div className="h-4 w-4 rounded bg-gray-800 flex-shrink-0" />
          <div className={`h-3 rounded bg-gray-800 ${i % 3 === 0 ? "w-32" : "w-24"}`} />
        </div>
      ))}
    </div>
  );
}

// ── File tree node ────────────────────────────────────────────────────────────

function FileNode({ entry }: { entry: DirEntry }) {
  const Icon = entry.isDirectory ? FolderClosed : File;
  return (
    <div className="flex items-center gap-2 px-2 py-1 rounded hover:bg-gray-900 transition-colors text-sm">
      <Icon size={14} className={entry.isDirectory ? "text-yellow-400 flex-shrink-0" : "text-gray-500 flex-shrink-0"} />
      <span className={entry.isDirectory ? "text-gray-200" : "text-gray-400"}>{entry.name}</span>
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function FilesScreen() {
  const activeProjectPath = useAppStore((s) => s.activeProjectPath);
  const setProjectPath = useAppStore((s) => s.setProjectPath);
  const { isConnected, licensed, retry: retryDaemon } = useDaemonStatus();

  const { result, reload } = useAsyncResult(
    async () => {
      if (!activeProjectPath) return [] as DirEntry[];
      const items = await readDir(activeProjectPath);
      return items
        .filter((e) => !e.name.startsWith("."))
        .sort((a, b) => {
          if (a.isDirectory !== b.isDirectory) return a.isDirectory ? -1 : 1;
          return a.name.localeCompare(b.name);
        });
    },
    [activeProjectPath]
  );

  const effectiveResult = !isConnected
    ? err({ code: "not_found", message: "ClawDE daemon offline", status: 404 } as AppError)
    : !licensed
    ? err({ code: "license_required", message: "ClawDE bundle required", status: 402 } as AppError)
    : !activeProjectPath
    ? ok([] as DirEntry[])
    : result;

  const handlePickFolder = async () => {
    const path = await pickProjectFolder();
    if (path) setProjectPath(path);
  };

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-100">Files</h1>
        <button
          onClick={handlePickFolder}
          className="flex items-center gap-1.5 text-xs text-gray-400 hover:text-blue-400 px-2 py-1 rounded border transition-colors"
          style={{ borderColor: "#1e2638" }}
        >
          <FolderOpen size={12} />
          {activeProjectPath ? "Change" : "Open Project"}
        </button>
      </div>

      {activeProjectPath && (
        <div className="text-xs text-gray-600 mb-3 truncate" title={activeProjectPath}>
          {activeProjectPath}
        </div>
      )}

      <AsyncScreen
        result={effectiveResult}
        renderData={(entries: DirEntry[]) => (
          <div className="overflow-y-auto">
            {entries.map((entry) => (
              <FileNode key={entry.name} entry={entry} />
            ))}
          </div>
        )}
        emptyCheck={(entries: DirEntry[]) => entries.length === 0}
        onRetry={reload}
        slots={{
          loading: <FileTreeSkeleton />,
          empty: (
            <div className="flex flex-col items-center justify-center h-full gap-4 mt-8">
              <FolderOpen size={40} className="text-gray-700" />
              <p className="text-gray-500 text-sm">Open a folder or create a new project</p>
              <button
                onClick={handlePickFolder}
                className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-500 transition-colors"
              >
                Open a folder
              </button>
            </div>
          ),
          offline: (
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-8">
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
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-8">
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
            <div className="flex flex-col items-center justify-center h-full gap-3 mt-8">
              <p className="text-orange-400 text-sm font-medium">Rate limit reached</p>
              <p className="text-gray-500 text-xs">Please wait a moment before trying again.</p>
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
