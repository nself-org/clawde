/**
 * Purpose: Small pill showing current daemon connection mode (LAN / Relay / Offline).
 * Inputs:  connectionMode from useConnectionStore.
 * Outputs: Coloured pill with text label.
 * Constraints: No taps; purely informational.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React from 'react';
import { View, Text, StyleSheet } from 'react-native';
import { useConnectionStore } from '@/lib/store';
import type { ConnectionMode } from '@/types/api';

const MODE_COLOR: Record<ConnectionMode, string> = {
  lan: '#30D158',
  relay: '#FFD60A',
  offline: '#FF453A',
};

const MODE_LABEL: Record<ConnectionMode, string> = {
  lan: 'LAN',
  relay: 'Relay',
  offline: 'Offline',
};

export function ConnectionBadge() {
  const mode = useConnectionStore((s) => s.connectionMode);

  return (
    <View style={[styles.pill, { backgroundColor: MODE_COLOR[mode] + '22' }]}>
      <View style={[styles.dot, { backgroundColor: MODE_COLOR[mode] }]} />
      <Text style={[styles.label, { color: MODE_COLOR[mode] }]}>{MODE_LABEL[mode]}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  pill: {
    flexDirection: 'row',
    alignItems: 'center',
    borderRadius: 12,
    paddingHorizontal: 8,
    paddingVertical: 4,
    gap: 4,
  },
  dot: { width: 7, height: 7, borderRadius: 4 },
  label: { fontSize: 12, fontWeight: '600' },
});
