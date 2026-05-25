/**
 * Purpose: Instructions screen — manage CLAUDE.md and per-project instructions.
 * Inputs:  activeProjectPath from appStore; Tauri fs for read/write
 * Outputs: Editor for CLAUDE.md files in the active project
 * Constraints: Reads project-root/CLAUDE.md; saves on Ctrl+S or Save button
 * SPORT: T-E1-07
 */

import React, { useEffect, useState } from "react";
import { readTextFile, writeTextFile, exists } from "@tauri-apps/plugin-fs";
import { join } from "@tauri-apps/api/path";
import { Save, BookOpen, FileText } from "lucide-react";
import { useAppStore } from "@/stores/appStore";

export function InstructionsScreen() {
  const activeProjectPath = useAppStore((s) => s.activeProjectPath);
  const [content, setContent] = useState("");
  const [filePath, setFilePath] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!activeProjectPath) {
      setContent("");
      setFilePath(null);
      return;
    }
    const load = async () => {
      setLoading(true);
      setError(null);
      try {
        const fp = await join(activeProjectPath, "CLAUDE.md");
        setFilePath(fp);
        const fileExists = await exists(fp);
        if (fileExists) {
          const text = await readTextFile(fp);
          setContent(text);
        } else {
          setContent("# Project Instructions\n\nAdd instructions for Claude here.\n");
        }
        setDirty(false);
      } catch (err) {
        setError(String(err));
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [activeProjectPath]);

  const save = async () => {
    if (!filePath) return;
    try {
      await writeTextFile(filePath, content);
      setDirty(false);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (err) {
      setError(String(err));
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "s") {
      e.preventDefault();
      save();
    }
  };

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <h1 className="text-lg font-semibold text-gray-100">Instructions</h1>
          {filePath && (
            <div className="flex items-center gap-1 text-xs text-gray-500">
              <FileText size={10} />
              <span className="truncate max-w-xs">CLAUDE.md</span>
              {dirty && <span className="text-yellow-400">●</span>}
            </div>
          )}
        </div>
        {filePath && (
          <button
            onClick={save}
            disabled={!dirty}
            className={[
              "flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg border transition-colors",
              dirty
                ? "text-blue-300 border-blue-700 hover:bg-blue-900/40"
                : saved
                ? "text-green-400 border-green-800"
                : "text-gray-600 border-gray-800 cursor-not-allowed",
            ].join(" ")}
          >
            <Save size={12} />
            {saved ? "Saved!" : "Save"}
          </button>
        )}
      </div>

      {!activeProjectPath ? (
        <div className="flex flex-col items-center justify-center flex-1 text-gray-600 gap-2">
          <BookOpen size={40} />
          <div className="text-sm">No project selected</div>
          <div className="text-xs text-gray-700">Open a project to manage its CLAUDE.md</div>
        </div>
      ) : loading ? (
        <div className="text-gray-600 text-sm text-center mt-16">Loading…</div>
      ) : error ? (
        <div className="text-red-400 text-sm p-3 rounded bg-red-950 border border-red-800">
          {error}
        </div>
      ) : (
        <textarea
          value={content}
          onChange={(e) => { setContent(e.target.value); setDirty(true); }}
          onKeyDown={handleKeyDown}
          spellCheck={false}
          className="flex-1 bg-gray-900 text-gray-200 text-sm font-mono p-4 rounded-xl border resize-none focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          style={{ borderColor: "#1e2638" }}
          placeholder="# Project Instructions&#10;&#10;Add instructions for Claude here."
        />
      )}
    </div>
  );
}
