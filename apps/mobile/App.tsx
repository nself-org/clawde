/**
 * Purpose: Root entry — React Navigation shell + QueryClient + push event bridge.
 *          Initialises Sentry error reporting.
 * Inputs:  DaemonPushEvent stream; connection store; React Query cache.
 * Outputs: Bottom-tab navigator with badge overlays and update banner.
 * Constraints: Must handle cold start (no active host) gracefully.
 * SPORT: T-E1-06 — React Native Expo migration
 */

import React, { useEffect, useRef, useState } from 'react';
import { View, Text, StyleSheet, TouchableOpacity, Platform, StatusBar } from 'react-native';
import * as SentryRN from '@sentry/react-native';
import { NavigationContainer } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query';
import { GestureHandlerRootView } from 'react-native-gesture-handler';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { initObservability } from '@nself/observability';

import { daemonClient } from '@/lib/daemon';
import { useConnectionStore } from '@/lib/store';
import type { DaemonPushEvent } from '@/types/api';

import { SessionsScreen } from '@/screens/SessionsScreen';
import { SessionDetailScreen } from '@/screens/SessionDetailScreen';
import { HostsScreen } from '@/screens/HostsScreen';
import { TasksScreen } from '@/screens/TasksScreen';
import { SettingsScreen } from '@/screens/SettingsScreen';

// ── Navigator types ──────────────────────────────────────────────────────────

export type RootStackParamList = {
  Main: undefined;
  SessionDetail: { sessionId: string };
};

export type BottomTabParamList = {
  Sessions: undefined;
  Tasks: undefined;
  Hosts: undefined;
  Settings: undefined;
};

// ── Sentry init (module level — before first render) ─────────────────────────
if (process.env.EXPO_PUBLIC_SENTRY_DSN) {
  initObservability({
    sentry: {
      sdk: SentryRN as any, // React Native SDK has different signature; type coercion needed
      dsn: process.env.EXPO_PUBLIC_SENTRY_DSN,
      environment: process.env.APP_ENV ?? 'development',
      appKind: 'native' as const,
      release: process.env.EXPO_PUBLIC_APP_VERSION ?? '0.3.4',
      tracesSampleRate: process.env.APP_ENV === 'production' ? 0.2 : 1.0,
    },
  });
}

const Stack = createNativeStackNavigator<RootStackParamList>();
const Tab = createBottomTabNavigator<BottomTabParamList>();

// ── React Query client ───────────────────────────────────────────────────────

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 10_000,
    },
  },
});

// ── Tab bar icon (text-based, no icon library dep) ──────────────────────────

function TabIcon({
  label,
  focused,
  badge,
}: {
  label: string;
  focused: boolean;
  badge?: number;
}) {
  return (
    <View style={styles.iconWrap}>
      <Text style={[styles.iconText, focused && styles.iconFocused]}>{label}</Text>
      {badge != null && badge > 0 && (
        <View style={styles.badge}>
          <Text style={styles.badgeText}>{badge > 99 ? '99+' : badge}</Text>
        </View>
      )}
    </View>
  );
}

// ── Update banner ────────────────────────────────────────────────────────────

function UpdateBanner({ onDismiss }: { onDismiss: () => void }) {
  return (
    <View style={styles.banner}>
      <Text style={styles.bannerText}>Daemon update available. Restart to apply.</Text>
      <TouchableOpacity onPress={onDismiss} hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}>
        <Text style={styles.bannerDismiss}>✕</Text>
      </TouchableOpacity>
    </View>
  );
}

// ── Main tabs ─────────────────────────────────────────────────────────────────

function MainTabs() {
  const [pendingToolCalls, setPendingToolCalls] = useState(0);

  const qc = useQueryClient();

  useEffect(() => {
    const unsub = daemonClient.addPushListener((event: DaemonPushEvent) => {
      switch (event.method) {
        case 'session.statusChanged':
        case 'session.created':
        case 'session.closed':
          qc.invalidateQueries({ queryKey: ['sessions'] });
          break;
        case 'toolCall.created':
          setPendingToolCalls((n) => n + 1);
          qc.invalidateQueries({ queryKey: ['toolCalls'] });
          break;
        case 'toolCall.resolved':
          setPendingToolCalls((n) => Math.max(0, n - 1));
          qc.invalidateQueries({ queryKey: ['toolCalls'] });
          break;
        case 'message.created':
          qc.invalidateQueries({ queryKey: ['messages'] });
          break;
        case 'task.updated':
          qc.invalidateQueries({ queryKey: ['tasks'] });
          break;
        default:
          break;
      }
    });
    return unsub;
  }, [qc]);

  return (
    <Tab.Navigator
      screenOptions={{
        headerShown: false,
        tabBarStyle: styles.tabBar,
        tabBarActiveTintColor: '#007AFF',
        tabBarInactiveTintColor: '#8E8E93',
      }}
    >
      <Tab.Screen
        name="Sessions"
        component={SessionsScreen}
        options={{
          tabBarIcon: ({ focused }) => (
            <TabIcon label="⊡" focused={focused} badge={pendingToolCalls} />
          ),
          tabBarLabel: 'Sessions',
        }}
      />
      <Tab.Screen
        name="Tasks"
        component={TasksScreen}
        options={{
          tabBarIcon: ({ focused }) => <TabIcon label="◫" focused={focused} />,
          tabBarLabel: 'Tasks',
        }}
      />
      <Tab.Screen
        name="Hosts"
        component={HostsScreen}
        options={{
          tabBarIcon: ({ focused }) => <TabIcon label="⊕" focused={focused} />,
          tabBarLabel: 'Hosts',
        }}
      />
      <Tab.Screen
        name="Settings"
        component={SettingsScreen}
        options={{
          tabBarIcon: ({ focused }) => <TabIcon label="⚙" focused={focused} />,
          tabBarLabel: 'Settings',
        }}
      />
    </Tab.Navigator>
  );
}

// ── Root app ─────────────────────────────────────────────────────────────────

function AppShell() {
  const [updateAvailable, setUpdateAvailable] = useState(false);

  // Load persisted hosts and start connecting
  useEffect(() => {
    useConnectionStore.getState().loadHosts();
    daemonClient.connect();
    return () => daemonClient.disconnect();
  }, []);

  // Listen for daemon push events that affect app-level UI
  useEffect(() => {
    const unsub = daemonClient.addPushListener((event: DaemonPushEvent) => {
      if (event.method === 'daemon.updateAvailable') {
        setUpdateAvailable(true);
      }
    });
    return unsub;
  }, []);

  return (
    <View style={styles.root}>
      <StatusBar barStyle="light-content" backgroundColor="#000" />
      {updateAvailable && <UpdateBanner onDismiss={() => setUpdateAvailable(false)} />}
      <NavigationContainer>
        <Stack.Navigator screenOptions={{ headerShown: false }}>
          <Stack.Screen name="Main" component={MainTabs} />
          <Stack.Screen
            name="SessionDetail"
            component={SessionDetailScreen}
            options={{ headerShown: true, headerTitle: 'Session', headerBackTitle: '' }}
          />
        </Stack.Navigator>
      </NavigationContainer>
    </View>
  );
}

function AppWrapper() {
  return (
    <GestureHandlerRootView style={styles.root}>
      <SafeAreaProvider>
        <QueryClientProvider client={queryClient}>
          <AppShell />
        </QueryClientProvider>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}

export default SentryRN.wrap(AppWrapper);

// ── Styles ────────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  root: { flex: 1 },
  tabBar: {
    backgroundColor: '#1C1C1E',
    borderTopColor: '#3A3A3C',
  },
  iconWrap: { alignItems: 'center', justifyContent: 'center' },
  iconText: { fontSize: 20, color: '#8E8E93' },
  iconFocused: { color: '#007AFF' },
  badge: {
    position: 'absolute',
    top: -4,
    right: -8,
    backgroundColor: '#FF3B30',
    borderRadius: 8,
    minWidth: 16,
    paddingHorizontal: 3,
    alignItems: 'center',
    justifyContent: 'center',
  },
  badgeText: { color: '#fff', fontSize: 10, fontWeight: '700' },
  banner: {
    backgroundColor: '#FF9500',
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingVertical: 8,
    paddingTop: Platform.OS === 'ios' ? 44 + 8 : 8,
  },
  bannerText: { color: '#fff', fontSize: 13, flex: 1 },
  bannerDismiss: { color: '#fff', fontSize: 16, fontWeight: '700', marginLeft: 12 },
});
