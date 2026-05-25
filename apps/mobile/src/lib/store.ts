/**
 * Purpose: Global Zustand store for connection state and active host.
 * Inputs:  DaemonClient connection events; host CRUD actions.
 * Outputs: isConnected, activeHostId, hosts list, connectionMode.
 * Constraints: Hosts persisted via SecureStore via auth.ts helpers.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import { create } from 'zustand';
import { daemonClient } from './daemon';
import * as auth from './auth';
import type { DaemonHost, ConnectionMode } from '../types/api';

interface ConnectionStore {
  isConnected: boolean;
  connectionMode: ConnectionMode;
  activeHostId: string | null;
  hosts: DaemonHost[];

  // Actions
  setConnected: (v: boolean) => void;
  setConnectionMode: (m: ConnectionMode) => void;
  setActiveHostId: (id: string | null) => void;
  loadHosts: () => Promise<void>;
  addHost: (host: DaemonHost) => Promise<void>;
  removeHost: (id: string) => Promise<void>;
  switchHost: (host: DaemonHost) => Promise<void>;
}

export const useConnectionStore = create<ConnectionStore>((set, get) => ({
  isConnected: false,
  connectionMode: 'offline',
  activeHostId: null,
  hosts: [],

  setConnected: (v) => set({ isConnected: v }),
  setConnectionMode: (m) => set({ connectionMode: m }),
  setActiveHostId: (id) => set({ activeHostId: id }),

  loadHosts: async () => {
    const json = await auth.getHostsJson();
    const hosts: DaemonHost[] = json ? (JSON.parse(json) as DaemonHost[]) : [];
    const activeHostId = await auth.getActiveHostId();
    set({ hosts, activeHostId });
  },

  addHost: async (host) => {
    const hosts = [...get().hosts, host];
    set({ hosts });
    await auth.setHostsJson(JSON.stringify(hosts));
  },

  removeHost: async (id) => {
    const hosts = get().hosts.filter((h) => h.id !== id);
    set({ hosts });
    await auth.setHostsJson(JSON.stringify(hosts));
  },

  switchHost: async (host) => {
    set({ activeHostId: host.id });
    await auth.setActiveHostId(host.id);
    daemonClient.setUrl(host.url);
    daemonClient.reconnect();
  },
}));

// Wire daemon connection events into the store
daemonClient.onConnectionChange((connected) => {
  useConnectionStore.getState().setConnected(connected);
  useConnectionStore.getState().setConnectionMode(
    connected ? daemonClient.connectionMode : 'offline',
  );
});
