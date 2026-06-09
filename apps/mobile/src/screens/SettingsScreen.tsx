/**
 * Purpose: App settings — active host display, connection mode, QR scan pairing, About.
 * Inputs:  useConnectionStore (activeHostId, hosts, connectionMode); expo-camera for QR.
 * Outputs: Settings list groups; QR scanner modal for pairing a new host.
 * Constraints: QR scan parses { name, url } JSON from the daemon's pairing QR code.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React, { useState, useCallback, useEffect } from 'react';
import {
  View,
  Text,
  ScrollView,
  TouchableOpacity,
  StyleSheet,
  Modal,
  Alert,
  Linking,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { Camera, CameraView } from 'expo-camera';
import { useConnectionStore } from '@/lib/store';
import { ConnectionBadge } from '@/components/ConnectionBadge';
import type { DaemonHost } from '@/types/api';
import * as Application from 'expo-application';

// ── Section component ─────────────────────────────────────────────────────────

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>{title}</Text>
      <View style={styles.sectionBody}>{children}</View>
    </View>
  );
}

function RowItem({
  label,
  value,
  onPress,
  danger,
}: {
  label: string;
  value?: string;
  onPress?: () => void;
  danger?: boolean;
}) {
  return (
    <TouchableOpacity style={styles.row} onPress={onPress} disabled={!onPress} activeOpacity={0.7}>
      <Text style={[styles.rowLabel, danger && styles.rowLabelDanger]}>{label}</Text>
      {value != null && <Text style={styles.rowValue} numberOfLines={1}>{value}</Text>}
    </TouchableOpacity>
  );
}

// ── QR Scanner modal ──────────────────────────────────────────────────────────

function QRScannerModal({
  visible,
  onClose,
  onScanned,
}: {
  visible: boolean;
  onClose: () => void;
  onScanned: (data: string) => void;
}) {
  const [hasPermission, setHasPermission] = useState<boolean | null>(null);
  const [scanned, setScanned] = useState(false);

  useEffect(() => {
    if (visible) {
      Camera.requestCameraPermissionsAsync().then(({ status }) => {
        setHasPermission(status === 'granted');
      });
      setScanned(false);
    }
  }, [visible]);

  const handleBarcode = useCallback(
    ({ data }: { data: string }) => {
      if (scanned) return;
      setScanned(true);
      onScanned(data);
    },
    [scanned, onScanned],
  );

  return (
    <Modal visible={visible} transparent={false} animationType="slide" onRequestClose={onClose}>
      <View style={styles.scannerContainer}>
        {hasPermission === null && (
          <Text style={styles.scannerMsg}>Requesting camera permission…</Text>
        )}
        {hasPermission === false && (
          <Text style={styles.scannerMsg}>Camera access denied. Enable in Settings.</Text>
        )}
        {hasPermission && (
          <CameraView
            style={StyleSheet.absoluteFill}
            onBarcodeScanned={handleBarcode}
            barcodeScannerSettings={{ barcodeTypes: ['qr'] }}
          />
        )}
        <View style={styles.scannerOverlay}>
          <View style={styles.scannerBox} />
          <Text style={styles.scannerHint}>Scan the ClawDE daemon pairing QR code</Text>
          <TouchableOpacity style={styles.scannerClose} onPress={onClose}>
            <Text style={styles.scannerCloseText}>Cancel</Text>
          </TouchableOpacity>
        </View>
      </View>
    </Modal>
  );
}

// ── Main screen ────────────────────────────────────────────────────────────────

export function SettingsScreen() {
  const hosts = useConnectionStore((s) => s.hosts);
  const activeHostId = useConnectionStore((s) => s.activeHostId);
  const connectionMode = useConnectionStore((s) => s.connectionMode);
  const switchHost = useConnectionStore((s) => s.switchHost);
  const addHost = useConnectionStore((s) => s.addHost);

  const [showScanner, setShowScanner] = useState(false);

  const activeHost = hosts.find((h) => h.id === activeHostId) ?? null;

  const handleQRScanned = useCallback(
    async (data: string) => {
      setShowScanner(false);
      try {
        const parsed = JSON.parse(data) as { name?: string; url?: string };
        if (!parsed.url) throw new Error('No URL in QR data');
        const host: DaemonHost = {
          id: `host-${Date.now()}`,
          name: parsed.name ?? 'Paired Host',
          url: parsed.url,
          isPaired: true,
        };
        await addHost(host);
        await switchHost(host);
        Alert.alert('Paired', `Connected to ${host.name}`);
      } catch {
        Alert.alert('Pairing failed', 'Invalid QR code. Expected JSON with {name, url}.');
      }
    },
    [addHost, switchHost],
  );

  const appVersion =
    Application.nativeApplicationVersion ?? '0.3.2';

  return (
    <SafeAreaView style={styles.container} edges={['top']}>
      <ScrollView>
        {/* Header */}
        <View style={styles.header}>
          <Text style={styles.headerTitle}>Settings</Text>
          <ConnectionBadge />
        </View>

        {/* Connection */}
        <Section title="CONNECTION">
          <RowItem
            label="Active Host"
            value={activeHost ? activeHost.name : 'None'}
          />
          <RowItem
            label="Mode"
            value={connectionMode.charAt(0).toUpperCase() + connectionMode.slice(1)}
          />
          <RowItem
            label="Daemon URL"
            value={activeHost?.url ?? '—'}
          />
          <RowItem
            label="Pair via QR Code"
            onPress={() => setShowScanner(true)}
          />
        </Section>

        {/* About */}
        <Section title="ABOUT">
          <RowItem label="Version" value={appVersion} />
          <RowItem
            label="GitHub"
            value="github.com/nself-org/clawde"
            onPress={() => Linking.openURL('https://github.com/nself-org/clawde')}
          />
        </Section>
      </ScrollView>

      <QRScannerModal
        visible={showScanner}
        onClose={() => setShowScanner(false)}
        onScanned={handleQRScanned}
      />
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
  section: { marginBottom: 24 },
  sectionTitle: {
    color: '#636366',
    fontSize: 12,
    fontWeight: '600',
    textTransform: 'uppercase',
    letterSpacing: 1,
    marginLeft: 16,
    marginBottom: 6,
  },
  sectionBody: { backgroundColor: '#1C1C1E' },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: 1,
    borderBottomColor: '#2C2C2E',
  },
  rowLabel: { color: '#fff', fontSize: 15 },
  rowLabelDanger: { color: '#FF453A' },
  rowValue: { color: '#636366', fontSize: 14, maxWidth: '55%', textAlign: 'right' },
  scannerContainer: { flex: 1, backgroundColor: '#000' },
  scannerMsg: { color: '#fff', textAlign: 'center', marginTop: 100, fontSize: 16 },
  scannerOverlay: {
    ...StyleSheet.absoluteFillObject,
    alignItems: 'center',
    justifyContent: 'center',
  },
  scannerBox: {
    width: 240,
    height: 240,
    borderWidth: 2,
    borderColor: '#007AFF',
    borderRadius: 12,
    marginBottom: 24,
  },
  scannerHint: { color: '#fff', fontSize: 14, textAlign: 'center', marginBottom: 32 },
  scannerClose: {
    backgroundColor: 'rgba(0,0,0,0.6)',
    paddingHorizontal: 24,
    paddingVertical: 12,
    borderRadius: 20,
  },
  scannerCloseText: { color: '#fff', fontSize: 16, fontWeight: '600' },
});
