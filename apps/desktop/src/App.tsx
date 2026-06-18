/**
 * Purpose: Root React component — wraps app with NselfI18nProvider, mounts
 *          AppShell inside an error boundary, and initialises ClawDE hooks.
 * Inputs:  None (self-bootstrapping)
 * Outputs: Full application UI wrapped in i18n context and error boundary
 * Constraints:
 *   - NselfI18nProvider must be the outermost provider so useNselfTranslation()
 *     works in all children including the ErrorFallback UI.
 *   - useClawDE must be called inside the component tree so stores are ready.
 *   - RTL: document.documentElement.dir is kept in sync with i18next locale changes.
 *     Tauri embeds a Vite SPA so the same dir-attribute approach as web applies.
 * SPORT: T-E1-07
 */

import { Component, ErrorInfo, ReactNode, useEffect } from "react";
import { NselfI18nProvider, useNselfTranslation, isRTL } from "@nself/i18n";
import { AppShell } from "@/components/AppShell";
import { useClawDE } from "@/hooks/useClawDE";
import i18next from "i18next";

// ── RTL — set document.dir on locale change ───────────────────────────────────

function useDocumentDir(): void {
  useEffect(() => {
    const applyDir = (lang: string): void => {
      document.documentElement.dir = isRTL(lang) ? "rtl" : "ltr";
    };
    applyDir(i18next.language ?? "en");
    i18next.on("languageChanged", applyDir);
    return () => {
      i18next.off("languageChanged", applyDir);
    };
  }, []);
}

// ── Error fallback — functional component uses t() for translated strings ─────

function ErrorFallback({ error, onRetry }: { error: Error | null; onRetry: () => void }) {
  const { t } = useNselfTranslation();
  return (
    <div
      className="flex flex-col items-center justify-center h-screen text-center p-8"
      style={{ background: "#030712" }}
    >
      <div className="text-red-400 text-2xl mb-4">✦</div>
      <div className="text-lg font-semibold text-gray-200 mb-2">
        {t('desktop.clawde.somethingWrong')}
      </div>
      <div className="text-sm text-gray-500 font-mono mb-6 max-w-md">
        {error?.message}
      </div>
      <button
        onClick={onRetry}
        className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm hover:bg-blue-500 transition-colors"
      >
        {t('desktop.clawde.tryAgain')}
      </button>
    </div>
  );
}

// ── Error Boundary ─────────────────────────────────────────────────────────────

interface ErrorBoundaryState { hasError: boolean; error: Error | null }

class ErrorBoundary extends Component<
  { children: ReactNode },
  ErrorBoundaryState
> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("[ClawDE] Uncaught error:", error, info);
  }

  render() {
    if (this.state.hasError) {
      return (
        <ErrorFallback
          error={this.state.error}
          onRetry={() => this.setState({ hasError: false, error: null })}
        />
      );
    }
    return this.props.children;
  }
}

// ── Root Component ─────────────────────────────────────────────────────────────

function ClawDEApp() {
  // Daemon polling + global shortcuts
  useClawDE();
  // Apply dir=rtl on Arabic locale (Tauri SPA — same approach as web).
  useDocumentDir();
  return <AppShell />;
}

export function App() {
  return (
    <NselfI18nProvider>
      <ErrorBoundary>
        <ClawDEApp />
      </ErrorBoundary>
    </NselfI18nProvider>
  );
}
