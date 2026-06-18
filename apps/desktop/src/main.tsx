/**
 * Purpose: ClawDE desktop entry point — initialises observability (Sentry + OTel),
 *          detects OS locale, initialises i18n, then mounts the React tree inside Tauri 2.
 * Inputs:  VITE_NSELF_SENTRY_DSN, VITE_APP_ENV, VITE_APP_VERSION from build env;
 *          navigator.language (OS locale via browser/Tauri WebView).
 * Outputs: React app mounted on #root, Sentry + OTel registered, i18next initialised.
 * Constraints:
 *   - initObservability() MUST be called before createRoot() so Sentry catches
 *     any errors thrown during React hydration.
 *   - initializeI18next() MUST be called before createRoot() so the i18n instance
 *     is ready before any component renders.
 *   - Locale is detected via navigator.language; falls back to 'en'.
 *   - PII scrubbing runs unconditionally via Sentry's beforeSend hook (scrubEvent in
 *     @nself/observability/pii — strips user.email, user.id, and common PII patterns).
 * SPORT: T-E1-07
 */

import * as Sentry from '@sentry/react';
import type { SentrySdk } from '@nself/observability';
import { initObservability } from '@nself/observability';
import React from "react";
import { createRoot } from "react-dom/client";
import { initializeI18next } from "@nself/i18n";
import type { Locale } from "@nself/i18n";
import { App } from "./App";

// ─── Sentry + OTel init (before React mounts) ────────────────────────────────
// initObservability calls Sentry.init() with scrubEvent as beforeSend (PII scrubbing)
// and registers OTel tracing. Gracefully no-ops if DSN is absent.
initObservability({
  sentry: {
    sdk: Sentry as unknown as SentrySdk,
    dsn: import.meta.env.VITE_NSELF_SENTRY_DSN ?? '',
    environment: import.meta.env.VITE_APP_ENV ?? 'development',
    appKind: 'native',
    tracesSampleRate: import.meta.env.VITE_APP_ENV === 'production' ? 0.2 : 1.0,
    release: import.meta.env.VITE_APP_VERSION ?? '1.1.1',
  },
});

// ─── Locale detection (navigator.language → 'en' fallback) ───────────────────
const SUPPORTED_LOCALES: Locale[] = ['en', 'fr', 'ar', 'es', 'zh', 'ja', 'de', 'pt'];

function detectLocale(): Locale {
  const raw = navigator.language.split('-')[0].toLowerCase();
  return (SUPPORTED_LOCALES as string[]).includes(raw) ? (raw as Locale) : 'en';
}

// ─── i18n init (module level — before first render) ──────────────────────────
initializeI18next(detectLocale());

// ─── React tree ───────────────────────────────────────────────────────────────
const container = document.getElementById("root");
if (!container) throw new Error("Root element #root not found");

createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
