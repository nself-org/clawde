/**
 * Purpose: LSP stdio bridge — spawns `nself lsp stdio` as a child process and
 *          exposes a LanguageClient-compatible transport for VS Code extension
 *          via the nself-lsp npm package.
 * Inputs:  No arguments — reads daemon port from tauriApi at connect time.
 * Outputs: LspBridge instance with start/stop/request/notify methods.
 * Constraints: Only one bridge instance active at a time. stop() MUST kill the
 *              child process to avoid zombie `nself lsp` procs on window close.
 *              Uses Tauri shell plugin; stdout/stderr listeners attach to Command.
 * SPORT: T-P3-E5-W1-S2-T02
 */

import { Command } from "@tauri-apps/plugin-shell";
import type { Child } from "@tauri-apps/plugin-shell";
import {
  type Result,
  type ClawDEError,
  ok,
  err,
  lspError,
  daemonOffline,
  fromThrown,
} from "./result";

// ── Types ─────────────────────────────────────────────────────────────────────

export interface LspRequest {
  jsonrpc: "2.0";
  id: number | string;
  method: string;
  params?: unknown;
}

export interface LspResponse {
  jsonrpc: "2.0";
  id: number | string | null;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

export interface LspNotification {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
}

export type LspMessage = LspResponse | LspNotification;

// ── LSP Content-Length framing helpers ───────────────────────────────────────

function encodeMessage(payload: unknown): string {
  const body = JSON.stringify(payload);
  return `Content-Length: ${body.length}\r\n\r\n${body}`;
}

interface ParseResult {
  messages: LspMessage[];
  remaining: string;
}

function parseMessages(raw: string): ParseResult {
  const messages: LspMessage[] = [];
  let remaining = raw;

  for (;;) {
    const headerEnd = remaining.indexOf("\r\n\r\n");
    if (headerEnd === -1) break;
    const header = remaining.slice(0, headerEnd);
    const match = /Content-Length:\s*(\d+)/i.exec(header);
    if (!match) break;
    const contentLength = parseInt(match[1], 10);
    const bodyStart = headerEnd + 4;
    if (remaining.length < bodyStart + contentLength) break;
    const body = remaining.slice(bodyStart, bodyStart + contentLength);
    remaining = remaining.slice(bodyStart + contentLength);
    try {
      messages.push(JSON.parse(body) as LspMessage);
    } catch {
      // skip malformed frame
    }
  }

  return { messages, remaining };
}

// ── Bridge class ──────────────────────────────────────────────────────────────

type MessageHandler = (msg: LspMessage) => void;

export class LspBridge {
  private child: Child | null = null;
  private nextId = 1;
  private pending = new Map<
    number | string,
    { resolve: (r: unknown) => void; reject: (e: ClawDEError) => void }
  >();
  private onMessage: MessageHandler | null = null;
  private rawBuffer = "";

  /** Register a handler to receive LSP notifications. */
  setOnMessage(handler: MessageHandler) {
    this.onMessage = handler;
  }

  /** Start the LSP stdio bridge by spawning `nself lsp stdio`. */
  async start(): Promise<Result<void, ClawDEError>> {
    if (this.child) return ok(undefined); // already running

    try {
      const cmd = Command.create("nself", ["lsp", "stdio"]);

      cmd.stdout.on("data", (chunk: string) => {
        this.rawBuffer += chunk;
        const { messages, remaining } = parseMessages(this.rawBuffer);
        this.rawBuffer = remaining;
        for (const msg of messages) {
          this.handleMessage(msg);
        }
      });

      cmd.stderr.on("data", (line: string) => {
        if (
          line.includes("daemon not running") ||
          line.includes("connection refused")
        ) {
          this.rejectAllPending(daemonOffline("clawd daemon is not running"));
        }
      });

      this.child = await cmd.spawn();
      return ok(undefined);
    } catch (e) {
      return err(fromThrown(e));
    }
  }

  /** Stop the bridge and kill the `nself lsp stdio` child process. */
  async stop(): Promise<void> {
    if (!this.child) return;
    try {
      await this.child.kill();
    } catch {
      // process may have already exited
    }
    this.child = null;
    this.rawBuffer = "";
    this.rejectAllPending(lspError("LSP bridge stopped"));
  }

  /** Send an LSP request and await the typed response. */
  async request<T = unknown>(
    method: string,
    params?: unknown
  ): Promise<Result<T, ClawDEError>> {
    if (!this.child) {
      return err(daemonOffline("LSP bridge is not started"));
    }
    const id = this.nextId++;
    const req: LspRequest = {
      jsonrpc: "2.0",
      id,
      method,
      ...(params !== undefined ? { params } : {}),
    };
    const encoded = encodeMessage(req);

    return new Promise<Result<T, ClawDEError>>((resolve) => {
      this.pending.set(id, {
        resolve: (r) => resolve(ok(r as T)),
        reject: (e) => resolve(err(e)),
      });

      this.child!.write(encoded).catch((writeErr: unknown) => {
        this.pending.delete(id);
        resolve(err(fromThrown(writeErr)));
      });
    });
  }

  /** Send an LSP notification (fire-and-forget, no response expected). */
  async notify(
    method: string,
    params?: unknown
  ): Promise<Result<void, ClawDEError>> {
    if (!this.child) {
      return err(daemonOffline("LSP bridge is not started"));
    }
    const notif: LspNotification = {
      jsonrpc: "2.0",
      method,
      ...(params !== undefined ? { params } : {}),
    };
    try {
      await this.child.write(encodeMessage(notif));
      return ok(undefined);
    } catch (e) {
      return err(fromThrown(e));
    }
  }

  get running(): boolean {
    return this.child !== null;
  }

  // ── Private ─────────────────────────────────────────────────────────────────

  private handleMessage(msg: LspMessage) {
    // It's a response if it has an `id` field (even null for errors without id)
    if ("id" in msg) {
      const response = msg as LspResponse;
      if (response.id !== null && response.id !== undefined) {
        const pending = this.pending.get(response.id);
        if (pending) {
          this.pending.delete(response.id);
          if (response.error) {
            pending.reject(lspError(response.error.message, response.error.code));
          } else {
            pending.resolve(response.result);
          }
          return;
        }
      }
    }
    // Notification or unmatched response → forward to handler
    this.onMessage?.(msg);
  }

  private rejectAllPending(error: ClawDEError) {
    const entries = [...this.pending.entries()];
    this.pending.clear();
    for (const [, handler] of entries) {
      handler.reject(error);
    }
  }
}

/** Singleton bridge instance — one per desktop window. */
export const lspBridge = new LspBridge();
