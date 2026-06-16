/**
 * Purpose: ClawDE desktop entry point — detects OS locale, initialises i18n,
 *          then mounts the React tree inside Tauri 2.
 * Inputs:  navigator.language (OS locale via browser/Tauri WebView)
 * Outputs: React app mounted on #root with i18n context active.
 * Constraints:
 *   - initializeI18next() MUST be called before createRoot() so the i18n instance
 *     is ready before any component renders.
 *   - Locale is detected via navigator.language; falls back to 'en'.
 * SPORT: T-E1-07
 */

import React from "react";
import { createRoot } from "react-dom/client";
import { initializeI18next } from "@nself/i18n";
import type { Locale } from "@nself/i18n";
import { App } from "./App";

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
