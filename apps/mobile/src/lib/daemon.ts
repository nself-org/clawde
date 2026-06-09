/**
 * Purpose: WebSocket client for the ClawDE Rust daemon on port 4300.
 * Inputs:  daemonUrl (ws://host:4300), JSON-RPC 2.0 messages.
 * Outputs: Typed RPC responses; onEvent callbacks for push events.
 * Constraints: Must handle reconnection, offline gracefully.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import type { DaemonPushEvent } from '../types/api';

type RpcCallback = (result: unknown, error?: { code: number; message: string }) => void;

export class DaemonClient {
  private ws: WebSocket | null = null;
  private _url = 'ws://127.0.0.1:4300';
  private _connected = false;
  private _pending = new Map<number, RpcCallback>();
  private _nextId = 1;
  private _reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private _listeners: Array<(event: DaemonPushEvent) => void> = [];
  private _onConnectionChange: ((connected: boolean) => void) | null = null;
  connectionMode: 'lan' | 'relay' | 'offline' = 'offline';

  get isConnected(): boolean {
    return this._connected;
  }

  get url(): string {
    return this._url;
  }

  setUrl(url: string): void {
    this._url = url;
  }

  onConnectionChange(cb: (connected: boolean) => void): void {
    this._onConnectionChange = cb;
  }

  addPushListener(cb: (event: DaemonPushEvent) => void): () => void {
    this._listeners.push(cb);
    return () => {
      this._listeners = this._listeners.filter((l) => l !== cb);
    };
  }

  connect(): void {
    if (this.ws) {
      this.ws.close();
    }
    try {
      this.ws = new WebSocket(this._url);
      this.ws.onopen = () => {
        this._connected = true;
        this.connectionMode = this._url.includes('relay') ? 'relay' : 'lan';
        this._onConnectionChange?.(true);
      };
      this.ws.onclose = () => {
        this._connected = false;
        this.connectionMode = 'offline';
        this._onConnectionChange?.(false);
        this._scheduleReconnect();
      };
      this.ws.onerror = () => {
        this._connected = false;
        this.connectionMode = 'offline';
      };
      this.ws.onmessage = (evt) => {
        this._handleMessage(evt.data as string);
      };
    } catch {
      this._scheduleReconnect();
    }
  }

  disconnect(): void {
    if (this._reconnectTimer) {
      clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
    this._connected = false;
    this.connectionMode = 'offline';
  }

  reconnect(): void {
    this.disconnect();
    this.connect();
  }

  async call<T = unknown>(method: string, params?: unknown): Promise<T> {
    return new Promise((resolve, reject) => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        reject(new Error('Daemon not connected'));
        return;
      }
      const id = this._nextId++;
      const msg = JSON.stringify({ jsonrpc: '2.0', id, method, params });
      this._pending.set(id, (result, error) => {
        if (error) {
          reject(new Error(error.message));
        } else {
          resolve(result as T);
        }
      });
      this.ws.send(msg);
    });
  }

  private _handleMessage(raw: string): void {
    try {
      const msg = JSON.parse(raw) as {
        id?: number;
        result?: unknown;
        error?: { code: number; message: string };
        method?: string;
        params?: Record<string, unknown>;
      };

      // JSON-RPC response
      if (msg.id !== undefined) {
        const cb = this._pending.get(msg.id);
        if (cb) {
          this._pending.delete(msg.id);
          cb(msg.result, msg.error);
        }
        return;
      }

      // Push notification (no id)
      if (msg.method) {
        const event: DaemonPushEvent = {
          method: msg.method,
          params: msg.params,
        };
        this._listeners.forEach((l) => l(event));
      }
    } catch {
      // Malformed message — ignore
    }
  }

  private _scheduleReconnect(): void {
    if (this._reconnectTimer) return;
    this._reconnectTimer = setTimeout(() => {
      this._reconnectTimer = null;
      this.connect();
    }, 3000);
  }
}

export const daemonClient = new DaemonClient();
