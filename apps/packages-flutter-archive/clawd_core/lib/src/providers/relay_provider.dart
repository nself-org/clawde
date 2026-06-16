import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_client/clawd_client.dart';
import 'daemon_provider.dart';
import 'connection_state_provider.dart';

/// Tracks the relay reconnection state exposed to the UI.
///
/// Listens to daemon push events for `relay:shutdown` and updates the
/// [RelayConnectionState] accordingly. Also mirrors the daemon's connection
/// mode to distinguish relay from LAN connections.
class RelayNotifier extends Notifier<RelayConnectionState> {
  late RelayReconnectTimer _timer;

  @override
  RelayConnectionState build() {
    _timer = RelayReconnectTimer(
      onReconnect: () async {
        final notifier = ref.read(daemonProvider.notifier);
        await notifier.reconnect();
      },
    );

    ref.onDispose(_timer.dispose);

    // Mirror daemon connection mode into relay state.
    ref.listen(daemonProvider, (prev, next) {
      final mode = ref.read(connectionModeProvider);
      if (next.isConnected && mode == ConnectionMode.relay) {
        _timer.onConnected();
      } else if (!next.isConnected &&
          (prev?.isConnected ?? false) &&
          mode == ConnectionMode.relay) {
        _timer.onDisconnected();
      }
    });

    // Handle relay:shutdown push events from the server.
    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        if (event['type'] == 'relay:shutdown') {
          final ms = event['reconnect_after_ms'] as int? ?? 0;
          _timer.onShutdown(reconnectAfterMs: ms);
        }
      });
    });

    return RelayConnectionState.idle;
  }
}

final relayStateProvider =
    NotifierProvider<RelayNotifier, RelayConnectionState>(RelayNotifier.new);
