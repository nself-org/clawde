/**
 * Purpose: Secure storage of daemon API tokens and host pairing data.
 * Inputs:  Host ID, pairing token from QR scan.
 * Outputs: Stored credentials retrieved on app launch.
 * Constraints: Uses @nself/native-bridge ExpoSecureStore (encrypted native keychain).
 *              ExpoSecureStore.get() returns string | null directly; throws SecureStoreError
 *              on backend failure (non-critical — callers fall back to null).
 * SPORT: T-E1-06 — React Native Expo migration
 */

import { ExpoSecureStore } from '@nself/native-bridge';

// Singleton secure-store instance for this app.
const secureStore = new ExpoSecureStore();

const KEYS = {
  activeHostId: 'clawde_active_host_id',
  hosts: 'clawde_hosts',
  lastSessionId: 'clawde_last_session_id',
} as const;

export async function getActiveHostId(): Promise<string | null> {
  return secureStore.get(KEYS.activeHostId);
}

export async function setActiveHostId(id: string): Promise<void> {
  await secureStore.set(KEYS.activeHostId, id);
}

export async function getHostsJson(): Promise<string | null> {
  return secureStore.get(KEYS.hosts);
}

export async function setHostsJson(json: string): Promise<void> {
  await secureStore.set(KEYS.hosts, json);
}

export async function getLastSessionId(): Promise<string | null> {
  return secureStore.get(KEYS.lastSessionId);
}

export async function setLastSessionId(id: string): Promise<void> {
  await secureStore.set(KEYS.lastSessionId, id);
}
