/**
 * Purpose: Jest mock for @nself/ui — provides AsyncScreen and all state
 *          sub-components as thin React wrappers with data-testid attributes
 *          so tests can assert which state is currently visible.
 * Constraints: Must not import any workspace source (test isolation).
 * SPORT: T-P3-E5-W1-S2-T01
 */

import React from "react";

// ── AsyncScreen mock ──────────────────────────────────────────────────────────

export const AsyncScreen = jest.fn(
  ({
    result,
    renderData,
    emptyCheck,
    onRetry,
    slots,
  }: {
    result: unknown;
    renderData: (data: unknown) => React.ReactNode;
    emptyCheck?: (data: unknown) => boolean;
    onRetry?: () => void;
    slots?: Record<string, React.ReactNode | (() => React.ReactNode)>;
  }): React.ReactElement => {
    if (result === "loading") {
      if (slots?.loading) {
        return React.createElement(
          "div",
          { "data-testid": "async-loading" },
          slots.loading as React.ReactNode
        );
      }
      return React.createElement(
        "div",
        { "data-testid": "async-loading" },
        "Loading"
      );
    }

    if (result && typeof result === "object" && "_tag" in result) {
      const r = result as { _tag: string; error?: { code: string }; value?: unknown };
      if (r._tag === "Err") {
        const code = r.error?.code ?? "";
        if (code === "license_required" || code === "auth_failed" || code === "forbidden") {
          if (slots?.permissionDenied) {
            return React.createElement(
              "div",
              { "data-testid": "async-permission-denied" },
              slots.permissionDenied as React.ReactNode
            );
          }
          return React.createElement(
            "div",
            { "data-testid": "async-permission-denied" },
            "Access restricted"
          );
        }
        if (code === "rate_limited") {
          if (slots?.rateLimited) {
            return React.createElement(
              "div",
              { "data-testid": "async-rate-limited" },
              slots.rateLimited as React.ReactNode
            );
          }
          return React.createElement(
            "div",
            { "data-testid": "async-rate-limited" },
            "Rate limit reached"
          );
        }
        if (code === "not_found" || code === "offline") {
          if (slots?.offline) {
            return React.createElement(
              "div",
              { "data-testid": "async-offline" },
              slots.offline as React.ReactNode
            );
          }
          return React.createElement(
            "div",
            { "data-testid": "async-offline" },
            "Offline"
          );
        }
        // generic error
        if (slots?.error && typeof slots.error === "function") {
          return React.createElement(
            "div",
            { "data-testid": "async-error" },
            (slots.error as (e: unknown, r?: () => void) => React.ReactNode)(r.error, onRetry)
          );
        }
        return React.createElement(
          "div",
          { "data-testid": "async-error", onClick: onRetry },
          "Something went wrong"
        );
      }
      if (r._tag === "Ok") {
        const data = r.value;
        if (emptyCheck?.(data) === true) {
          if (slots?.empty) {
            return React.createElement(
              "div",
              { "data-testid": "async-empty" },
              slots.empty as React.ReactNode
            );
          }
          return React.createElement(
            "div",
            { "data-testid": "async-empty" },
            "Nothing here yet"
          );
        }
        return React.createElement(
          "div",
          { "data-testid": "async-populated" },
          renderData(data)
        );
      }
    }

    return React.createElement("div", { "data-testid": "async-unknown" }, null);
  }
);

// ── State sub-component mocks ─────────────────────────────────────────────────

export const LoadingState = jest.fn((): React.ReactElement =>
  React.createElement("div", { role: "status", "data-testid": "async-loading" }, "Loading")
);

export const EmptyState = jest.fn(
  ({ heading }: { heading: string }): React.ReactElement =>
    React.createElement("div", { role: "status", "data-testid": "async-empty" }, heading)
);

export const ErrorState = jest.fn(
  ({ onRetry }: { onRetry?: () => void }): React.ReactElement =>
    React.createElement(
      "div",
      { "data-testid": "async-error", onClick: onRetry },
      "Something went wrong"
    )
);

export const OfflineState = jest.fn((): React.ReactElement =>
  React.createElement("div", { "data-testid": "async-offline" }, "Offline")
);

export const PermissionDeniedState = jest.fn((): React.ReactElement =>
  React.createElement("div", { "data-testid": "async-permission-denied" }, "Access restricted")
);

export const RateLimitedState = jest.fn((): React.ReactElement =>
  React.createElement("div", { "data-testid": "async-rate-limited" }, "Rate limit reached")
);
