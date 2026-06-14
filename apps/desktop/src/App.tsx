/**
 * Purpose: Root React component — mounts AppShell and initialises ClawDE hooks.
 * Inputs:  None (self-bootstrapping)
 * Outputs: Full application UI wrapped in error boundary
 * Constraints: useClawDE must be called inside the component tree so stores are ready
 * SPORT: T-E1-07
 */

import { Component, ErrorInfo, ReactNode } from "react";
import { AppShell } from "@/components/AppShell";
import { useClawDE } from "@/hooks/useClawDE";

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
        <div
          className="flex flex-col items-center justify-center h-screen text-center p-8"
          style={{ background: "#030712" }}
        >
          <div className="text-red-400 text-2xl mb-4">✦</div>
          <div className="text-lg font-semibold text-gray-200 mb-2">
            Something went wrong
          </div>
          <div className="text-sm text-gray-500 font-mono mb-6 max-w-md">
            {this.state.error?.message}
          </div>
          <button
            onClick={() => this.setState({ hasError: false, error: null })}
            className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm hover:bg-blue-500 transition-colors"
          >
            Try again
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

// ── Root Component ─────────────────────────────────────────────────────────────

function ClawDEApp() {
  // Daemon polling + global shortcuts
  useClawDE();
  return <AppShell />;
}

export function App() {
  return (
    <ErrorBoundary>
      <ClawDEApp />
    </ErrorBoundary>
  );
}
