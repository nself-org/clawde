/**
 * Purpose: Unit tests for DaemonClient JSON-RPC 2.0 call/response and push event dispatch.
 * Inputs:  Mock WebSocket; DaemonClient instance.
 * Outputs: Jest assertions on call resolution, rejection, and push listener behaviour.
 * Constraints: Uses jest fake timers for reconnect schedule testing.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import { DaemonClient } from '../lib/daemon';

// ── Minimal WebSocket mock ────────────────────────────────────────────────────

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static OPEN = 1; // Required: DaemonClient checks ws.readyState !== WebSocket.OPEN
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((evt: { data: string }) => void) | null = null;
  readyState = 1; // OPEN
  sent: string[] = [];

  constructor(public url: string) {
    MockWebSocket.instances.push(this);
    // Simulate async open
    setTimeout(() => this.onopen?.(), 0);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = 3;
    this.onclose?.();
  }

  simulateMessage(data: object) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }
}

// Inject mock globally before importing daemon
// eslint-disable-next-line @typescript-eslint/no-explicit-any
(globalThis as any).WebSocket = MockWebSocket;

// ── Tests ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  MockWebSocket.instances = [];
  jest.useFakeTimers();
});

afterEach(() => {
  jest.useRealTimers();
});

describe('DaemonClient', () => {
  it('connects and resolves a JSON-RPC call', async () => {
    const client = new DaemonClient();
    client.connect();

    jest.runAllTimers(); // flush open
    await Promise.resolve();

    const ws = MockWebSocket.instances[0];

    // Call resolves when server replies
    const promise = client.call<{ sessions: [] }>('session.list');

    // Find the message the client sent and extract its id
    expect(ws.sent).toHaveLength(1);
    const req = JSON.parse(ws.sent[0]) as { id: number; method: string };
    expect(req.method).toBe('session.list');

    // Simulate daemon response
    ws.simulateMessage({ jsonrpc: '2.0', id: req.id, result: { sessions: [] } });

    const result = await promise;
    expect(result).toEqual({ sessions: [] });

    client.disconnect();
  });

  it('rejects when daemon returns an error', async () => {
    const client = new DaemonClient();
    client.connect();
    jest.runAllTimers();
    await Promise.resolve();

    const ws = MockWebSocket.instances[0];
    const promise = client.call('session.invalid');

    const req = JSON.parse(ws.sent[0]) as { id: number };
    ws.simulateMessage({
      jsonrpc: '2.0',
      id: req.id,
      error: { code: -32601, message: 'Method not found' },
    });

    await expect(promise).rejects.toThrow('Method not found');
    client.disconnect();
  });

  it('dispatches push events to listeners', async () => {
    const client = new DaemonClient();
    client.connect();
    jest.runAllTimers();
    await Promise.resolve();

    const ws = MockWebSocket.instances[0];
    const events: string[] = [];
    const unsub = client.addPushListener((evt) => events.push(evt.method));

    ws.simulateMessage({ method: 'toolCall.created', params: { id: '123' } });
    expect(events).toEqual(['toolCall.created']);

    unsub();
    ws.simulateMessage({ method: 'message.created', params: {} });
    expect(events).toHaveLength(1); // unsubscribed

    client.disconnect();
  });

  it('returns connectionMode=offline when not connected', () => {
    const client = new DaemonClient();
    expect(client.connectionMode).toBe('offline');
    expect(client.isConnected).toBe(false);
  });

  it('rejects calls when not connected', async () => {
    const client = new DaemonClient();
    await expect(client.call('session.list')).rejects.toThrow('Daemon not connected');
  });
});
