import 'dart:async';
import 'dart:developer' as dev;
import 'dart:io' show File;
import 'dart:math';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_client/clawd_client.dart';
import '../utils/paths.dart';
import 'settings_provider.dart';
import 'connection_state_provider.dart';

/// The connection state of the local clawd daemon.
enum DaemonStatus { disconnected, connecting, connected, error }

/// Runtime information returned by the `daemon.status` RPC.
class DaemonInfo {
  final String version;
  final int uptime; // seconds
  final int activeSessions;
  final int port;

  const DaemonInfo({
    required this.version,
    required this.uptime,
    required this.activeSessions,
    required this.port,
  });

  factory DaemonInfo.fromJson(Map<String, dynamic> json) => DaemonInfo(
        version: json['version'] as String,
        uptime: json['uptime'] as int,
        activeSessions: json['activeSessions'] as int,
        port: json['port'] as int,
      );
}

class DaemonState {
  final DaemonStatus status;
  final String? errorMessage;
  final DaemonInfo? daemonInfo;
  /// Number of reconnect attempts made since last disconnect. 0 = first attempt.
  final int reconnectAttempt;

  const DaemonState({
    this.status = DaemonStatus.disconnected,
    this.errorMessage,
    this.daemonInfo,
    this.reconnectAttempt = 0,
  });

  bool get isConnected => status == DaemonStatus.connected;

  DaemonState copyWith({
    DaemonStatus? status,
    String? errorMessage,
    DaemonInfo? daemonInfo,
    int? reconnectAttempt,
  }) =>
      DaemonState(
        status: status ?? this.status,
        errorMessage: errorMessage ?? this.errorMessage,
        daemonInfo: daemonInfo ?? this.daemonInfo,
        reconnectAttempt: reconnectAttempt ?? this.reconnectAttempt,
      );
}

/// Manages the singleton ClawdClient and its connection lifecycle.
/// Both desktop and mobile share this provider via ProviderScope.
class DaemonNotifier extends Notifier<DaemonState> {
  late ClawdClient _client;
  String? _authToken;
  int _reconnectAttempt = 0;
  Timer? _reconnectTimer;
  StreamSubscription<Map<String, dynamic>>? _pushSubscription;
  bool _disposed = false;

  static const int _maxReconnectAttempts = 8;
  static const Duration _baseDelay = Duration(seconds: 2);
  static const Duration _maxDelay = Duration(seconds: 60);

  @override
  DaemonState build() {
    // Read initial URL without subscribing — build() stays stable.
    final url = ref.read(settingsProvider).valueOrNull?.daemonUrl
        ?? 'ws://127.0.0.1:4300';
    // Bootstrap token (desktop, injected by DaemonManager) takes priority
    // over reading the file, avoiding a race between daemon startup and read.
    _authToken = ref.read(bootstrapTokenProvider) ?? _readLocalAuthToken();
    _client = ClawdClient(url: url, authToken: _authToken);

    ref.onDispose(() {
      _disposed = true;
      _reconnectTimer?.cancel();
      _pushSubscription?.cancel();
      _client.disconnect();
    });

    // Reconnect with new URL when the daemon URL setting changes.
    ref.listen(settingsProvider, (prev, next) {
      final newUrl = next.valueOrNull?.daemonUrl;
      final oldUrl = prev?.valueOrNull?.daemonUrl;
      if (newUrl != null && newUrl != oldUrl) {
        _switchUrl(newUrl);
      }
    });

    // Auto-connect on first use.
    _connect();
    return const DaemonState(status: DaemonStatus.connecting);
  }

  /// Switch to a new daemon URL — disconnect old client, connect to new one.
  Future<void> _switchUrl(String newUrl) async {
    if (_disposed) return;
    _reconnectTimer?.cancel();
    _reconnectAttempt = 0;
    _pushSubscription?.cancel();
    _pushSubscription = null;
    _client.disconnect();
    _client = ClawdClient(url: newUrl, authToken: _authToken);
    await _connect();
  }

  /// Switch to a new host, updating both the URL and the auth token.
  ///
  /// Called by the mobile app when the user activates a paired host so that
  /// subsequent reconnects use the stored device_token for `daemon.auth`.
  Future<void> switchToHost({
    required String url,
    String? authToken,
    String? relayUrl,
    String? daemonId,
  }) async {
    if (_disposed) return;
    _reconnectTimer?.cancel();
    _reconnectAttempt = 0;
    _pushSubscription?.cancel();
    _pushSubscription = null;
    _client.disconnect();

    // Update stored token so reconnect attempts also use it.
    _authToken = authToken;

    _client = ClawdClient(
      url: url,
      authToken: authToken,
      relayUrl: relayUrl,
      daemonId: daemonId,
    );
    await _connect();
  }

  /// Read the daemon auth token from the platform-appropriate data directory.
  ///
  /// Only meaningful on desktop (macOS/Linux/Windows) where the daemon runs
  /// locally and writes the token file.  Returns null on mobile or if the
  /// file does not exist yet.
  static String? _readLocalAuthToken() {
    try {
      final path = clawdTokenFilePath();
      if (path == null) return null;
      final file = File(path);
      if (!file.existsSync()) return null;
      final token = file.readAsStringSync().trim();
      return token.isEmpty ? null : token;
    } catch (_) {
      return null;
    }
  }

  Future<void> _connect() async {
    if (_disposed) return;
    _reconnectTimer?.cancel();
    state = const DaemonState(status: DaemonStatus.connecting);
    try {
      await _client.connect();
      if (_disposed) return;
      _reconnectAttempt = 0;
      state = const DaemonState(status: DaemonStatus.connected);
      _syncConnectionMode();
      _listenForPushEvents();
      // Best-effort: fetch daemon info after connecting.
      await refreshStatus();
    } on ClawdDisconnectedError catch (e) {
      if (_disposed) return;
      dev.log('Connect failed (disconnected): $e', name: 'clawd_core');
      state = DaemonState(
          status: DaemonStatus.error, errorMessage: e.toString());
      _updateConnectionMode(ConnectionMode.offline);
      _scheduleReconnect();
    } catch (e) {
      if (_disposed) return;
      dev.log('Connect failed: $e', name: 'clawd_core');
      state = DaemonState(
          status: DaemonStatus.error, errorMessage: e.toString());
      _updateConnectionMode(ConnectionMode.offline);
      _scheduleReconnect();
    }
  }

  /// Map the client's [ClawdConnectionMode] to [ConnectionMode] and push it
  /// to [connectionModeProvider] so the UI reflects the active transport.
  void _syncConnectionMode() {
    final mode = switch (_client.connectionMode) {
      ClawdConnectionMode.lan => ConnectionMode.lan,
      ClawdConnectionMode.relay => ConnectionMode.relay,
      ClawdConnectionMode.offline => ConnectionMode.offline,
    };
    _updateConnectionMode(mode);
  }

  void _updateConnectionMode(ConnectionMode mode) {
    try {
      ref.read(connectionModeProvider.notifier).update(mode);
    } catch (_) {
      // Provider may have been disposed; ignore.
    }
  }

  void _scheduleReconnect() {
    if (_disposed || _reconnectAttempt >= _maxReconnectAttempts) return;
    final delay = _backoffDelay(_reconnectAttempt);
    dev.log(
      'Reconnect attempt ${_reconnectAttempt + 1} in ${delay.inSeconds}s',
      name: 'clawd_core',
    );
    state = state.copyWith(
      status: DaemonStatus.connecting,
      reconnectAttempt: _reconnectAttempt + 1,
    );
    _reconnectAttempt++;
    _reconnectTimer = Timer(delay, _connect);
  }

  Duration _backoffDelay(int attempt) {
    final ms = _baseDelay.inMilliseconds * pow(2, attempt).toInt();
    final jitter = Random().nextInt(1000); // up to 1s jitter
    return Duration(
        milliseconds: min(ms + jitter, _maxDelay.inMilliseconds));
  }

  /// Reconnect immediately (e.g. user tap or app foreground).
  Future<void> reconnect() {
    _reconnectAttempt = 0;
    // Clear error state immediately so UI reflects the reconnect attempt.
    // Cannot use copyWith here because it cannot set errorMessage to null.
    state = const DaemonState(status: DaemonStatus.connecting);
    return _connect();
  }

  /// Fetch daemon runtime info (version, uptime, active sessions, port).
  /// Graceful: failures set daemonInfo to null without disconnecting.
  Future<void> refreshStatus() async {
    try {
      final result = await _client.call<Map<String, dynamic>>('daemon.status');
      if (_disposed) return;
      state = state.copyWith(daemonInfo: DaemonInfo.fromJson(result));
    } catch (_) {
      if (_disposed) return;
      state = state.copyWith(daemonInfo: null);
    }
  }

  void _listenForPushEvents() {
    _pushSubscription = _client.pushEvents.listen(
      (event) {
        // Push events are handled by individual providers via ref.listen.
        // This stream is exposed via daemonPushEventsProvider.
      },
      onError: (e) {
        if (_disposed) return;
        dev.log('Push stream error: $e', name: 'clawd_core');
        state = const DaemonState(status: DaemonStatus.disconnected);
        _scheduleReconnect();
      },
      onDone: () {
        if (_disposed) return;
        state = const DaemonState(status: DaemonStatus.disconnected);
        _scheduleReconnect();
      },
    );
  }

  /// Exposes the underlying client so other providers can make RPC calls.
  ClawdClient get client => _client;
}

/// Override in the desktop layer to inject a token from DaemonManager.
/// Mobile leaves this null — DaemonNotifier falls back to reading the file.
final bootstrapTokenProvider = Provider<String?>((ref) => null);

final daemonProvider = NotifierProvider<DaemonNotifier, DaemonState>(
  DaemonNotifier.new,
);

/// Exposes the raw push-event stream from the daemon for providers that need
/// to react to server-pushed notifications (e.g. new messages, tool calls).
///
/// Watches the daemon STATE (not just the notifier) so that when the daemon
/// reconnects with a new client (e.g. after a URL switch), this provider
/// rebuilds and re-subscribes to the new client's stream.
final daemonPushEventsProvider = StreamProvider<Map<String, dynamic>>((ref) {
  // Watching state triggers rebuild on every DaemonState change.
  // When daemon reaches `connected` after a URL switch, the new client's
  // stream is picked up here.
  ref.watch(daemonProvider);
  final notifier = ref.watch(daemonProvider.notifier);
  return notifier.client.pushEvents;
});

/// Provides daemon runtime info as an AsyncValue for the About page (Sprint NN AG.9).
///
/// Returns loading while connecting, error on failure, and data with version/uptime
/// when the daemon is connected and has returned status information.
final daemonInfoProvider = Provider<AsyncValue<Map<String, dynamic>>>((ref) {
  final state = ref.watch(daemonProvider);
  switch (state.status) {
    case DaemonStatus.connecting:
    case DaemonStatus.disconnected:
      return const AsyncValue.loading();
    case DaemonStatus.error:
      return AsyncValue.error(
        state.errorMessage ?? 'Daemon unavailable',
        StackTrace.empty,
      );
    case DaemonStatus.connected:
      final info = state.daemonInfo;
      if (info == null) return const AsyncValue.loading();
      return AsyncValue.data({
        'version': info.version,
        'daemon_id': '',
        'uptime': info.uptime,
      });
  }
});
