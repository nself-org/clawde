/**
 * Purpose: Secure storage of daemon API tokens and host pairing data.
 * Inputs:  Host ID, pairing token from QR scan.
 * Outputs: Stored credentials retrieved on app launch.
 * Constraints: Uses @nself/native-bridge ExpoSecureStore (encrypted native keychain).
 *              Results are unwrapped with null-fallback on error (non-critical storage).
 * SPORT: T-E1-06 — React Native Expo migration
 */

import { ExpoSecureStore } from '@nself/native-bridge';
import { isOk } from '@nself/errors';

// Singleton secure-store instance for this app.
const secureStore = new ExpoSecureStore();

const KEYS = {
  activeHostId: 'clawde_active_host_id',
  hosts: 'clawde_hosts',
  lastSessionId: 'clawde_last_session_id',
} as const;

export async function getActiveHostId(): Promise<string | null> {
  const result = await secureStore.getItem(KEYS.activeHostId);
  return isOk(result) ? result.value : null;
}

export async function setActiveHostId(id: string): Promise<void> {
  await secureStore.setItem(KEYS.activeHostId, id);
}

export async function getHostsJson(): Promise<string | null> {
  const result = await secureStore.getItem(KEYS.hosts);
  return isOk(result) ? result.value : null;
}

export async function setHostsJson(json: string): Promise<void> {
  await secureStore.setItem(KEYS.hosts, json);
}

export async function getLastSessionId(): Promise<string | null> {
  const result = await secureStore.getItem(KEYS.lastSessionId);
  return isOk(result) ? result.value : null;
}

export async function setLastSessionId(id: string): Promise<void> {
  await secureStore.setItem(KEYS.lastSessionId, id);
}
