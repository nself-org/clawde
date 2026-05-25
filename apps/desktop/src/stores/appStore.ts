/**
 * Purpose: Global app state — daemon connection, active project, nav route.
 * Inputs:  Tauri commands (daemonStatus, healthCheck)
 * Outputs: Zustand store consumed by AppShell and top-level components
 * SPORT: T-E1-07
 */

import { create } from "zustand";
import type { DaemonStatus, NavRoute } from "@/types";
import { daemonStatus, healthCheck } from "@/lib/tauriApi";

interface AppState {
  // Daemon
  daemonStatus: DaemonStatus | null;
  daemonVersion: string | null;
  daemonError: string | null;
  // Active project
  activeProjectPath: string | null;
  // Navigation
  currentRoute: NavRoute;
  // Actions
  refreshDaemon: () => Promise<void>;
  setProjectPath: (path: string | null) => void;
  setRoute: (route: NavRoute) => void;
}

export const useAppStore = create<AppState>((set) => ({
  daemonStatus: null,
  daemonVersion: null,
  daemonError: null,
  activeProjectPath: null,
  currentRoute: "chat",

  refreshDaemon: async () => {
    try {
      const [status, health] = await Promise.all([
        daemonStatus(),
        healthCheck().catch(() => null),
      ]);
      set({
        daemonStatus: status,
        daemonVersion: health?.version ?? null,
        daemonError: null,
      });
    } catch (err) {
      set({ daemonError: String(err) });
    }
  },

  setProjectPath: (path) => set({ activeProjectPath: path }),
  setRoute: (route) => set({ currentRoute: route }),
}));
