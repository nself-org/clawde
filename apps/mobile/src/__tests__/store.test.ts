/**
 * Purpose: Unit tests for connection Zustand store — host CRUD and active host tracking.
 * Inputs:  useConnectionStore actions; mock SecureStore.
 * Outputs: Jest assertions on state transitions after add/remove/switch.
 * Constraints: SecureStore is mocked; daemonClient.setUrl/reconnect are stubbed.
 * SPORT: T-E1-06 — React Native Expo migration
 */

// Mock expo-secure-store before importing the store
jest.mock('expo-secure-store', () => {
  const store: Record<string, string> = {};
  return {
    getItemAsync: jest.fn((key: string) => Promise.resolve(store[key] ?? null)),
    setItemAsync: jest.fn((key: string, val: string) => {
      store[key] = val;
      return Promise.resolve();
    }),
  };
});

// Mock the daemon so switchHost doesn't try to open a WebSocket
jest.mock('../lib/daemon', () => ({
  daemonClient: {
    setUrl: jest.fn(),
    reconnect: jest.fn(),
    onConnectionChange: jest.fn(),
    connectionMode: 'offline',
  },
}));

import { useConnectionStore } from '../lib/store';
import type { DaemonHost } from '../types/api';

const hostA: DaemonHost = { id: 'a', name: 'Host A', url: 'ws://10.0.0.1:4300', isPaired: false };
const hostB: DaemonHost = { id: 'b', name: 'Host B', url: 'ws://10.0.0.2:4300', isPaired: true };

beforeEach(() => {
  // Reset store to initial state between tests
  useConnectionStore.setState({
    isConnected: false,
    connectionMode: 'offline',
    activeHostId: null,
    hosts: [],
  });
});

describe('useConnectionStore', () => {
  it('adds a host', async () => {
    await useConnectionStore.getState().addHost(hostA);
    expect(useConnectionStore.getState().hosts).toHaveLength(1);
    expect(useConnectionStore.getState().hosts[0].id).toBe('a');
  });

  it('removes a host', async () => {
    await useConnectionStore.getState().addHost(hostA);
    await useConnectionStore.getState().addHost(hostB);
    await useConnectionStore.getState().removeHost('a');
    const { hosts } = useConnectionStore.getState();
    expect(hosts).toHaveLength(1);
    expect(hosts[0].id).toBe('b');
  });

  it('sets activeHostId on switchHost', async () => {
    await useConnectionStore.getState().addHost(hostA);
    await useConnectionStore.getState().switchHost(hostA);
    expect(useConnectionStore.getState().activeHostId).toBe('a');
  });

  it('setConnected and setConnectionMode update state', () => {
    useConnectionStore.getState().setConnected(true);
    useConnectionStore.getState().setConnectionMode('lan');
    const state = useConnectionStore.getState();
    expect(state.isConnected).toBe(true);
    expect(state.connectionMode).toBe('lan');
  });
});
