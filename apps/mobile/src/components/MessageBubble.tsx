/**
 * Purpose: Chat bubble for assistant/user messages with optional file-edit indicator.
 * Inputs:  Message entity; optional file-edit list from message metadata.
 * Outputs: Styled bubble (right=user, left=assistant) with file edit count badge.
 * Constraints: Markdown rendered via react-native-markdown-display for assistant messages.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React from 'react';
import { View, Text, StyleSheet } from 'react-native';
import Markdown from 'react-native-markdown-display';
import type { Message } from '@/types/api';

interface Props {
  message: Message;
}

const markdownStyles = {
  body: { color: '#EBEBF5', fontSize: 15, lineHeight: 22 },
  code_inline: {
    backgroundColor: '#2C2C2E',
    color: '#30D158',
    fontFamily: 'monospace',
    fontSize: 13,
    paddingHorizontal: 4,
    borderRadius: 3,
  },
  fence: {
    backgroundColor: '#2C2C2E',
    borderRadius: 8,
    padding: 10,
  },
  code_block: {
    backgroundColor: '#2C2C2E',
    color: '#30D158',
    fontFamily: 'monospace',
    fontSize: 12,
    lineHeight: 18,
  },
};

export function MessageBubble({ message }: Props) {
  const isUser = message.role === 'user';
  const fileEdits = message.metadata?.files ?? [];

  return (
    <View style={[styles.wrap, isUser ? styles.wrapUser : styles.wrapAssistant]}>
      <View style={[styles.bubble, isUser ? styles.bubbleUser : styles.bubbleAssistant]}>
        {isUser ? (
          <Text style={styles.userText}>{message.content}</Text>
        ) : (
          <Markdown style={markdownStyles as Record<string, object>}>{message.content}</Markdown>
        )}
        {fileEdits.length > 0 && (
          <View style={styles.fileEditsRow}>
            <Text style={styles.fileEditsText}>
              {fileEdits.length} file{fileEdits.length > 1 ? 's' : ''} edited
            </Text>
          </View>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { paddingHorizontal: 12, paddingVertical: 4 },
  wrapUser: { alignItems: 'flex-end' },
  wrapAssistant: { alignItems: 'flex-start' },
  bubble: {
    maxWidth: '85%',
    borderRadius: 16,
    padding: 12,
  },
  bubbleUser: {
    backgroundColor: '#007AFF',
    borderBottomRightRadius: 4,
  },
  bubbleAssistant: {
    backgroundColor: '#1C1C1E',
    borderBottomLeftRadius: 4,
  },
  userText: { color: '#fff', fontSize: 15, lineHeight: 22 },
  fileEditsRow: {
    marginTop: 6,
    backgroundColor: 'rgba(0,0,0,0.2)',
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 3,
    alignSelf: 'flex-start',
  },
  fileEditsText: { color: '#ffffffaa', fontSize: 11 },
});
