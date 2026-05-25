/**
 * Purpose: Files screen — project directory browser for the active project.
 * Inputs:  activeProjectPath from appStore
 * Outputs: Directory listing with file/folder nodes
 * Constraints: Uses Tauri fs plugin for directory reads; read-only in v1
 * SPORT: T-E1-07
 */

import React, { useEffect, useState } from "react";
import { readDir } from "@tauri-apps/plugin-fs";
import { FolderOpen, File, FolderClosed } from "lucide-react";
import { useAppStore } from "@/stores/appStore";
import { pickProjectFolder } from "@/lib/tauriApi";

interface DirEntry {
  name?: string;
  isDirectory: boolean;
  path: string;
}

export function FilesScreen() {
  const activeProjectPath = useAppStore((s) => s.activeProjectPath);
  const setProjectPath = useAppStore((s) => s.setProjectPath);
  const [entries, setEntries] = useState<DirEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!activeProjectPath) {
      setEntries([]);
      return;
    }
    setLoading(true);
    setError(null);
    readDir(activeProjectPath)
      .then((items) => {
        const mapped = items
          .filter((e) => !e.name?.startsWith("."))
          .sort((a, b) => {
            if (a.isDirectory !== b.isDirectory)
              return a.isDirectory ? -1 : 1;
            return (a.name ?? "").localeCompare(b.name ?? "");
          });
        setEntries(mapped as DirEntry[]);
      })
      .catch((err) => setError(String(err)))
      .finally(() => setLoading(false));
  }, [activeProjectPath]);

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

      {!activeProjectPath ? (
        <div className="flex flex-col items-center justify-center flex-1 text-gray-600 gap-2">
          <FolderOpen size={40} />
          <div className="text-sm">No project selected</div>
          <button
            onClick={handlePickFolder}
            className="text-blue-400 hover:underline text-sm"
          >
            Open a folder
          </button>
        </div>
      ) : loading ? (
        <div className="text-gray-600 text-sm text-center mt-16">Loading…</div>
      ) : error ? (
        <div className="text-red-400 text-sm text-center mt-16">{error}</div>
      ) : (
        <div className="text-xs text-gray-500 mb-2 truncate">{activeProjectPath}</div>
      )}

      {entries.length > 0 && (
        <div className="overflow-y-auto">
          {entries.map((entry) => (
            <div
              key={entry.path}
              className="flex items-center gap-2 px-2 py-1 rounded hover:bg-gray-900 transition-colors text-sm"
            >
              {entry.isDirectory ? (
                <FolderClosed size={14} className="text-yellow-400 flex-shrink-0" />
              ) : (
                <File size={14} className="text-gray-500 flex-shrink-0" />
              )}
              <span className={entry.isDirectory ? "text-gray-200" : "text-gray-400"}>
                {entry.name}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
