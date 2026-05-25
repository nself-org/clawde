/**
 * Purpose: Daemon host management — saved hosts list, add/delete, and connect.
 * Inputs:  useConnectionStore (hosts, activeHostId, switchHost, addHost, removeHost).
 * Outputs: Host list with active indicator; add-host bottom sheet; swipe-to-delete.
 * Constraints: mDNS discovery stub (requires native Zeroconf module — shows "discovering…").
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
  Alert,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { Swipeable } from 'react-native-gesture-handler';
import { useConnectionStore } from '@/lib/store';
import { ConnectionBadge } from '@/components/ConnectionBadge';
import type { DaemonHost } from '@/types/api';

// ── Host row ───────────────────────────────────────────────────────────────────

function HostRow({
  host,
  isActive,
  onConnect,
  onDelete,
}: {
  host: DaemonHost;
  isActive: boolean;
  onConnect: () => void;
  onDelete: () => void;
}) {
  const renderRight = () => (
    <TouchableOpacity style={styles.swipeDelete} onPress={onDelete}>
      <Text style={styles.swipeText}>Delete</Text>
    </TouchableOpacity>
  );

  return (
    <Swipeable renderRightActions={renderRight}>
      <TouchableOpacity style={styles.row} onPress={onConnect} activeOpacity={0.7}>
        <View style={styles.rowLeft}>
          {isActive && <View style={styles.activeDot} />}
          <View>
            <Text style={styles.rowName}>{host.name}</Text>
            <Text style={styles.rowUrl}>{host.url}</Text>
          </View>
        </View>
        {isActive && <Text style={styles.connectedLabel}>Connected</Text>}
      </TouchableOpacity>
    </Swipeable>
  );
}

// ── Add host modal ─────────────────────────────────────────────────────────────

function AddHostModal({ visible, onClose }: { visible: boolean; onClose: () => void }) {
  const [name, setName] = useState('');
  const [url, setUrl] = useState('ws://');
  const addHost = useConnectionStore((s) => s.addHost);

  const submit = useCallback(async () => {
    if (!name.trim() || !url.trim()) return;
    const host: DaemonHost = {
      id: `host-${Date.now()}`,
      name: name.trim(),
      url: url.trim(),
      isPaired: false,
    };
    await addHost(host);
    setName('');
    setUrl('ws://');
    onClose();
  }, [name, url, addHost, onClose]);

  return (
    <Modal visible={visible} transparent animationType="slide" onRequestClose={onClose}>
      <View style={styles.modalOverlay}>
        <View style={styles.modalSheet}>
          <Text style={styles.modalTitle}>Add Daemon Host</Text>
          <TextInput
            style={styles.input}
            placeholder="Name (e.g. MacBook Pro)"
            placeholderTextColor="#636366"
            value={name}
            onChangeText={setName}
          />
          <TextInput
            style={styles.input}
            placeholder="URL (e.g. ws://192.168.1.10:4300)"
            placeholderTextColor="#636366"
            value={url}
            onChangeText={setUrl}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
          />
          <TouchableOpacity
            style={[styles.modalBtn, (!name.trim() || !url.trim()) && styles.modalBtnDisabled]}
            onPress={submit}
            disabled={!name.trim() || !url.trim()}
          >
            <Text style={styles.modalBtnText}>Add Host</Text>
          </TouchableOpacity>
          <TouchableOpacity style={styles.modalCancel} onPress={onClose}>
            <Text style={styles.modalCancelText}>Cancel</Text>
          </TouchableOpacity>
        </View>
      </View>
    </Modal>
  );
}

// ── Main screen ────────────────────────────────────────────────────────────────

export function HostsScreen() {
  const [showModal, setShowModal] = useState(false);
  const hosts = useConnectionStore((s) => s.hosts);
  const activeHostId = useConnectionStore((s) => s.activeHostId);
  const switchHost = useConnectionStore((s) => s.switchHost);
  const removeHost = useConnectionStore((s) => s.removeHost);

  const handleConnect = useCallback(
    (host: DaemonHost) => {
      switchHost(host);
    },
    [switchHost],
  );

  const handleDelete = useCallback(
    (id: string) => {
      Alert.alert('Remove host?', undefined, [
        { text: 'Cancel', style: 'cancel' },
        { text: 'Remove', style: 'destructive', onPress: () => removeHost(id) },
      ]);
    },
    [removeHost],
  );

  return (
    <SafeAreaView style={styles.container} edges={['top']}>
      {/* Header */}
      <View style={styles.header}>
        <Text style={styles.headerTitle}>Hosts</Text>
        <View style={styles.headerRight}>
          <ConnectionBadge />
          <TouchableOpacity onPress={() => setShowModal(true)}>
            <Text style={styles.headerAdd}>＋</Text>
          </TouchableOpacity>
        </View>
      </View>

      {/* LAN discovery hint */}
      <View style={styles.discoveryHint}>
        <Text style={styles.discoveryText}>
          mDNS discovery — ensure the daemon is running with{' '}
          <Text style={styles.mono}>--mdns</Text> on the same network.
        </Text>
      </View>

      {/* Saved hosts */}
      <FlatList
        data={hosts}
        keyExtractor={(h) => h.id}
        renderItem={({ item }) => (
          <HostRow
            host={item}
            isActive={item.id === activeHostId}
            onConnect={() => handleConnect(item)}
            onDelete={() => handleDelete(item.id)}
          />
        )}
        ItemSeparatorComponent={() => <View style={styles.separator} />}
        ListEmptyComponent={
          <View style={styles.empty}>
            <Text style={styles.emptyText}>No saved hosts</Text>
            <Text style={styles.emptyHint}>Tap + to add one</Text>
          </View>
        }
      />

      <AddHostModal visible={showModal} onClose={() => setShowModal(false)} />
    </SafeAreaView>
  );
}

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
  headerRight: { flexDirection: 'row', alignItems: 'center', gap: 12 },
  headerAdd: { color: '#007AFF', fontSize: 28 },
  discoveryHint: {
    backgroundColor: '#1C1C1E',
    marginHorizontal: 16,
    borderRadius: 10,
    padding: 12,
    marginBottom: 12,
  },
  discoveryText: { color: '#8E8E93', fontSize: 13, lineHeight: 18 },
  mono: { fontFamily: 'monospace', color: '#30D158' },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    backgroundColor: '#1C1C1E',
    paddingHorizontal: 16,
    paddingVertical: 14,
  },
  rowLeft: { flexDirection: 'row', alignItems: 'center', gap: 10 },
  activeDot: { width: 10, height: 10, borderRadius: 5, backgroundColor: '#30D158' },
  rowName: { color: '#fff', fontSize: 16, fontWeight: '600' },
  rowUrl: { color: '#636366', fontSize: 12, marginTop: 2 },
  connectedLabel: { color: '#30D158', fontSize: 12, fontWeight: '600' },
  separator: { height: 1, backgroundColor: '#2C2C2E' },
  swipeDelete: {
    backgroundColor: '#FF453A',
    justifyContent: 'center',
    alignItems: 'center',
    width: 80,
  },
  swipeText: { color: '#fff', fontWeight: '700', fontSize: 13 },
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
    marginBottom: 12,
  },
  modalBtn: {
    backgroundColor: '#007AFF',
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 4,
  },
  modalBtnDisabled: { opacity: 0.4 },
  modalBtnText: { color: '#fff', fontSize: 16, fontWeight: '700' },
  modalCancel: { paddingVertical: 14, alignItems: 'center', marginTop: 4 },
  modalCancelText: { color: '#FF453A', fontSize: 16 },
});
