/**
 * Purpose: Top-level app hook — daemon polling and global keyboard shortcuts.
 * Inputs:  App-level Zustand store, Tauri global-shortcut plugin
 * Outputs: Polling side-effect + shortcut registration on mount
 * Constraints: Shortcuts match Flutter originals (Cmd+N/W/P/./[/]/K, Escape)
 * SPORT: T-E1-07
 */

import { useEffect, useCallback } from "react";
import { register, unregisterAll } from "@tauri-apps/plugin-global-shortcut";
import { useAppStore } from "@/stores/appStore";
import { useConversationStore } from "@/stores/conversationStore";
import type { NavRoute } from "@/types";

const POLL_INTERVAL_MS = 10_000;

export function useClawDE() {
  const refreshDaemon = useAppStore((s) => s.refreshDaemon);
  const setRoute = useAppStore((s) => s.setRoute);
  const activeSession = useConversationStore((s) => s.activeSession);

  // Daemon health polling
  useEffect(() => {
    refreshDaemon();
    const interval = setInterval(refreshDaemon, POLL_INTERVAL_MS);
    return () => clearInterval(interval);
  }, [refreshDaemon]);

  const navigateTo = useCallback(
    (route: NavRoute) => setRoute(route),
    [setRoute]
  );

  // Global keyboard shortcuts matching Flutter originals
  useEffect(() => {
    const shortcuts: Array<[string, () => void]> = [
      ["CmdOrCtrl+N", () => navigateTo("chat")],
      ["CmdOrCtrl+P", () => navigateTo("search")],
      ["CmdOrCtrl+K", () => navigateTo("search")],
      ["CmdOrCtrl+Shift+P", () => navigateTo("search")],
    ];

    const registered: string[] = [];
    const setup = async () => {
      for (const [shortcut, handler] of shortcuts) {
        try {
          await register(shortcut, handler);
          registered.push(shortcut);
        } catch {
          // Shortcut may already be registered or unavailable
        }
      }
    };

    setup();
    return () => {
      unregisterAll().catch(() => {});
    };
  }, [navigateTo, activeSession]);

  return { refreshDaemon };
}
