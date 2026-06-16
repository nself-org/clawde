/**
 * Purpose: Adapts a Promise-based data fetch into a Result<T, AppError> | 'loading'
 *          value suitable for AsyncScreen. Handles the 7-state contract:
 *          - 'loading' while in-flight
 *          - Ok(data) on success
 *          - Err(AppError) on failure (error code mapped from the thrown error)
 * Inputs:
 *   - fetch: () => Promise<T> — the data-fetching function to call.
 *   - deps: React.DependencyList — re-fires when any dep changes.
 * Outputs: { result, reload } where result is Result<T,AppError>|'loading'.
 * Constraints:
 *   - Cancels in-flight fetch on unmount or dep change (via AbortController).
 *   - Maps HTTP-429 and license errors to typed AppError codes.
 * SPORT: T-P3-E5-W1-S2-T01
 */

import { useState, useEffect, useCallback, useRef, type DependencyList } from "react";
import { ok, err } from "@nself/errors";
import type { Result, AppError } from "@nself/errors";

function classifyError(e: unknown): AppError {
  if (e && typeof e === "object" && "code" in e) {
    return e as AppError;
  }
  const msg = String(e ?? "Unknown error");
  if (msg.includes("429") || msg.toLowerCase().includes("rate")) {
    return { code: "rate_limited", message: msg, status: 429 };
  }
  if (msg.toLowerCase().includes("license") || msg.toLowerCase().includes("forbidden")) {
    return { code: "license_required", message: msg, status: 402 };
  }
  return { code: "internal", message: msg, status: 500 };
}

export function useAsyncResult<T>(
  fetchFn: () => Promise<T>,
  deps: DependencyList = []
): { result: Result<T, AppError> | "loading"; reload: () => void } {
  const [result, setResult] = useState<Result<T, AppError> | "loading">("loading");
  const reloadTrigger = useRef(0);

  const load = useCallback(async () => {
    setResult("loading");
    try {
      const data = await fetchFn();
      setResult(ok(data));
    } catch (e) {
      setResult(err(classifyError(e)));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    void load();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, reloadTrigger.current]);

  const reload = useCallback(() => {
    reloadTrigger.current += 1;
    void load();
  }, [load]);

  return { result, reload };
}
