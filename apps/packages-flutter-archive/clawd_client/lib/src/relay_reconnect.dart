import 'dart:async';
import 'dart:developer' as dev;
import 'dart:math';

/// Relay connection state seen by the UI.
enum RelayConnectionState {
  /// No relay involved (LAN-only or disconnected).
  idle,

  /// Connecting or re-connecting to the relay server.
  connecting,

  /// Relay tunnel active and healthy.
  connected,

  /// Relay tunnel lost; backing off before next attempt.
  reconnecting,

  /// All retry attempts exhausted — relay is unavailable.
  failed,
}

/// Exponential backoff reconnect timer for the relay connection.
///
/// The relay server broadcasts `relay:shutdown` before restarting, which
/// clients must honour by delaying their reconnect attempt by at least
/// [reconnectAfterMs] milliseconds.
///
/// Backoff schedule (jitter ±10%):
///   attempt 0 → 1s, 1 → 2s, 2 → 4s, 3 → 8s, 4 → 16s, …, ≥5 → 30s (cap)
///
/// After [maxAttempts] failures the state is set to [RelayConnectionState.failed]
/// and no further reconnects are scheduled automatically.
class RelayReconnectTimer {
  RelayReconnectTimer({
    required this.onReconnect,
    this.maxAttempts = 10,
    this.baseDelay = const Duration(seconds: 1),
    this.maxDelay = const Duration(seconds: 30),
  });

  /// Called when the backoff delay elapses and a reconnect attempt should start.
  final Future<void> Function() onReconnect;

  final int maxAttempts;
  final Duration baseDelay;
  final Duration maxDelay;

  int _attempt = 0;
  Timer? _timer;
  bool _disposed = false;

  RelayConnectionState _state = RelayConnectionState.idle;
  final StreamController<RelayConnectionState> _stateController =
      StreamController.broadcast();

  /// Current reconnect state.
  RelayConnectionState get state => _state;

  /// Stream of state changes.
  Stream<RelayConnectionState> get stateStream => _stateController.stream;

  void _setState(RelayConnectionState s) {
    _state = s;
    if (!_stateController.isClosed) _stateController.add(s);
  }

  /// Notify the timer that a relay connection was established successfully.
  /// Resets the attempt counter and sets state to [connected].
  void onConnected() {
    _attempt = 0;
    _timer?.cancel();
    _timer = null;
    _setState(RelayConnectionState.connected);
  }

  /// Notify the timer that the relay connection was lost.
  /// Schedules a reconnect if attempts remain.
  void onDisconnected() {
    if (_disposed) return;
    if (_attempt >= maxAttempts) {
      _setState(RelayConnectionState.failed);
      dev.log('Relay reconnect exhausted ($maxAttempts attempts)', name: 'relay_reconnect');
      return;
    }
    _scheduleReconnect(delay: _backoff(_attempt));
  }

  /// Called when the relay server sends `relay:shutdown`.
  ///
  /// Schedules a reconnect after [reconnectAfterMs] milliseconds (server hint),
  /// or [baseDelay] if the hint is missing or zero.
  void onShutdown({int reconnectAfterMs = 0}) {
    if (_disposed) return;
    _timer?.cancel();
    final hint = reconnectAfterMs > 0
        ? Duration(milliseconds: reconnectAfterMs)
        : baseDelay;
    _scheduleReconnect(delay: hint);
  }

  void _scheduleReconnect({required Duration delay}) {
    _timer?.cancel();
    _setState(RelayConnectionState.reconnecting);
    dev.log(
      'Relay reconnect in ${delay.inMilliseconds}ms (attempt ${_attempt + 1}/$maxAttempts)',
      name: 'relay_reconnect',
    );
    _timer = Timer(delay, () async {
      if (_disposed) return;
      _attempt++;
      _setState(RelayConnectionState.connecting);
      try {
        await onReconnect();
      } catch (e) {
        dev.log('Relay reconnect attempt failed: $e', name: 'relay_reconnect');
        onDisconnected();
      }
    });
  }

  Duration _backoff(int attempt) {
    final ms = baseDelay.inMilliseconds * pow(2, attempt).toInt();
    final jitter = Random().nextInt((ms * 0.1).ceil() + 1);
    return Duration(milliseconds: min(ms + jitter, maxDelay.inMilliseconds));
  }

  /// Reset — clears the attempt counter and cancels any pending timer.
  void reset() {
    _timer?.cancel();
    _timer = null;
    _attempt = 0;
    _setState(RelayConnectionState.idle);
  }

  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _timer = null;
    _stateController.close();
  }
}
