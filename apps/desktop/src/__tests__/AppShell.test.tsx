/**
 * Purpose: Smoke tests for AppShell navigation and store integration.
 * Inputs:  Mocked Tauri APIs; in-memory Zustand stores
 * Outputs: Pass/fail assertions on render + nav interaction
 * Constraints: jsdom environment; @tauri-apps/* mocked via jest moduleNameMapper
 * SPORT: T-E1-07
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { useAppStore } from "@/stores/appStore";
import { useConversationStore } from "@/stores/conversationStore";

// Mock tauriApi at module level so each function can be controlled independently.
// This prevents listSessions() calls from consuming the daemonStatus() rejection mock.
jest.mock("@/lib/tauriApi", () => ({
  listSessions: jest.fn().mockResolvedValue([]),
  getSession: jest.fn().mockResolvedValue(null),
  createSession: jest.fn().mockResolvedValue(null),
  submitTask: jest.fn().mockResolvedValue(undefined),
  healthCheck: jest.fn().mockResolvedValue(null),
  daemonStatus: jest.fn().mockResolvedValue(null),
  getMetrics: jest.fn().mockResolvedValue(null),
  getMemory: jest.fn().mockResolvedValue([]),
  pickProjectFolder: jest.fn().mockResolvedValue(null),
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const tauriApi = require("@/lib/tauriApi") as {
  listSessions: jest.Mock;
  daemonStatus: jest.Mock;
  healthCheck: jest.Mock;
};

// Reset stores and mocks between tests
beforeEach(() => {
  jest.clearAllMocks();
  // Restore safe defaults after clearAllMocks
  tauriApi.listSessions.mockResolvedValue([]);
  tauriApi.daemonStatus.mockResolvedValue(null);
  tauriApi.healthCheck.mockResolvedValue(null);

  useAppStore.setState({
    daemonStatus: null,
    daemonVersion: null,
    daemonError: null,
    activeProjectPath: null,
    currentRoute: "chat",
  });

  useConversationStore.setState({
    activeSession: null,
    sessions: [],
    messages: {},
    isStreaming: false,
    streamingContent: "",
    error: null,
  });
});

describe("AppShell", () => {
  it("renders without crashing", async () => {
    const { AppShell } = await import("@/components/AppShell");
    await act(async () => {
      render(<AppShell />);
    });
    // Navigation rail should be present
    expect(screen.getByTitle("Chat")).toBeInTheDocument();
    expect(screen.getByTitle("Sessions")).toBeInTheDocument();
    expect(screen.getByTitle("Settings")).toBeInTheDocument();
  });

  it("changes route on nav button click", async () => {
    const { AppShell } = await import("@/components/AppShell");
    await act(async () => {
      render(<AppShell />);
    });
    const sessionsBtn = screen.getByTitle("Sessions");
    fireEvent.click(sessionsBtn);
    expect(useAppStore.getState().currentRoute).toBe("sessions");
  });

  it("shows disconnected when daemon is null", async () => {
    const { AppShell } = await import("@/components/AppShell");
    await act(async () => {
      render(<AppShell />);
    });
    // Daemon status is null → "Starting..." state
    expect(screen.getByText(/Starting/i)).toBeInTheDocument();
  });

  it("shows connected when daemon is running", async () => {
    useAppStore.setState({
      daemonStatus: { running: true, has_token: true, port_ws: 4300, port_rest: 4301 },
    });
    const { AppShell } = await import("@/components/AppShell");
    await act(async () => {
      render(<AppShell />);
    });
    expect(screen.getByText(/Connected/i)).toBeInTheDocument();
  });
});

describe("appStore", () => {
  it("refreshDaemon sets daemonError on invoke failure", async () => {
    // daemonStatus rejects → Promise.all rejects → catch sets daemonError
    tauriApi.daemonStatus.mockRejectedValueOnce(new Error("daemon offline"));
    // healthCheck is wrapped in .catch() inside refreshDaemon, so it resolving null is fine
    tauriApi.healthCheck.mockResolvedValue(null);

    const store = useAppStore.getState();
    await store.refreshDaemon();

    expect(useAppStore.getState().daemonError).toMatch(/daemon offline/);
  });

  it("setRoute updates currentRoute", () => {
    useAppStore.getState().setRoute("doctor");
    expect(useAppStore.getState().currentRoute).toBe("doctor");
  });

  it("setProjectPath updates activeProjectPath", () => {
    useAppStore.getState().setProjectPath("/home/user/project");
    expect(useAppStore.getState().activeProjectPath).toBe("/home/user/project");
  });
});
