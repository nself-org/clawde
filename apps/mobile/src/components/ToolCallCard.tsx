/**
 * Purpose: Renders a single tool call with approve/reject swipe actions.
 * Inputs:  ToolCall entity; onApprove/onReject callbacks.
 * Outputs: Swipeable card showing tool name, input summary, and status badge.
 * Constraints: Approve/reject only for status==='pending'.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React, { useCallback } from 'react';
import { View, Text, StyleSheet, TouchableOpacity } from 'react-native';
import { Swipeable } from 'react-native-gesture-handler';
import type { ToolCall, ToolCallStatus } from '@/types/api';

const STATUS_COLOR: Record<ToolCallStatus, string> = {
  pending: '#FFD60A',
  approved: '#30D158',
  rejected: '#FF453A',
  completed: '#636366',
  error: '#FF453A',
};

interface Props {
  toolCall: ToolCall;
  onApprove?: (id: string) => void;
  onReject?: (id: string) => void;
}

export function ToolCallCard({ toolCall, onApprove, onReject }: Props) {
  const isPending = toolCall.status === 'pending';

  const renderRight = useCallback(
    () =>
      isPending ? (
        <TouchableOpacity
          style={[styles.swipeAction, styles.swipeReject]}
          onPress={() => onReject?.(toolCall.id)}
        >
          <Text style={styles.swipeText}>Reject</Text>
        </TouchableOpacity>
      ) : null,
    [isPending, toolCall.id, onReject],
  );

  const renderLeft = useCallback(
    () =>
      isPending ? (
        <TouchableOpacity
          style={[styles.swipeAction, styles.swipeApprove]}
          onPress={() => onApprove?.(toolCall.id)}
        >
          <Text style={styles.swipeText}>Approve</Text>
        </TouchableOpacity>
      ) : null,
    [isPending, toolCall.id, onApprove],
  );

  // Summarise the input JSON for display (first 200 chars)
  const inputSummary = (() => {
    try {
      const s = typeof toolCall.input === 'string'
        ? toolCall.input
        : JSON.stringify(toolCall.input);
      return s.length > 200 ? s.slice(0, 200) + '…' : s;
    } catch {
      return String(toolCall.input);
    }
  })();

  return (
    <Swipeable renderRightActions={renderRight} renderLeftActions={renderLeft}>
      <View style={styles.card}>
        <View style={styles.header}>
          <Text style={styles.toolName}>{toolCall.tool}</Text>
          <View style={[styles.badge, { backgroundColor: STATUS_COLOR[toolCall.status] }]}>
            <Text style={styles.badgeText}>{toolCall.status}</Text>
          </View>
        </View>
        <Text style={styles.input} numberOfLines={4}>
          {inputSummary}
        </Text>
        {toolCall.output != null && (
          <Text style={styles.output} numberOfLines={2}>
            ↳ {typeof toolCall.output === 'string' ? toolCall.output : JSON.stringify(toolCall.output)}
          </Text>
        )}
      </View>
    </Swipeable>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: '#1C1C1E',
    padding: 12,
    borderLeftWidth: 3,
    borderLeftColor: '#007AFF',
  },
  header: { flexDirection: 'row', alignItems: 'center', marginBottom: 6, gap: 8 },
  toolName: { color: '#007AFF', fontSize: 13, fontWeight: '700', fontFamily: 'monospace', flex: 1 },
  badge: {
    borderRadius: 6,
    paddingHorizontal: 6,
    paddingVertical: 2,
  },
  badgeText: { color: '#000', fontSize: 11, fontWeight: '700' },
  input: { color: '#EBEBF5', fontSize: 12, fontFamily: 'monospace', lineHeight: 18 },
  output: { color: '#636366', fontSize: 12, fontFamily: 'monospace', marginTop: 4 },
  swipeAction: {
    width: 80,
    justifyContent: 'center',
    alignItems: 'center',
  },
  swipeApprove: { backgroundColor: '#30D158' },
  swipeReject: { backgroundColor: '#FF453A' },
  swipeText: { color: '#fff', fontWeight: '700', fontSize: 13 },
});
