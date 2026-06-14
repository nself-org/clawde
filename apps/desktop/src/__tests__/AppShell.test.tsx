/**
 * Purpose: Smoke tests for AppShell navigation and store integration.
 * Inputs:  Mocked Tauri APIs; in-memory Zustand stores
 * Outputs: Pass/fail assertions on render + nav interaction
 * Constraints: jsdom environment; @tauri-apps/* mocked via jest moduleNameMapper
 * SPORT: T-E1-07
 */

import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { useAppStore } from "@/stores/appStore";

// Reset Zustand stores between tests
beforeEach(() => {
  useAppStore.setState({
    daemonStatus: null,
    daemonVersion: null,
    daemonError: null,
    activeProjectPath: null,
    currentRoute: "chat",
  });
});

describe("AppShell", () => {
  it("renders without crashing", async () => {
    const { AppShell } = await import("@/components/AppShell");
    render(<AppShell />);
    // Navigation rail should be present
    expect(screen.getByTitle("Chat")).toBeInTheDocument();
    expect(screen.getByTitle("Sessions")).toBeInTheDocument();
    expect(screen.getByTitle("Settings")).toBeInTheDocument();
  });

  it("changes route on nav button click", async () => {
    const { AppShell } = await import("@/components/AppShell");
    render(<AppShell />);
    const sessionsBtn = screen.getByTitle("Sessions");
    fireEvent.click(sessionsBtn);
    expect(useAppStore.getState().currentRoute).toBe("sessions");
  });

  it("shows disconnected when daemon is null", async () => {
    const { AppShell } = await import("@/components/AppShell");
    render(<AppShell />);
    // Daemon status is null → "Starting..." state
    expect(screen.getByText(/Starting/i)).toBeInTheDocument();
  });

  it("shows connected when daemon is running", async () => {
    useAppStore.setState({
      daemonStatus: { running: true, has_token: true, port_ws: 4300, port_rest: 4301 },
    });
    const { AppShell } = await import("@/components/AppShell");
    render(<AppShell />);
    expect(screen.getByText(/Connected/i)).toBeInTheDocument();
  });
});

describe("appStore", () => {
  it("refreshDaemon sets daemonError on invoke failure", async () => {
    const { invoke } = jest.requireMock("@tauri-apps/api/core") as {
      invoke: jest.Mock;
    };
    invoke.mockRejectedValueOnce(new Error("daemon offline"));

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
