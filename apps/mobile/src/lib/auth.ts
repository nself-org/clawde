/**
 * Purpose: Secure storage of daemon API tokens and host pairing data.
 * Inputs:  Host ID, pairing token from QR scan.
 * Outputs: Stored credentials retrieved on app launch.
 * Constraints: Uses Expo SecureStore (encrypted native keychain).
 * SPORT: T-E1-06 — React Native Expo migration
 */

import * as SecureStore from 'expo-secure-store';

const KEYS = {
  activeHostId: 'clawde_active_host_id',
  hosts: 'clawde_hosts',
  lastSessionId: 'clawde_last_session_id',
} as const;

export async function getActiveHostId(): Promise<string | null> {
  return SecureStore.getItemAsync(KEYS.activeHostId);
}

export async function setActiveHostId(id: string): Promise<void> {
  await SecureStore.setItemAsync(KEYS.activeHostId, id);
}

export async function getHostsJson(): Promise<string | null> {
  return SecureStore.getItemAsync(KEYS.hosts);
}

export async function setHostsJson(json: string): Promise<void> {
  await SecureStore.setItemAsync(KEYS.hosts, json);
}

export async function getLastSessionId(): Promise<string | null> {
  return SecureStore.getItemAsync(KEYS.lastSessionId);
}

export async function setLastSessionId(id: string): Promise<void> {
  await SecureStore.setItemAsync(KEYS.lastSessionId, id);
}
