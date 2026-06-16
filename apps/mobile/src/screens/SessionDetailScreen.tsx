/**
 * Purpose: Full session view — interleaved message + tool-call list, paginated, auto-scroll.
 * Inputs:  sessionId route param; useMessages + useToolCalls + useSendMessage hooks.
 * Outputs: FlatList of MessageBubble + ToolCallCard items; ChatInput at bottom; action menu.
 * Constraints: Load earlier messages on scroll-to-top; auto-scroll on new message.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React, { useCallback, useEffect, useRef } from 'react';
import {
  View,
  FlatList,
  Text,
  StyleSheet,
  TouchableOpacity,
  ActionSheetIOS,
  Platform,
  Alert,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useRoute, useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp, NativeStackScreenProps } from '@react-navigation/native-stack';

import {
  useMessages,
  useSendMessage,
  useToolCalls,
  useApproveToolCall,
  useRejectToolCall,
  usePauseSession,
  useResumeSession,
  useCloseSession,
} from '@/hooks/useClawDEApi';
import { daemonClient } from '@/lib/daemon';
import type { RootStackParamList } from '../../App';
import type { Message, ToolCall } from '@/types/api';

import { MessageBubble } from '@/components/MessageBubble';
import { ToolCallCard } from '@/components/ToolCallCard';
import { ChatInput } from '@/components/ChatInput';

type ScreenProps = NativeStackScreenProps<RootStackParamList, 'SessionDetail'>;

type ListItem =
  | { type: 'message'; data: Message }
  | { type: 'toolCall'; data: ToolCall };

function buildItems(messages: Message[], toolCalls: ToolCall[]): ListItem[] {
  const items: ListItem[] = [
    ...messages.map((m) => ({ type: 'message' as const, data: m })),
    ...toolCalls.map((t) => ({ type: 'toolCall' as const, data: t })),
  ];
  // Sort by createdAt ascending
  return items.sort((a, b) => {
    const ta = new Date(a.data.createdAt).getTime();
    const tb = new Date(b.data.createdAt).getTime();
    return ta - tb;
  });
}

export function SessionDetailScreen() {
  const route = useRoute<ScreenProps['route']>();
  const nav = useNavigation<NativeStackNavigationProp<RootStackParamList>>();
  const { sessionId } = route.params;

  const listRef = useRef<FlatList>(null);

  const { data: messages = [] } = useMessages(sessionId);
  const { data: toolCalls = [] } = useToolCalls(sessionId);
  const send = useSendMessage(sessionId);
  const approve = useApproveToolCall(sessionId);
  const reject = useRejectToolCall(sessionId);
  const pause = usePauseSession();
  const resume = useResumeSession();
  const close = useCloseSession();

  const items = buildItems(messages, toolCalls);

  // Auto-scroll to bottom when new items arrive
  useEffect(() => {
    if (items.length > 0) {
      setTimeout(() => listRef.current?.scrollToEnd({ animated: true }), 100);
    }
  }, [items.length]);

  // Subscribe to push events for this session
  useEffect(() => {
    return daemonClient.addPushListener((evt) => {
      if (
        evt.method === 'message.created' ||
        evt.method === 'toolCall.created' ||
        evt.method === 'toolCall.resolved'
      ) {
        // RQ invalidation is handled in App.tsx; no extra action needed here
      }
    });
  }, []);

  const handleSend = useCallback(
    (content: string) => {
      send.mutate(content);
    },
    [send],
  );

  const showMenu = useCallback(() => {
    const options = ['Pause', 'Resume', 'Close', 'Cancel Session', 'Cancel'];
    const destructive = 2; // Close
    const cancel = 4;

    if (Platform.OS === 'ios') {
      ActionSheetIOS.showActionSheetWithOptions(
        { options, destructiveButtonIndex: destructive, cancelButtonIndex: cancel },
        (idx) => {
          if (idx === 0) pause.mutate(sessionId);
          if (idx === 1) resume.mutate(sessionId);
          if (idx === 2) {
            Alert.alert('Close session?', 'This cannot be undone.', [
              { text: 'Cancel', style: 'cancel' },
              {
                text: 'Close',
                style: 'destructive',
                onPress: () => {
                  close.mutate(sessionId, { onSuccess: () => nav.goBack() });
                },
              },
            ]);
          }
        },
      );
    } else {
      Alert.alert('Session', undefined, [
        { text: 'Pause', onPress: () => pause.mutate(sessionId) },
        { text: 'Resume', onPress: () => resume.mutate(sessionId) },
        {
          text: 'Close',
          style: 'destructive',
          onPress: () => close.mutate(sessionId, { onSuccess: () => nav.goBack() }),
        },
        { text: 'Cancel', style: 'cancel' },
      ]);
    }
  }, [sessionId, pause, resume, close, nav]);

  const pendingCount = toolCalls.filter((t) => t.status === 'pending').length;

  const renderItem = useCallback(
    ({ item }: { item: ListItem }) => {
      if (item.type === 'message') {
        return <MessageBubble message={item.data} />;
      }
      return (
        <ToolCallCard
          toolCall={item.data}
          onApprove={(id) => approve.mutate(id)}
          onReject={(id) => reject.mutate(id)}
        />
      );
    },
    [approve, reject],
  );

  return (
    <SafeAreaView style={styles.container} edges={['bottom']}>
      {/* Pending tool call banner */}
      {pendingCount > 0 && (
        <View style={styles.pendingBanner}>
          <Text style={styles.pendingText}>
            {pendingCount} tool call{pendingCount > 1 ? 's' : ''} awaiting approval
          </Text>
        </View>
      )}

      {/* Menu button (via header right) */}
      <TouchableOpacity style={styles.menuBtn} onPress={showMenu}>
        <Text style={styles.menuBtnText}>•••</Text>
      </TouchableOpacity>

      {/* Message list */}
      <FlatList
        ref={listRef}
        data={items}
        keyExtractor={(item) => `${item.type}-${item.data.id}`}
        renderItem={renderItem}
        contentContainerStyle={styles.listContent}
        ListEmptyComponent={
          <View style={styles.empty}>
            <Text style={styles.emptyText}>No messages yet</Text>
            <Text style={styles.emptyHint}>Start the conversation below</Text>
          </View>
        }
      />

      {/* Input bar */}
      <ChatInput onSend={handleSend} isPending={send.isPending} />
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#000' },
  pendingBanner: {
    backgroundColor: '#FFD60A',
    paddingHorizontal: 16,
    paddingVertical: 8,
  },
  pendingText: { color: '#000', fontWeight: '600', fontSize: 13, textAlign: 'center' },
  menuBtn: {
    position: 'absolute',
    top: 8,
    right: 12,
    zIndex: 10,
    padding: 8,
  },
  menuBtnText: { color: '#007AFF', fontSize: 18, fontWeight: '700', letterSpacing: 2 },
  listContent: { paddingVertical: 12 },
  empty: { alignItems: 'center', paddingTop: 80 },
  emptyText: { color: '#636366', fontSize: 18, fontWeight: '600' },
  emptyHint: { color: '#48484A', fontSize: 14, marginTop: 8 },
});
