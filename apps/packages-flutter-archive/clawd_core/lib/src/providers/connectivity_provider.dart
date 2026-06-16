// SPDX-License-Identifier: MIT
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'daemon_provider.dart';

/// A LAN peer discovered via mDNS by the daemon.
class LanPeer {
  const LanPeer({
    required this.name,
    required this.address,
    required this.port,
    required this.version,
    required this.daemonId,
    required this.lastSeen,
  });

  factory LanPeer.fromJson(Map<String, dynamic> json) => LanPeer(
        name: json['name'] as String? ?? '',
        address: json['address'] as String? ?? '',
        port: (json['port'] as num?)?.toInt() ?? 4300,
        version: json['version'] as String? ?? 'unknown',
        daemonId: json['daemon_id'] as String? ?? '',
        lastSeen: (json['last_seen'] as num?)?.toInt() ?? 0,
      );

  final String name;
  final String address;
  final int port;
  final String version;
  final String daemonId;
  final int lastSeen;

  String get wsUrl => 'ws://$address:$port';
}

/// Connection quality state from the `connectivity.status` RPC.
class ConnectivityState {
  const ConnectivityState({
    this.mode = 'relay',
    this.rttMs = 0,
    this.packetLossPct = 0.0,
    this.degraded = false,
    this.lastPingAt = 0,
    this.preferDirect = false,
    this.vpnHost,
    this.airGap = false,
    this.lanPeers = const [],
    this.error,
  });

  factory ConnectivityState.fromJson(Map<String, dynamic> json) =>
      ConnectivityState(
        mode: json['mode'] as String? ?? 'relay',
        rttMs: (json['rtt_ms'] as num?)?.toInt() ?? 0,
        packetLossPct: (json['packet_loss_pct'] as num?)?.toDouble() ?? 0.0,
        degraded: json['degraded'] as bool? ?? false,
        lastPingAt: (json['last_ping_at'] as num?)?.toInt() ?? 0,
        preferDirect: json['prefer_direct'] as bool? ?? false,
        vpnHost: json['vpn_host'] as String?,
        airGap: json['air_gap'] as bool? ?? false,
        lanPeers: (json['lan_peers'] as List<dynamic>?)
                ?.map((e) => LanPeer.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
      );

  /// `"relay"` | `"direct"` | `"vpn"` | `"offline"`
  final String mode;
  final int rttMs;
  final double packetLossPct;
  final bool degraded;
  final int lastPingAt;
  final bool preferDirect;
  final String? vpnHost;
  final bool airGap;
  final List<LanPeer> lanPeers;
  final String? error;

  bool get hasData => rttMs > 0 || lanPeers.isNotEmpty;

  ConnectivityState copyWith({
    String? mode,
    int? rttMs,
    double? packetLossPct,
    bool? degraded,
    int? lastPingAt,
    bool? preferDirect,
    String? vpnHost,
    bool? airGap,
    List<LanPeer>? lanPeers,
    String? error,
  }) =>
      ConnectivityState(
        mode: mode ?? this.mode,
        rttMs: rttMs ?? this.rttMs,
        packetLossPct: packetLossPct ?? this.packetLossPct,
        degraded: degraded ?? this.degraded,
        lastPingAt: lastPingAt ?? this.lastPingAt,
        preferDirect: preferDirect ?? this.preferDirect,
        vpnHost: vpnHost ?? this.vpnHost,
        airGap: airGap ?? this.airGap,
        lanPeers: lanPeers ?? this.lanPeers,
        error: error,
      );
}

/// Polls `connectivity.status` on demand.
///
/// Silently returns default state when the daemon is not connected.
class ConnectivityNotifier extends AsyncNotifier<ConnectivityState> {
  @override
  Future<ConnectivityState> build() async {
    return _fetch();
  }

  Future<ConnectivityState> _fetch() async {
    final daemon = ref.read(daemonProvider);
    if (!daemon.isConnected) {
      return const ConnectivityState();
    }
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final result =
          await client.call<Map<String, dynamic>>('connectivity.status');
      return ConnectivityState.fromJson(result);
    } catch (e) {
      return ConnectivityState(error: e.toString());
    }
  }

  /// Manually refresh the connectivity status.
  Future<void> refresh() async {
    state = const AsyncLoading();
    state = AsyncData(await _fetch());
  }
}

final connectivityProvider =
    AsyncNotifierProvider<ConnectivityNotifier, ConnectivityState>(
  ConnectivityNotifier.new,
);

/// Convenience: RTT in ms (0 when unavailable).
final connectionRttProvider = Provider<int>((ref) {
  return ref.watch(connectivityProvider).valueOrNull?.rttMs ?? 0;
});

/// Convenience: true when connection quality is degraded.
final connectionDegradedProvider = Provider<bool>((ref) {
  return ref.watch(connectivityProvider).valueOrNull?.degraded ?? false;
});

/// Convenience: list of visible LAN peers.
final lanPeersProvider = Provider<List<LanPeer>>((ref) {
  return ref.watch(connectivityProvider).valueOrNull?.lanPeers ?? [];
});
