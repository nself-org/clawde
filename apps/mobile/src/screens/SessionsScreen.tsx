/**
 * Purpose: Session list with filter chips, swipe actions, and new-session modal.
 * Inputs:  useSessions RQ query; useCreateSession/usePauseSession/useResumeSession/useCloseSession mutations.
 * Outputs: Filtered FlatList; bottom-sheet modal for repo-path input.
 * Constraints: Swipe right = pause/resume; swipe left = close. Only running sessions can be paused.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React, { useState, useCallback } from 'react';
import {
  View,
  Text,
  FlatList,
  TouchableOpacity,
  StyleSheet,
  Modal,
  TextInput,
  ActivityIndicator,
  Alert,
  RefreshControl,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { Swipeable } from 'react-native-gesture-handler';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';

import {
  useSessions,
  useCreateSession,
  usePauseSession,
  useResumeSession,
  useCloseSession,
} from '@/hooks/useClawDEApi';
import type { Session, SessionStatus } from '@/types/api';
import type { RootStackParamList } from '../../App';

type Nav = NativeStackNavigationProp<RootStackParamList>;

// ── Status chip colours ───────────────────────────────────────────────────────

const STATUS_COLOR: Record<SessionStatus, string> = {
  running: '#30D158',
  paused: '#FFD60A',
  completed: '#636366',
  error: '#FF453A',
};

type FilterValue = 'all' | SessionStatus;

const FILTERS: { label: string; value: FilterValue }[] = [
  { label: 'All', value: 'all' },
  { label: 'Running', value: 'running' },
  { label: 'Paused', value: 'paused' },
  { label: 'Done', value: 'completed' },
  { label: 'Error', value: 'error' },
];

// ── Session row ────────────────────────────────────────────────────────────────

function SessionRow({ session }: { session: Session }) {
  const nav = useNavigation<Nav>();
  const pause = usePauseSession();
  const resume = useResumeSession();
  const close = useCloseSession();

  const togglePause = useCallback(() => {
    if (session.status === 'running') {
      pause.mutate(session.id);
    } else if (session.status === 'paused') {
      resume.mutate(session.id);
    }
  }, [session.id, session.status, pause, resume]);

  const handleClose = useCallback(() => {
    Alert.alert('Close session?', 'This cannot be undone.', [
      { text: 'Cancel', style: 'cancel' },
      { text: 'Close', style: 'destructive', onPress: () => close.mutate(session.id) },
    ]);
  }, [session.id, close]);

  const renderRight = () => (
    <TouchableOpacity style={[styles.swipeAction, styles.swipeClose]} onPress={handleClose}>
      <Text style={styles.swipeActionText}>Close</Text>
    </TouchableOpacity>
  );

  const renderLeft = () => {
    const canToggle = session.status === 'running' || session.status === 'paused';
    if (!canToggle) return null;
    return (
      <TouchableOpacity style={[styles.swipeAction, styles.swipePause]} onPress={togglePause}>
        <Text style={styles.swipeActionText}>
          {session.status === 'running' ? 'Pause' : 'Resume'}
        </Text>
      </TouchableOpacity>
    );
  };

  const repoName = session.repoPath.split('/').pop() ?? session.repoPath;

  return (
    <Swipeable renderRightActions={renderRight} renderLeftActions={renderLeft}>
      <TouchableOpacity
        style={styles.row}
        onPress={() => nav.navigate('SessionDetail', { sessionId: session.id })}
        activeOpacity={0.7}
      >
        <View style={[styles.statusDot, { backgroundColor: STATUS_COLOR[session.status] }]} />
        <View style={styles.rowText}>
          <Text style={styles.rowTitle} numberOfLines={1}>
            {repoName}
          </Text>
          <Text style={styles.rowSub} numberOfLines={1}>
            {session.repoPath}
          </Text>
        </View>
        <Text style={styles.rowChevron}>›</Text>
      </TouchableOpacity>
    </Swipeable>
  );
}

// ── New session modal ─────────────────────────────────────────────────────────

function NewSessionModal({
  visible,
  onClose,
}: {
  visible: boolean;
  onClose: () => void;
}) {
  const [repoPath, setRepoPath] = useState('');
  const create = useCreateSession();

  const submit = useCallback(() => {
    if (!repoPath.trim()) return;
    create.mutate(repoPath.trim(), { onSuccess: () => { setRepoPath(''); onClose(); } });
  }, [repoPath, create, onClose]);

  return (
    <Modal visible={visible} transparent animationType="slide" onRequestClose={onClose}>
      <View style={styles.modalOverlay}>
        <View style={styles.modalSheet}>
          <Text style={styles.modalTitle}>New Session</Text>
          <TextInput
            style={styles.input}
            placeholder="Repository path"
            placeholderTextColor="#636366"
            value={repoPath}
            onChangeText={setRepoPath}
            autoCapitalize="none"
            autoCorrect={false}
            onSubmitEditing={submit}
          />
          <TouchableOpacity
            style={[styles.modalBtn, !repoPath.trim() && styles.modalBtnDisabled]}
            onPress={submit}
            disabled={!repoPath.trim() || create.isPending}
          >
            {create.isPending ? (
              <ActivityIndicator color="#fff" />
            ) : (
              <Text style={styles.modalBtnText}>Start</Text>
            )}
          </TouchableOpacity>
          <TouchableOpacity style={styles.modalCancel} onPress={onClose}>
            <Text style={styles.modalCancelText}>Cancel</Text>
          </TouchableOpacity>
        </View>
      </View>
    </Modal>
  );
}

// ── Main screen ───────────────────────────────────────────────────────────────

export function SessionsScreen() {
  const [filter, setFilter] = useState<FilterValue>('all');
  const [showModal, setShowModal] = useState(false);
  const { data: sessions = [], isLoading, refetch } = useSessions();

  const filtered =
    filter === 'all' ? sessions : sessions.filter((s) => s.status === filter);

  return (
    <SafeAreaView style={styles.container} edges={['top']}>
      {/* Header */}
      <View style={styles.header}>
        <Text style={styles.headerTitle}>Sessions</Text>
        <TouchableOpacity onPress={() => setShowModal(true)}>
          <Text style={styles.headerAdd}>＋</Text>
        </TouchableOpacity>
      </View>

      {/* Filter chips */}
      <View style={styles.chips}>
        {FILTERS.map((f) => (
          <TouchableOpacity
            key={f.value}
            style={[styles.chip, filter === f.value && styles.chipActive]}
            onPress={() => setFilter(f.value)}
          >
            <Text style={[styles.chipText, filter === f.value && styles.chipTextActive]}>
              {f.label}
            </Text>
          </TouchableOpacity>
        ))}
      </View>

      {/* List */}
      <FlatList
        data={filtered}
        keyExtractor={(s) => s.id}
        renderItem={({ item }) => <SessionRow session={item} />}
        ItemSeparatorComponent={() => <View style={styles.separator} />}
        refreshControl={<RefreshControl refreshing={isLoading} onRefresh={refetch} tintColor="#007AFF" />}
        ListEmptyComponent={
          isLoading ? null : (
            <View style={styles.empty}>
              <Text style={styles.emptyText}>No sessions</Text>
              <Text style={styles.emptyHint}>Tap + to start one</Text>
            </View>
          )
        }
      />

      <NewSessionModal visible={showModal} onClose={() => setShowModal(false)} />
    </SafeAreaView>
  );
}

// ── Styles ─────────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#000' },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingVertical: 12,
  },
  headerTitle: { color: '#fff', fontSize: 22, fontWeight: '700' },
  headerAdd: { color: '#007AFF', fontSize: 28 },
  chips: { flexDirection: 'row', paddingHorizontal: 12, paddingBottom: 8, gap: 8 },
  chip: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 16,
    backgroundColor: '#1C1C1E',
  },
  chipActive: { backgroundColor: '#007AFF' },
  chipText: { color: '#8E8E93', fontSize: 13 },
  chipTextActive: { color: '#fff', fontWeight: '600' },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: '#1C1C1E',
    paddingHorizontal: 16,
    paddingVertical: 14,
  },
  statusDot: { width: 10, height: 10, borderRadius: 5, marginRight: 12 },
  rowText: { flex: 1 },
  rowTitle: { color: '#fff', fontSize: 16, fontWeight: '600' },
  rowSub: { color: '#636366', fontSize: 12, marginTop: 2 },
  rowChevron: { color: '#636366', fontSize: 20 },
  separator: { height: 1, backgroundColor: '#2C2C2E' },
  swipeAction: {
    justifyContent: 'center',
    alignItems: 'center',
    width: 80,
  },
  swipeClose: { backgroundColor: '#FF453A' },
  swipePause: { backgroundColor: '#FFD60A' },
  swipeActionText: { color: '#fff', fontWeight: '700', fontSize: 13 },
  empty: { alignItems: 'center', paddingTop: 80 },
  emptyText: { color: '#636366', fontSize: 18, fontWeight: '600' },
  emptyHint: { color: '#48484A', fontSize: 14, marginTop: 8 },
  modalOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.6)',
    justifyContent: 'flex-end',
  },
  modalSheet: {
    backgroundColor: '#1C1C1E',
    borderTopLeftRadius: 16,
    borderTopRightRadius: 16,
    padding: 24,
    paddingBottom: 40,
  },
  modalTitle: { color: '#fff', fontSize: 18, fontWeight: '700', marginBottom: 16 },
  input: {
    backgroundColor: '#2C2C2E',
    borderRadius: 10,
    color: '#fff',
    padding: 14,
    fontSize: 16,
    marginBottom: 16,
  },
  modalBtn: {
    backgroundColor: '#007AFF',
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
  },
  modalBtnDisabled: { opacity: 0.4 },
  modalBtnText: { color: '#fff', fontSize: 16, fontWeight: '700' },
  modalCancel: { paddingVertical: 14, alignItems: 'center', marginTop: 4 },
  modalCancelText: { color: '#FF453A', fontSize: 16 },
});
