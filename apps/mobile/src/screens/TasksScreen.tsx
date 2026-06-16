/**
 * Purpose: Agent task dashboard — tabbed Board / Activity / Agents views.
 * Inputs:  useTasks, useDashboardStats, useActivityFeed, useAgents hooks; activeHostId.
 * Outputs: Top-tab navigator; task detail bottom-sheet with mark-done/blocked actions.
 * Constraints: repoPath sourced from active host's first session or manual input.
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
  ActivityIndicator,
  RefreshControl,
  TextInput,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import {
  useTasks,
  useDashboardStats,
  useActivityFeed,
  useAgents,
  useUpdateTaskStatus,
} from '@/hooks/useClawDEApi';
import type { AgentTask, ActivityEntry, Agent } from '@/types/api';

// ── Tab bar ────────────────────────────────────────────────────────────────────

type Tab = 'board' | 'activity' | 'agents';

const TABS: { label: string; value: Tab }[] = [
  { label: 'Board', value: 'board' },
  { label: 'Activity', value: 'activity' },
  { label: 'Agents', value: 'agents' },
];

// ── Task detail sheet ──────────────────────────────────────────────────────────

function TaskDetailSheet({
  task,
  onClose,
}: {
  task: AgentTask | null;
  onClose: () => void;
}) {
  const update = useUpdateTaskStatus();

  const markDone = useCallback(() => {
    if (!task) return;
    update.mutate({ taskId: task.id, status: 'done' }, { onSuccess: onClose });
  }, [task, update, onClose]);

  const markBlocked = useCallback(() => {
    if (!task) return;
    update.mutate({ taskId: task.id, status: 'blocked' }, { onSuccess: onClose });
  }, [task, update, onClose]);

  return (
    <Modal
      visible={task != null}
      transparent
      animationType="slide"
      onRequestClose={onClose}
    >
      <View style={styles.sheetOverlay}>
        <View style={styles.sheet}>
          {task && (
            <>
              <Text style={styles.sheetTitle}>{task.title}</Text>
              {task.description && (
                <Text style={styles.sheetDesc}>{task.description}</Text>
              )}
              <View style={styles.sheetMeta}>
                <View style={styles.statusPill}>
                  <Text style={styles.statusPillText}>{task.status}</Text>
                </View>
              </View>
              {task.notes && (
                <View style={styles.notesBox}>
                  <Text style={styles.notesText}>{task.notes}</Text>
                </View>
              )}
              <View style={styles.sheetActions}>
                <TouchableOpacity
                  style={[styles.sheetBtn, styles.sheetBtnDone]}
                  onPress={markDone}
                  disabled={update.isPending}
                >
                  <Text style={styles.sheetBtnText}>Mark Done</Text>
                </TouchableOpacity>
                <TouchableOpacity
                  style={[styles.sheetBtn, styles.sheetBtnBlocked]}
                  onPress={markBlocked}
                  disabled={update.isPending}
                >
                  <Text style={styles.sheetBtnText}>Mark Blocked</Text>
                </TouchableOpacity>
              </View>
              <TouchableOpacity style={styles.sheetCancel} onPress={onClose}>
                <Text style={styles.sheetCancelText}>Close</Text>
              </TouchableOpacity>
            </>
          )}
        </View>
      </View>
    </Modal>
  );
}

// ── Board tab ──────────────────────────────────────────────────────────────────

function BoardTab({ repoPath }: { repoPath: string }) {
  const { data: tasks = [], isLoading, refetch } = useTasks(repoPath);
  const { data: stats } = useDashboardStats(repoPath);
  const [selected, setSelected] = useState<AgentTask | null>(null);

  const renderTask = ({ item }: { item: AgentTask }) => (
    <TouchableOpacity style={styles.taskCard} onPress={() => setSelected(item)} activeOpacity={0.7}>
      <Text style={styles.taskTitle} numberOfLines={2}>
        {item.title}
      </Text>
      <View style={styles.taskMeta}>
        <Text style={styles.taskStatus}>{item.status}</Text>
      </View>
    </TouchableOpacity>
  );

  return (
    <>
      {stats && (
        <View style={styles.statsRow}>
          {Object.entries(stats.byStatus).map(([k, v]) => (
            <View key={k} style={styles.statCell}>
              <Text style={styles.statValue}>{v as number}</Text>
              <Text style={styles.statLabel}>{k}</Text>
            </View>
          ))}
        </View>
      )}
      <FlatList
        data={tasks}
        keyExtractor={(t) => t.id}
        renderItem={renderTask}
        numColumns={2}
        columnWrapperStyle={styles.taskGrid}
        contentContainerStyle={styles.taskGridContent}
        refreshControl={
          <RefreshControl refreshing={isLoading} onRefresh={refetch} tintColor="#007AFF" />
        }
        ListEmptyComponent={
          isLoading ? (
            <ActivityIndicator color="#007AFF" style={{ marginTop: 40 }} />
          ) : (
            <View style={styles.empty}>
              <Text style={styles.emptyText}>No tasks</Text>
            </View>
          )
        }
      />
      <TaskDetailSheet task={selected} onClose={() => setSelected(null)} />
    </>
  );
}

// ── Activity tab ───────────────────────────────────────────────────────────────

function ActivityTab({ repoPath }: { repoPath: string }) {
  const { data: entries = [], isLoading, refetch } = useActivityFeed(repoPath);

  return (
    <FlatList
      data={entries}
      keyExtractor={(_, i) => String(i)}
      renderItem={({ item }: { item: ActivityEntry }) => (
        <View style={styles.actRow}>
          <Text style={styles.actType}>{item.type}</Text>
          <Text style={styles.actDesc} numberOfLines={2}>
            {item.description}
          </Text>
          <Text style={styles.actTime}>{new Date(item.timestamp).toLocaleTimeString()}</Text>
        </View>
      )}
      ItemSeparatorComponent={() => <View style={styles.separator} />}
      refreshControl={
        <RefreshControl refreshing={isLoading} onRefresh={refetch} tintColor="#007AFF" />
      }
      ListEmptyComponent={
        isLoading ? (
          <ActivityIndicator color="#007AFF" style={{ marginTop: 40 }} />
        ) : (
          <View style={styles.empty}>
            <Text style={styles.emptyText}>No activity</Text>
          </View>
        )
      }
    />
  );
}

// ── Agents tab ─────────────────────────────────────────────────────────────────

function AgentsTab({ repoPath }: { repoPath: string }) {
  const { data: agents = [], isLoading, refetch } = useAgents(repoPath);

  return (
    <FlatList
      data={agents}
      keyExtractor={(a) => a.agentId}
      renderItem={({ item }: { item: Agent }) => (
        <View style={styles.agentRow}>
          <View style={styles.agentDot} />
          <View>
            <Text style={styles.agentType}>{item.agentType}</Text>
            <Text style={styles.agentId}>{item.agentId}</Text>
          </View>
          <Text style={[styles.agentStatus, item.status === 'active' && styles.agentActive]}>
            {item.status}
          </Text>
        </View>
      )}
      ItemSeparatorComponent={() => <View style={styles.separator} />}
      refreshControl={
        <RefreshControl refreshing={isLoading} onRefresh={refetch} tintColor="#007AFF" />
      }
      ListEmptyComponent={
        isLoading ? (
          <ActivityIndicator color="#007AFF" style={{ marginTop: 40 }} />
        ) : (
          <View style={styles.empty}>
            <Text style={styles.emptyText}>No agents running</Text>
          </View>
        )
      }
    />
  );
}

// ── Main screen ────────────────────────────────────────────────────────────────

export function TasksScreen() {
  const [tab, setTab] = useState<Tab>('board');
  const [repoPath, setRepoPath] = useState('');
  const [editingRepo, setEditingRepo] = useState(false);

  return (
    <SafeAreaView style={styles.container} edges={['top']}>
      {/* Header */}
      <View style={styles.header}>
        <Text style={styles.headerTitle}>Tasks</Text>
      </View>

      {/* Repo path input */}
      <TouchableOpacity onPress={() => setEditingRepo(true)} style={styles.repoRow}>
        <Text style={styles.repoLabel}>Repo:</Text>
        {editingRepo ? (
          <TextInput
            style={styles.repoInput}
            value={repoPath}
            onChangeText={setRepoPath}
            autoFocus
            onBlur={() => setEditingRepo(false)}
            onSubmitEditing={() => setEditingRepo(false)}
            autoCapitalize="none"
            autoCorrect={false}
            placeholder="/path/to/repo"
            placeholderTextColor="#636366"
          />
        ) : (
          <Text style={styles.repoPath} numberOfLines={1}>
            {repoPath || '/path/to/repo'}
          </Text>
        )}
      </TouchableOpacity>

      {/* Tab switcher */}
      <View style={styles.tabBar}>
        {TABS.map((t) => (
          <TouchableOpacity
            key={t.value}
            style={[styles.tabBtn, tab === t.value && styles.tabBtnActive]}
            onPress={() => setTab(t.value)}
          >
            <Text style={[styles.tabLabel, tab === t.value && styles.tabLabelActive]}>
              {t.label}
            </Text>
          </TouchableOpacity>
        ))}
      </View>

      {/* Tab content */}
      {tab === 'board' && <BoardTab repoPath={repoPath} />}
      {tab === 'activity' && <ActivityTab repoPath={repoPath} />}
      {tab === 'agents' && <AgentsTab repoPath={repoPath} />}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#000' },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 16,
    paddingVertical: 12,
  },
  headerTitle: { color: '#fff', fontSize: 22, fontWeight: '700' },
  repoRow: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: '#1C1C1E',
    marginHorizontal: 16,
    borderRadius: 10,
    padding: 10,
    marginBottom: 12,
    gap: 8,
  },
  repoLabel: { color: '#636366', fontSize: 13 },
  repoPath: { color: '#007AFF', fontSize: 13, fontFamily: 'monospace', flex: 1 },
  repoInput: {
    color: '#007AFF',
    fontSize: 13,
    fontFamily: 'monospace',
    flex: 1,
    padding: 0,
  },
  tabBar: {
    flexDirection: 'row',
    borderBottomWidth: 1,
    borderBottomColor: '#2C2C2E',
    marginBottom: 4,
  },
  tabBtn: { flex: 1, paddingVertical: 10, alignItems: 'center' },
  tabBtnActive: { borderBottomWidth: 2, borderBottomColor: '#007AFF' },
  tabLabel: { color: '#636366', fontSize: 14, fontWeight: '600' },
  tabLabelActive: { color: '#007AFF' },
  statsRow: {
    flexDirection: 'row',
    paddingHorizontal: 16,
    paddingVertical: 8,
    gap: 12,
  },
  statCell: { alignItems: 'center', flex: 1 },
  statValue: { color: '#fff', fontSize: 22, fontWeight: '700' },
  statLabel: { color: '#636366', fontSize: 11, textTransform: 'uppercase' },
  taskGrid: { gap: 10 },
  taskGridContent: { padding: 12, gap: 10 },
  taskCard: {
    flex: 1,
    backgroundColor: '#1C1C1E',
    borderRadius: 10,
    padding: 12,
    minHeight: 80,
  },
  taskTitle: { color: '#fff', fontSize: 14, fontWeight: '600', lineHeight: 20 },
  taskMeta: { flexDirection: 'row', marginTop: 8 },
  taskStatus: {
    color: '#636366',
    fontSize: 11,
    textTransform: 'uppercase',
    fontWeight: '600',
  },
  actRow: {
    backgroundColor: '#1C1C1E',
    paddingHorizontal: 16,
    paddingVertical: 10,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  actType: { color: '#007AFF', fontSize: 12, fontWeight: '700', width: 60 },
  actDesc: { color: '#EBEBF5', fontSize: 13, flex: 1 },
  actTime: { color: '#636366', fontSize: 11 },
  agentRow: {
    backgroundColor: '#1C1C1E',
    paddingHorizontal: 16,
    paddingVertical: 12,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
  },
  agentDot: { width: 8, height: 8, borderRadius: 4, backgroundColor: '#636366' },
  agentType: { color: '#fff', fontSize: 14, fontWeight: '600' },
  agentId: { color: '#636366', fontSize: 11, fontFamily: 'monospace' },
  agentStatus: { color: '#636366', fontSize: 12, marginLeft: 'auto' },
  agentActive: { color: '#30D158' },
  separator: { height: 1, backgroundColor: '#2C2C2E' },
  empty: { alignItems: 'center', paddingTop: 60 },
  emptyText: { color: '#636366', fontSize: 16 },
  sheetOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.6)',
    justifyContent: 'flex-end',
  },
  sheet: {
    backgroundColor: '#1C1C1E',
    borderTopLeftRadius: 16,
    borderTopRightRadius: 16,
    padding: 24,
    paddingBottom: 40,
  },
  sheetTitle: { color: '#fff', fontSize: 18, fontWeight: '700', marginBottom: 8 },
  sheetDesc: { color: '#8E8E93', fontSize: 14, lineHeight: 20, marginBottom: 12 },
  sheetMeta: { flexDirection: 'row', marginBottom: 12 },
  statusPill: {
    backgroundColor: '#2C2C2E',
    borderRadius: 6,
    paddingHorizontal: 10,
    paddingVertical: 4,
  },
  statusPillText: { color: '#8E8E93', fontSize: 12, fontWeight: '600' },
  notesBox: {
    backgroundColor: '#2C2C2E',
    borderRadius: 8,
    padding: 10,
    marginBottom: 16,
  },
  notesText: { color: '#EBEBF5', fontSize: 13, lineHeight: 18 },
  sheetActions: { flexDirection: 'row', gap: 10, marginBottom: 8 },
  sheetBtn: { flex: 1, borderRadius: 10, paddingVertical: 12, alignItems: 'center' },
  sheetBtnDone: { backgroundColor: '#30D158' },
  sheetBtnBlocked: { backgroundColor: '#FF9500' },
  sheetBtnText: { color: '#fff', fontSize: 15, fontWeight: '700' },
  sheetCancel: { paddingVertical: 12, alignItems: 'center' },
  sheetCancelText: { color: '#636366', fontSize: 15 },
});
