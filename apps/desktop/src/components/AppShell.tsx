/**
 * Purpose: Root layout — navigation rail (10 tabs) + routed content area + status bar.
 * Inputs:  currentRoute from appStore, daemonStatus for connection indicator
 * Outputs: Rendered shell with nav rail on left and active screen on right
 * Constraints: Matches Flutter NavigationRail with Doctor badge on warn/error
 * SPORT: T-E1-07
 */

import React from "react";
import {
  MessageSquare, List, FolderOpen, GitBranch, LayoutDashboard,
  Search, Package, Stethoscope, BookOpen, Settings, Wifi, WifiOff,
  AlertCircle,
} from "lucide-react";
import { useAppStore } from "@/stores/appStore";
import type { NavRoute } from "@/types";
import { ChatScreen } from "./ChatScreen";
import { SessionsScreen } from "./SessionsScreen";
import { FilesScreen } from "./FilesScreen";
import { GitScreen } from "./GitScreen";
import { DashboardScreen } from "./DashboardScreen";
import { SearchScreen } from "./SearchScreen";
import { PacksScreen } from "./PacksScreen";
import { DoctorScreen } from "./DoctorScreen";
import { InstructionsScreen } from "./InstructionsScreen";
import { SettingsScreen } from "./SettingsScreen";

interface NavItem {
  route: NavRoute;
  label: string;
  icon: React.ReactNode;
  badge?: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { route: "chat", label: "Chat", icon: <MessageSquare size={20} /> },
  { route: "sessions", label: "Sessions", icon: <List size={20} /> },
  { route: "files", label: "Files", icon: <FolderOpen size={20} /> },
  { route: "git", label: "Git", icon: <GitBranch size={20} /> },
  { route: "dashboard", label: "Dashboard", icon: <LayoutDashboard size={20} /> },
  { route: "search", label: "Search", icon: <Search size={20} /> },
  { route: "packs", label: "Packs", icon: <Package size={20} /> },
  { route: "doctor", label: "Doctor", icon: <Stethoscope size={20} /> },
  { route: "instructions", label: "Instructions", icon: <BookOpen size={20} /> },
  { route: "settings", label: "Settings", icon: <Settings size={20} /> },
];

function ScreenContent({ route }: { route: NavRoute }) {
  switch (route) {
    case "chat": return <ChatScreen />;
    case "sessions": return <SessionsScreen />;
    case "files": return <FilesScreen />;
    case "git": return <GitScreen />;
    case "dashboard": return <DashboardScreen />;
    case "search": return <SearchScreen />;
    case "packs": return <PacksScreen />;
    case "doctor": return <DoctorScreen />;
    case "instructions": return <InstructionsScreen />;
    case "settings": return <SettingsScreen />;
    default: return <ChatScreen />;
  }
}

function ConnectionStatus() {
  const { daemonStatus, daemonError } = useAppStore((s) => ({
    daemonStatus: s.daemonStatus,
    daemonError: s.daemonError,
  }));

  if (daemonError) {
    return (
      <div className="flex items-center gap-1 text-xs text-red-400 px-2 py-1">
        <WifiOff size={12} />
        <span>Disconnected</span>
      </div>
    );
  }
  if (!daemonStatus?.running) {
    return (
      <div className="flex items-center gap-1 text-xs text-yellow-400 px-2 py-1">
        <AlertCircle size={12} />
        <span>Starting...</span>
      </div>
    );
  }
  return (
    <div className="flex items-center gap-1 text-xs text-green-400 px-2 py-1">
      <Wifi size={12} />
      <span>Connected</span>
    </div>
  );
}

function StatusBar() {
  const { daemonVersion, activeProjectPath } = useAppStore((s) => ({
    daemonVersion: s.daemonVersion,
    activeProjectPath: s.activeProjectPath,
  }));

  return (
    <div
      className="flex items-center justify-between px-3 py-1 text-xs text-gray-500"
      style={{ background: "#0a0e1a", borderTop: "1px solid #1e2638" }}
    >
      <span className="truncate max-w-xs" title={activeProjectPath ?? undefined}>
        {activeProjectPath ? `📁 ${activeProjectPath.split("/").pop()}` : "No project"}
      </span>
      <span>{daemonVersion ? `clawd v${daemonVersion}` : ""}</span>
    </div>
  );
}

export function AppShell() {
  const currentRoute = useAppStore((s) => s.currentRoute);
  const setRoute = useAppStore((s) => s.setRoute);

  return (
    <div className="flex h-screen w-screen overflow-hidden" style={{ background: "#030712" }}>
      {/* Navigation Rail */}
      <nav
        className="flex flex-col items-center py-2 gap-1 flex-shrink-0"
        style={{
          width: 56,
          background: "#0a0e1a",
          borderRight: "1px solid #1e2638",
        }}
      >
        {/* App icon / logo placeholder */}
        <div className="w-8 h-8 rounded mb-2 bg-blue-600 flex items-center justify-center text-white text-xs font-bold">
          C
        </div>

        {NAV_ITEMS.map((item) => {
          const active = currentRoute === item.route;
          return (
            <button
              key={item.route}
              title={item.label}
              onClick={() => setRoute(item.route)}
              className={[
                "relative flex items-center justify-center w-10 h-10 rounded-lg",
                "transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500",
                active
                  ? "bg-blue-600 text-white"
                  : "text-gray-400 hover:bg-gray-800 hover:text-gray-200",
              ].join(" ")}
            >
              {item.icon}
              {item.badge && (
                <span className="absolute top-1 right-1 w-2 h-2 bg-yellow-400 rounded-full" />
              )}
            </button>
          );
        })}

        {/* Spacer */}
        <div className="flex-1" />

        {/* Connection status indicator */}
        <div className="mb-1">
          <ConnectionStatus />
        </div>
      </nav>

      {/* Main content area */}
      <div className="flex flex-col flex-1 min-w-0">
        <div className="flex-1 overflow-hidden">
          <ScreenContent route={currentRoute} />
        </div>
        <StatusBar />
      </div>
    </div>
  );
}
