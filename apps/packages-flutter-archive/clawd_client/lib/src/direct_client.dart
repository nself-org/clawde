// SPDX-License-Identifier: MIT
import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';

/// A discovered `clawd` daemon on the local network.
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

  /// mDNS instance name, e.g. `clawd-abc12345._clawde._tcp.local.`
  final String name;

  /// Resolved IP address.
  final String address;

  /// Daemon WebSocket port.
  final int port;

  /// Daemon version string.
  final String version;

  /// Stable daemon ID (SHA-256 of machine hardware ID).
  final String daemonId;

  /// Unix timestamp (seconds) when this peer was last seen via mDNS.
  final int lastSeen;

  /// WebSocket URL for direct connection.
  String get wsUrl => 'ws://$address:$port';

  @override
  String toString() => 'LanPeer($name, $address:$port, v$version)';
}

/// Connectivity status returned by `connectivity.status` RPC.
class ConnectivityStatus {
  const ConnectivityStatus({
    required this.mode,
    required this.rttMs,
    required this.packetLossPct,
    required this.degraded,
    required this.lastPingAt,
    required this.preferDirect,
    this.vpnHost,
    required this.airGap,
    required this.lanPeers,
  });

  factory ConnectivityStatus.fromJson(Map<String, dynamic> json) =>
      ConnectivityStatus(
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

  /// Connection mode: `"relay"` | `"direct"` | `"vpn"` | `"offline"`.
  final String mode;

  /// Round-trip time to relay/peer in milliseconds.
  final int rttMs;

  /// Estimated packet loss percentage.
  final double packetLossPct;

  /// True when RTT > 500ms or packet loss > 5%.
  final bool degraded;

  /// Unix timestamp of last ping.
  final int lastPingAt;

  /// Config: prefer direct LAN connection before relay.
  final bool preferDirect;

  /// Config: VPN host, if set.
  final String? vpnHost;

  /// Config: air-gap mode (no outbound relay/API calls).
  final bool airGap;

  /// Currently visible LAN peers.
  final List<LanPeer> lanPeers;
}

/// Lightweight helper for direct-mode daemon connections.
///
/// Used by the [DirectModeNotifier] in `clawd_core` to connect to a
/// specific [LanPeer] without going through the relay.
///
/// Authentication: sends `daemon.auth` RPC immediately after connecting,
/// using the provided [authToken].
class DirectClient {
  DirectClient({
    required this.peer,
    required this.authToken,
    this.callTimeout = const Duration(seconds: 10),
  });

  final LanPeer peer;
  final String authToken;
  final Duration callTimeout;

  WebSocketChannel? _channel;
  int _nextId = 1;
  final _pending = <int, Completer<Map<String, dynamic>>>{};
  StreamSubscription<dynamic>? _sub;

  /// Connect to the peer and authenticate.
  Future<void> connect() async {
    _channel = WebSocketChannel.connect(Uri.parse(peer.wsUrl));
    _sub = _channel!.stream.listen(
      _onMessage,
      onError: _onError,
      onDone: _onDone,
    );
    // Authenticate
    await _call('daemon.auth', {'token': authToken});
  }

  /// Call `connectivity.status` on the direct peer.
  Future<ConnectivityStatus> connectivityStatus() async {
    final result = await _call('connectivity.status', {});
    return ConnectivityStatus.fromJson(result);
  }

  /// Close the connection.
  void close() {
    _sub?.cancel();
    _channel?.sink.close();
    _channel = null;
    for (final c in _pending.values) {
      c.completeError(StateError('DirectClient closed'));
    }
    _pending.clear();
  }

  Future<Map<String, dynamic>> _call(String method, Map<String, dynamic> params) async {
    final id = _nextId++;
    final req = jsonEncode({
      'jsonrpc': '2.0',
      'id': id,
      'method': method,
      'params': params,
    });
    final completer = Completer<Map<String, dynamic>>();
    _pending[id] = completer;
    _channel!.sink.add(req);
    return completer.future.timeout(callTimeout, onTimeout: () {
      _pending.remove(id);
      throw TimeoutException('DirectClient.$method timed out', callTimeout);
    });
  }

  void _onMessage(dynamic data) {
    final msg = jsonDecode(data as String) as Map<String, dynamic>;
    final id = msg['id'];
    if (id == null) return; // notification — ignore

    final completer = _pending.remove(id as int);
    if (completer == null) return;

    if (msg.containsKey('error')) {
      completer.completeError(
        Exception(msg['error']['message'] ?? 'RPC error'),
      );
    } else {
      completer.complete(msg['result'] as Map<String, dynamic>? ?? {});
    }
  }

  void _onError(Object error) {
    for (final c in _pending.values) {
      c.completeError(error);
    }
    _pending.clear();
  }

  void _onDone() {
    for (final c in _pending.values) {
      c.completeError(StateError('WebSocket closed unexpectedly'));
    }
    _pending.clear();
  }
}
