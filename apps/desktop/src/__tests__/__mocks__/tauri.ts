/**
 * Purpose: Mock @tauri-apps/* modules for jest test environment.
 * Inputs:  Jest moduleNameMapper resolving @tauri-apps/api/core → this file
 * Outputs: Stub implementations of invoke(), window events, etc.
 * Constraints: Must not import any Tauri native code; pure JS stubs only
 * SPORT: T-E1-07
 */

// Core (invoke)
export const invoke = jest.fn().mockResolvedValue(null);

// App
export const getVersion = jest.fn().mockResolvedValue("0.3.2");

// Shell
export const Command = {
  create: jest.fn(() => ({
    execute: jest.fn().mockResolvedValue({ code: 0, stdout: "", stderr: "" }),
  })),
};
export const open = jest.fn().mockResolvedValue(undefined);

// Dialog
export const open: jest.Mock = jest.fn().mockResolvedValue(null);
export const save: jest.Mock = jest.fn().mockResolvedValue(null);

// FS
export const readTextFile = jest.fn().mockResolvedValue("");
export const writeTextFile = jest.fn().mockResolvedValue(undefined);
export const readDir = jest.fn().mockResolvedValue([]);
export const exists = jest.fn().mockResolvedValue(false);

// Global shortcut
export const register = jest.fn().mockResolvedValue(undefined);
export const unregisterAll = jest.fn().mockResolvedValue(undefined);

// Process
export const exit = jest.fn().mockResolvedValue(undefined);

// Notification
export const sendNotification = jest.fn();
export const requestPermission = jest.fn().mockResolvedValue("granted");
export const isPermissionGranted = jest.fn().mockResolvedValue(true);

// Path
export const join = jest.fn((...parts: string[]) => Promise.resolve(parts.join("/")));

// Updater
export const check = jest.fn().mockResolvedValue(null);
