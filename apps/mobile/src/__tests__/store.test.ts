/**
 * Purpose: Unit tests for connection Zustand store — host CRUD and active host tracking.
 * Inputs:  useConnectionStore actions; mock @nself/native-bridge ExpoSecureStore.
 * Outputs: Jest assertions on state transitions after add/remove/switch.
 * Constraints: ExpoSecureStore is mocked; daemonClient.setUrl/reconnect are stubbed.
 * SPORT: T-E1-06 — React Native Expo migration
 */

// Mock @nself/native-bridge ExpoSecureStore before importing the store
const mockStore: Record<string, string> = {};
jest.mock('@nself/native-bridge', () => {
  return {
    ExpoSecureStore: jest.fn().mockImplementation(() => ({
      getItem: jest.fn((key: string) =>
        Promise.resolve({ _tag: 'Ok' as const, value: mockStore[key] ?? null }),
      ),
      setItem: jest.fn((key: string, val: string) => {
        mockStore[key] = val;
        return Promise.resolve({ _tag: 'Ok' as const, value: undefined });
      }),
      deleteItem: jest.fn((key: string) => {
        delete mockStore[key];
        return Promise.resolve({ _tag: 'Ok' as const, value: undefined });
      }),
    })),
  };
});

// Mock @nself/errors isOk helper (simple passthrough for the Ok tag)
jest.mock('@nself/errors', () => ({
  isOk: (r: { _tag: string }) => r._tag === 'Ok',
}));

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
  // Clear in-memory mock store
  Object.keys(mockStore).forEach((k) => delete mockStore[k]);
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
