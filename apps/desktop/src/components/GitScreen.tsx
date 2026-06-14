/**
 * Purpose: Git screen — status, branch, and recent commits for the active project.
 * Inputs:  activeProjectPath from appStore; shell commands via Tauri
 * Outputs: Git status panel (branch, staged/unstaged changes, recent log)
 * Constraints: Read-only in v1; uses Tauri shell for git commands
 * SPORT: T-E1-07
 */

import { useEffect, useState } from "react";
import { Command } from "@tauri-apps/plugin-shell";
import { GitBranch, GitCommit, RefreshCw } from "lucide-react";
import { useAppStore } from "@/stores/appStore";

interface GitStatus {
  branch: string;
  status: string;
  log: string;
}

async function runGit(args: string[], cwd?: string): Promise<string> {
  const cmd = Command.create("git", args, cwd ? { cwd } : undefined);
  const out = await cmd.execute();
  return out.stdout.trim();
}

export function GitScreen() {
  const activeProjectPath = useAppStore((s) => s.activeProjectPath);
  const [gitStatus, setGitStatus] = useState<GitStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const loadGit = async () => {
    if (!activeProjectPath) return;
    setLoading(true);
    setError(null);
    try {
      const [branch, status, log] = await Promise.all([
        runGit(["rev-parse", "--abbrev-ref", "HEAD"], activeProjectPath),
        runGit(["status", "--short"], activeProjectPath),
        runGit(["log", "--oneline", "-10"], activeProjectPath),
      ]);
      setGitStatus({ branch, status, log });
    } catch (err) {
      setError(String(err));
      setGitStatus(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { loadGit(); }, [activeProjectPath]);

  return (
    <div className="flex flex-col h-full p-4" style={{ background: "#030712" }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold text-gray-100">Git</h1>
        <button
          onClick={loadGit}
          disabled={loading}
          className="text-gray-400 hover:text-gray-200 p-1.5 rounded transition-colors"
          title="Refresh"
        >
          <RefreshCw size={16} className={loading ? "animate-spin" : ""} />
        </button>
      </div>

      {!activeProjectPath && (
        <div className="text-gray-600 text-sm text-center mt-16">
          No project selected. Open a project first.
        </div>
      )}

      {error && (
        <div className="text-red-400 text-sm p-3 rounded bg-red-950 border border-red-800">
          {error}
        </div>
      )}

      {gitStatus && (
        <div className="space-y-4">
          {/* Branch */}
          <div
            className="flex items-center gap-2 px-3 py-2 rounded-lg border"
            style={{ borderColor: "#1e2638", background: "#0a0e1a" }}
          >
            <GitBranch size={14} className="text-blue-400" />
            <span className="text-sm text-gray-200 font-mono">{gitStatus.branch}</span>
          </div>

          {/* Status */}
          <div>
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
              Working Tree
            </div>
            {gitStatus.status ? (
              <pre className="text-xs text-gray-300 font-mono bg-gray-900 rounded-lg p-3 overflow-x-auto whitespace-pre-wrap">
                {gitStatus.status}
              </pre>
            ) : (
              <div className="text-xs text-green-400 flex items-center gap-1">
                <span>✓</span> Working tree clean
              </div>
            )}
          </div>

          {/* Recent commits */}
          <div>
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
              Recent Commits
            </div>
            <div className="space-y-1">
              {gitStatus.log.split("\n").filter(Boolean).map((line, i) => {
                const [hash, ...rest] = line.split(" ");
                return (
                  <div key={i} className="flex items-start gap-2 text-xs">
                    <GitCommit size={12} className="text-gray-600 flex-shrink-0 mt-0.5" />
                    <span className="text-gray-500 font-mono">{hash}</span>
                    <span className="text-gray-300">{rest.join(" ")}</span>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
