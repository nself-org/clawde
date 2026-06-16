import 'package:flutter_riverpod/flutter_riverpod.dart';

/// The transport mode of the current daemon connection.
enum ConnectionMode {
  /// Desktop connected to the local daemon on the same machine (localhost).
  local,

  /// Connected to a daemon on a different machine over the local network (LAN).
  lan,

  /// Connected to a daemon via the relay server over the internet.
  relay,

  /// Actively attempting to reconnect after a disconnect.
  reconnecting,

  /// No connection to any daemon.
  offline;

  /// Whether this mode represents an active, usable connection.
  bool get isConnected =>
      this == ConnectionMode.local ||
      this == ConnectionMode.lan ||
      this == ConnectionMode.relay;

  /// Human-readable label for display in connection status indicators.
  String get displayLabel => switch (this) {
        ConnectionMode.local => 'Local',
        ConnectionMode.lan => 'LAN',
        ConnectionMode.relay => 'Relay',
        ConnectionMode.reconnecting => 'Reconnecting...',
        ConnectionMode.offline => 'Offline',
      };

  /// Color hint token for the UI layer to map to a platform Color.
  ///
  /// clawd_core avoids hard-coding Color values — the UI layer (desktop/mobile)
  /// maps these tokens to theme-appropriate colors.
  ///   'green'  → connected locally
  ///   'blue'   → connected via LAN
  ///   'amber'  → connected via relay (internet)
  ///   'orange' → reconnecting
  ///   'red'    → offline / error
  String get colorHint => switch (this) {
        ConnectionMode.local => 'green',
        ConnectionMode.lan => 'blue',
        ConnectionMode.relay => 'amber',
        ConnectionMode.reconnecting => 'orange',
        ConnectionMode.offline => 'red',
      };
}

/// Tracks the active [ConnectionMode] for the daemon connection.
///
/// Updated by the daemon connection layer (e.g. DaemonNotifier or the relay
/// client) as the transport mode changes. The UI reads this to show the
/// appropriate connection status badge.
class ConnectionModeNotifier extends Notifier<ConnectionMode> {
  @override
  ConnectionMode build() => ConnectionMode.offline;

  /// Set the current connection mode.
  void update(ConnectionMode mode) => state = mode;

  /// Convenience: mark as reconnecting (preserves caller intent).
  void markReconnecting() => state = ConnectionMode.reconnecting;

  /// Convenience: mark as offline.
  void markOffline() => state = ConnectionMode.offline;
}

final connectionModeProvider =
    NotifierProvider<ConnectionModeNotifier, ConnectionMode>(
  ConnectionModeNotifier.new,
);
