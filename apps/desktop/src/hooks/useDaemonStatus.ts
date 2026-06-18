/**
 * Purpose: Polls the clawd HTTP daemon health endpoint every 5 s and exposes
 *          a stable `isConnected` boolean. Two consecutive failures flip the
 *          state to offline so a single network blip does not trip the UI.
 * Inputs:  None — side-effect hook called at app boot.
 * Outputs: { isConnected: boolean; licensed: boolean; retry: () => void }
 *   isConnected — false after 2+ consecutive /health failures.
 *   licensed    — mirrors the `licensed` field from /health (true when the
 *                 ClawDE bundle is active on this machine; true by default for
 *                 free desktop features — only the /health response can set
 *                 it to false, e.g. if the daemon validates bundle entitlements).
 *   retry       — manual one-shot health check (resets failure counter).
 * Constraints:
 *   - Daemon HTTP port 7432 (clawd HTTP API — NOT the Tauri command bridge).
 *   - 2-failure threshold prevents flapping on a single timeout.
 *   - Does NOT depend on Tauri invoke; uses raw fetch so the hook is testable
 *     without Tauri native APIs.
 * SPORT: T-P3-E5-W1-S2-T01
 */

import { useEffect, useRef, useCallback, useState } from "react";

const HEALTH_URL = "http://127.0.0.1:7432/health";
const POLL_INTERVAL_MS = 5_000;
const FAILURE_THRESHOLD = 2;

export interface DaemonHealth {
  ok: boolean;
  version?: string;
  /** True when the ClawDE bundle license is active. Desktop-only features are
   *  always free; this flag governs mobile + team features. */
  licensed?: boolean;
}

export interface UseDaemonStatusResult {
  /** False after 2+ consecutive /health failures (daemon offline). */
  isConnected: boolean;
  /** True when the ClawDE bundle is active. False → show permission-denied CTA. */
  licensed: boolean;
  /** Manual retry — resets the failure counter and fires an immediate check. */
  retry: () => void;
}

export function useDaemonStatus(): UseDaemonStatusResult {
  const [isConnected, setIsConnected] = useState(true);
  const [licensed, setLicensed] = useState(true);
  const consecutiveFailures = useRef(0);

  const checkHealth = useCallback(async () => {
    try {
      const resp = await fetch(HEALTH_URL, {
        signal: AbortSignal.timeout(3_000),
      });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = (await resp.json()) as DaemonHealth;
      consecutiveFailures.current = 0;
      setIsConnected(true);
      setLicensed(data.licensed !== false); // default true; only false if explicitly false
    } catch {
      consecutiveFailures.current += 1;
      if (consecutiveFailures.current >= FAILURE_THRESHOLD) {
        setIsConnected(false);
      }
    }
  }, []);

  const retry = useCallback(() => {
    consecutiveFailures.current = 0;
    void checkHealth();
  }, [checkHealth]);

  useEffect(() => {
    void checkHealth();
    const interval = setInterval(() => void checkHealth(), POLL_INTERVAL_MS);
    return () => clearInterval(interval);
  }, [checkHealth]);

  return { isConnected, licensed, retry };
}
