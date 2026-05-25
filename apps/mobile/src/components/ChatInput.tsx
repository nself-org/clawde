/**
 * Purpose: Multi-line text input bar for sending messages to a session.
 * Inputs:  onSend callback; isPending flag (disables while mutation in flight).
 * Outputs: Controlled TextInput with send button; auto-expands up to 5 lines.
 * Constraints: Clears after successful send. Disabled when no content or isPending.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React, { useState, useCallback } from 'react';
import {
  View,
  TextInput,
  TouchableOpacity,
  StyleSheet,
  ActivityIndicator,
  Text,
} from 'react-native';

interface Props {
  onSend: (content: string) => void;
  isPending?: boolean;
}

export function ChatInput({ onSend, isPending = false }: Props) {
  const [text, setText] = useState('');

  const handleSend = useCallback(() => {
    const trimmed = text.trim();
    if (!trimmed || isPending) return;
    onSend(trimmed);
    setText('');
  }, [text, isPending, onSend]);

  const canSend = text.trim().length > 0 && !isPending;

  return (
    <View style={styles.container}>
      <TextInput
        style={styles.input}
        value={text}
        onChangeText={setText}
        placeholder="Message…"
        placeholderTextColor="#636366"
        multiline
        maxLength={4000}
        numberOfLines={1}
        // Allow up to ~5 lines before scrolling internally
        onSubmitEditing={handleSend}
        blurOnSubmit={false}
        editable={!isPending}
      />
      <TouchableOpacity
        style={[styles.sendBtn, !canSend && styles.sendBtnDisabled]}
        onPress={handleSend}
        disabled={!canSend}
      >
        {isPending ? (
          <ActivityIndicator color="#fff" size="small" />
        ) : (
          <Text style={styles.sendIcon}>↑</Text>
        )}
      </TouchableOpacity>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    backgroundColor: '#1C1C1E',
    borderTopWidth: 1,
    borderTopColor: '#2C2C2E',
    paddingHorizontal: 12,
    paddingVertical: 8,
    gap: 8,
  },
  input: {
    flex: 1,
    backgroundColor: '#2C2C2E',
    borderRadius: 20,
    paddingHorizontal: 14,
    paddingVertical: 10,
    color: '#fff',
    fontSize: 15,
    maxHeight: 120,
  },
  sendBtn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: '#007AFF',
    alignItems: 'center',
    justifyContent: 'center',
  },
  sendBtnDisabled: { backgroundColor: '#2C2C2E' },
  sendIcon: { color: '#fff', fontSize: 18, fontWeight: '700' },
});
