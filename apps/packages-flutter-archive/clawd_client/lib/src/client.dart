import 'dart:async';
import 'dart:convert';
import 'dart:developer' as dev;
import 'dart:typed_data';

import 'package:clawd_proto/clawd_proto.dart';
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:web_socket_channel/web_socket_channel.dart';

import 'exceptions.dart';
import 'relay_crypto.dart';

/// Default clawd daemon port.
const int kClawdPort = 4300;

/// Default timeout for RPC calls. Long-running operations (e.g. session.create,
/// session.sendMessage) inherit this; callers can override per-call if needed.
const Duration kDefaultCallTimeout = Duration(seconds: 30);

/// How the client is currently connected to the daemon.
///
/// Named [ClawdConnectionMode] to avoid clashing with the richer
/// `ConnectionMode` enum exported by `clawd_core`.
enum ClawdConnectionMode {
  /// Not connected.
  offline,

  /// Connected directly over the local network (LAN / localhost).
  lan,

  /// Connected via the ClawDE relay server (off-LAN / internet).
  relay,
}

// ─── Relay connection options ─────────────────────────────────────────────────

/// Options for connecting to the daemon via the ClawDE relay server.
///
/// When supplied to [ClawdClient], the client performs the relay handshake
/// (type: "connect") before authenticating with the daemon.  If [enableE2e]
/// is true (the default) the client also performs the X25519 key exchange and
/// encrypts all subsequent frames with ChaCha20-Poly1305.
class RelayOptions {
  const RelayOptions({
    required this.daemonId,
    required this.userToken,
    this.enableE2e = true,
    this.e2eSeed,
  });

  /// The daemon's unique ID (registered with the relay server).
  final String daemonId;

  /// JWT issued by nhost — used to authenticate with the relay server.
  final String userToken;

  /// Whether to perform E2E key exchange (recommended; default true).
  final bool enableE2e;

  /// Optional 32-byte seed for deriving a stable X25519 keypair.
  ///
  /// When set, the same keypair is used across reconnects and app restarts,
  /// providing a consistent device identity to the relay server.
  /// The caller is responsible for generating this seed once and persisting
  /// it in platform secure storage (e.g. `flutter_secure_storage`).
  ///
  /// When null (default), a fresh ephemeral keypair is generated on each
  /// connection, providing forward secrecy at the cost of key continuity.
  final Uint8List? e2eSeed;
}

// ─── Client ───────────────────────────────────────────────────────────────────

/// JSON-RPC 2.0 WebSocket client for the clawd daemon.
///
/// Can connect directly (LAN) or via the ClawDE relay server (remote).
/// When [relayOptions] is supplied the client performs the relay handshake
/// and optional E2E encryption automatically.
///
/// For mobile device connections, supply [relayUrl] and [daemonId] to enable
/// automatic LAN → relay fallback: the client tries the direct [url] first
/// (2-second timeout), then falls back to the relay if it fails.
///
/// Usage (local):
/// ```dart
/// final client = ClawdClient(authToken: token);
/// await client.connect();
/// final session = Session.fromJson(await client.call('session.create', {...}));
/// ```
///
/// Usage (relay with E2E):
/// ```dart
/// final client = ClawdClient(
///   url: 'wss://api.clawde.io/relay/ws',
///   relayOptions: RelayOptions(daemonId: id, userToken: jwt),
///   authToken: daemonToken,
/// );
/// await client.connect();
/// ```
///
/// Usage (mobile with LAN → relay fallback):
/// ```dart
/// final client = ClawdClient(
///   url: 'ws://192.168.1.5:4300',
///   authToken: deviceToken,
///   relayUrl: 'wss://api.clawde.io/relay/ws',
///   daemonId: 'abc123',
/// );
/// await client.connect(); // tries LAN first, falls back to relay
/// print(client.connectionMode); // ClawdConnectionMode.lan or .relay
/// ```
class ClawdClient {
  ClawdClient({
    this.url = 'ws://127.0.0.1:$kClawdPort',
    this.callTimeout = kDefaultCallTimeout,
    this.authToken,
    this.relayOptions,
    this.relayHandshakeTimeout = const Duration(seconds: 10),
    this.onRelayProgress,
    this.relayUrl,
    this.daemonId,
    this.queueWhenOffline = true,
    @visibleForTesting WebSocketChannel Function(Uri)? channelFactory,
  }) : _channelFactory = channelFactory ?? WebSocketChannel.connect;

  final String url;
  final Duration callTimeout;

  /// Timeout for each relay handshake step (connect confirmation, E2E hello).
  /// Defaults to 10 seconds.
  final Duration relayHandshakeTimeout;

  /// Optional callback invoked during the relay handshake to report progress.
  /// The argument is a human-readable status string (e.g. "connecting",
  /// "waiting for relay", "performing E2E handshake", "authenticating").
  final void Function(String status)? onRelayProgress;

  /// Auth token for the daemon.  When set, [connect] sends a `daemon.auth`
  /// RPC immediately after the WebSocket (and optional relay/E2E) handshake.
  final String? authToken;

  /// When set, the client connects via the ClawDE relay server instead of
  /// connecting directly to the daemon.
  final RelayOptions? relayOptions;

  /// Relay WebSocket URL for automatic LAN → relay fallback.
  ///
  /// When set alongside [daemonId], the client will try [url] (direct LAN)
  /// first with a 2-second timeout.  On failure it retries via the relay.
  /// Has no effect when [relayOptions] is also set (explicit relay mode).
  final String? relayUrl;

  /// Daemon hardware fingerprint — required for relay fallback routing.
  /// Obtained from the `device.pair` RPC response field `daemon_id`.
  final String? daemonId;

  /// Whether to queue outbound RPC calls when the client is disconnected,
  /// draining the queue on next successful connection.  Defaults to true.
  final bool queueWhenOffline;

  final WebSocketChannel Function(Uri) _channelFactory;

  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _subscription;
  bool _disposed = false;
  /// True once [connect] has successfully established a channel at least once.
  /// Used to distinguish a fresh (never-connected) client from a reconnecting
  /// one: queuing only makes sense for the latter.
  bool _hasEverConnected = false;
  int _idCounter = 0;
  final Map<int, Completer<dynamic>> _pending = {};
  final StreamController<Map<String, dynamic>> _pushEvents =
      StreamController.broadcast();

  // E2E session (set after handshake, null for direct/unencrypted connections).
  RelayE2eSession? _e2eSession;

  // ─── Connection mode tracking ────────────────────────────────────────────

  ClawdConnectionMode _connectionMode = ClawdConnectionMode.offline;
  final StreamController<ClawdConnectionMode> _connectionModeController =
      StreamController.broadcast();

  /// The current connection mode (offline / lan / relay).
  ClawdConnectionMode get connectionMode => _connectionMode;

  /// Stream that emits a new [ClawdConnectionMode] whenever the connection state
  /// transitions (connect, fallback to relay, disconnect, etc.).
  Stream<ClawdConnectionMode> get connectionModeStream =>
      _connectionModeController.stream;

  void _setConnectionMode(ClawdConnectionMode mode) {
    _connectionMode = mode;
    if (!_connectionModeController.isClosed) {
      _connectionModeController.add(mode);
    }
  }

  // ─── Offline message queue ───────────────────────────────────────────────

  /// Pending calls enqueued while disconnected (only used when [queueWhenOffline]).
  final List<_QueuedCall> _callQueue = [];

  bool get isConnected => _channel != null;

  /// Stream of server-push events (session updates, git status, tool calls).
  Stream<Map<String, dynamic>> get pushEvents => _pushEvents.stream;

  Future<void> connect() async {
    final relay = relayOptions;
    if (relay != null) {
      // Explicit relay mode: connect directly to the relay URL.
      await _connectWebSocket(url, ClawdConnectionMode.relay);
      await _doRelayHandshake(relay);
    } else {
      // LAN mode with optional fallback.
      final bool connectedLan = await _tryConnectLan();
      if (!connectedLan) {
        // LAN failed — attempt relay fallback if coordinates are provided.
        final rUrl = relayUrl;
        final dId = daemonId;
        if (rUrl != null && dId != null) {
          onRelayProgress?.call('LAN unreachable — connecting via relay');
          await _connectWebSocket(rUrl, ClawdConnectionMode.relay);
          // Device relay: authenticate with the daemon using authToken.
          // The relay server itself is authenticated via the daemon auth RPC.
          final token = authToken;
          if (token != null && token.isNotEmpty) {
            onRelayProgress?.call('authenticating via relay');
            await call<Map<String, dynamic>>('daemon.auth', {'token': token});
          }
          onRelayProgress?.call('connected via relay');
          _drainCallQueue();
          return;
        }
        // No relay fallback available — propagate the connection error.
        throw Exception('Cannot connect to daemon at $url');
      }
    }

    // Authenticate with the daemon (encrypted when E2E is active).
    final token = authToken;
    if (token != null && token.isNotEmpty) {
      onRelayProgress?.call('authenticating');
      await call<Map<String, dynamic>>('daemon.auth', {'token': token});
    }
    onRelayProgress?.call('connected');
    _drainCallQueue();
  }

  /// Attempt a direct WebSocket connection to [url] with a 2-second timeout.
  /// Returns true if the connection succeeded, false on failure/timeout.
  Future<bool> _tryConnectLan() async {
    try {
      await _connectWebSocket(url, ClawdConnectionMode.lan)
          .timeout(const Duration(seconds: 2));
      return true;
    } catch (_) {
      // Clean up any partial state.
      _subscription?.cancel();
      _subscription = null;
      _channel?.sink.close();
      _channel = null;
      _e2eSession = null;
      return false;
    }
  }

  Future<void> _connectWebSocket(String wsUrl, ClawdConnectionMode mode) async {
    final channel = _channelFactory(Uri.parse(wsUrl));
    _channel = channel;
    _hasEverConnected = true;
    _subscription = channel.stream.listen(
      (raw) => _processMessage(raw),
      onDone: _onDisconnect,
      onError: (_) => _onDisconnect(),
    );
    _setConnectionMode(mode);
  }

  Future<void> _doRelayHandshake(RelayOptions relay) async {
    // 1. Send relay connect message.
    onRelayProgress?.call('connecting to relay');
    _sendRaw(jsonEncode({
      'type': 'connect',
      'daemonId': relay.daemonId,
      'token': relay.userToken,
    }));

    // 2. Wait for relay to confirm the connection.
    onRelayProgress?.call('waiting for relay confirmation');
    await _waitForPushEvent(
      'connected',
      timeout: relayHandshakeTimeout,
    );

    // 3. E2E handshake.
    if (relay.enableE2e) {
      onRelayProgress?.call('performing E2E key exchange');
      await _performE2eHandshake();
    }
  }

  void disconnect() {
    _disposed = true;
    _subscription?.cancel();
    _subscription = null;
    _channel?.sink.close();
    _channel = null;
    _e2eSession = null;
    _setConnectionMode(ClawdConnectionMode.offline);
    // Fail all queued calls immediately.
    for (final queued in _callQueue) {
      queued.completer.completeError(const ClawdDisconnectedError());
    }
    _callQueue.clear();
    _connectionModeController.close();
  }

  /// Send all queued calls that accumulated while offline.
  void _drainCallQueue() {
    if (_callQueue.isEmpty) return;
    final toSend = List<_QueuedCall>.from(_callQueue);
    _callQueue.clear();
    for (final queued in toSend) {
      // Re-issue each queued call.  If it fails, complete with the error.
      call<dynamic>(queued.method, queued.params).then(
        (result) {
          if (!queued.completer.isCompleted) queued.completer.complete(result);
        },
        onError: (Object e) {
          if (!queued.completer.isCompleted) queued.completer.completeError(e);
        },
      );
    }
  }

  /// Send a JSON-RPC 2.0 request and return the decoded result.
  ///
  /// Throws [ClawdDisconnectedError] if not connected or connection drops.
  /// Throws [ClawdRpcError] if the daemon returns an error response.
  /// Throws [ClawdTimeoutError] if no response arrives within [callTimeout].
  ///
  /// When [queueWhenOffline] is true and the client is not currently connected,
  /// the call is held in an internal queue and sent on the next successful
  /// connection instead of throwing [ClawdDisconnectedError] immediately.
  /// Maximum safe integer for JSON (2^53 - 1). After this, reset the counter.
  static const int _maxSafeId = 9007199254740991; // Number.MAX_SAFE_INTEGER

  Future<T> call<T>(String method, [Map<String, dynamic>? params]) async {
    if (_channel == null) {
      if (queueWhenOffline && _hasEverConnected && !_disposed) {
        // Queue the call and return a future that resolves when drained.
        final completer = Completer<dynamic>();
        _callQueue.add(_QueuedCall(
          method: method,
          params: params,
          completer: completer,
        ));
        dev.log(
          'Queued call "$method" (offline, queue depth: ${_callQueue.length})',
          name: 'clawd_client',
        );
        return (await completer.future.timeout(
          callTimeout,
          onTimeout: () {
            _callQueue.removeWhere((q) => q.completer == completer);
            throw ClawdTimeoutError(method);
          },
        )) as T;
      }
      throw const ClawdDisconnectedError();
    }

    // Reset counter when it would overflow and no calls are pending.
    if (_idCounter >= _maxSafeId && _pending.isEmpty) {
      assert(() {
        dev.log(
          'RPC _idCounter reset from $_idCounter to 0',
          name: 'clawd_client',
        );
        return true;
      }());
      _idCounter = 0;
    }
    final id = ++_idCounter;
    final completer = Completer<dynamic>();
    _pending[id] = completer;

    final text = jsonEncode(
      RpcRequest(method: method, params: params, id: id).toJson(),
    );
    await _sendFrame(text);

    try {
      return (await completer.future.timeout(
        callTimeout,
        onTimeout: () {
          _pending.remove(id);
          throw ClawdTimeoutError(method);
        },
      )) as T;
    } catch (_) {
      _pending.remove(id);
      rethrow;
    }
  }

  // ─── Internal ─────────────────────────────────────────────────────────────

  void _sendRaw(String text) {
    _channel?.sink.add(text);
  }

  /// Send a frame, encrypting it if E2E is active.
  Future<void> _sendFrame(String text) async {
    final session = _e2eSession;
    if (session != null) {
      final payload = await session.encrypt(text);
      _sendRaw(jsonEncode({'type': 'e2e', 'payload': payload}));
    } else {
      _sendRaw(text);
    }
  }

  void _processMessage(dynamic raw) {
    _handleMessageAsync(raw).catchError((Object e) {
      dev.log('message processing error: $e', name: 'clawd_client');
    });
  }

  Future<void> _handleMessageAsync(dynamic raw) async {
    if (_disposed) return;
    Map<String, dynamic> json;
    try {
      json = jsonDecode(raw as String) as Map<String, dynamic>;
    } catch (_) {
      return;
    }

    // Decrypt E2E frames.
    if (json['type'] == 'e2e') {
      final session = _e2eSession;
      if (session == null) return;
      final payload = json['payload'] as String?;
      if (payload == null) return;
      try {
        final decrypted = await session.decrypt(payload);
        json = jsonDecode(decrypted) as Map<String, dynamic>;
      } catch (e) {
        dev.log('E2E decrypt failed: $e', name: 'clawd_client');
        return;
      }
    }

    // Relay/protocol messages (no `id`) → push events stream.
    if (!json.containsKey('id') || json['id'] == null) {
      _pushEvents.add(json);
      return;
    }

    // JSON-RPC response → complete pending call.
    final response = RpcResponse.fromJson(json);
    final completer = _pending.remove(response.id);
    if (completer == null) return;

    if (response.isError) {
      final err = response.error!;
      dev.log(
        'RPC error [${err.code}]: ${err.message}',
        name: 'clawd_client',
      );
      completer.completeError(ClawdRpcError(
        code: err.code,
        message: err.message,
      ));
    } else {
      completer.complete(response.result);
    }
  }

  // ─── Task API ──────────────────────────────────────────────────────────────

  /// List tasks with optional filters.
  Future<List<AgentTask>> listTasks({
    String? repoPath,
    String? status,
    String? agent,
    String? severity,
    String? phase,
    int? limit,
    int? offset,
  }) async {
    final params = <String, dynamic>{
      if (repoPath != null) 'repo_path': repoPath,
      if (status != null) 'status': status,
      if (agent != null) 'agent': agent,
      if (severity != null) 'severity': severity,
      if (phase != null) 'phase': phase,
      if (limit != null) 'limit': limit,
      if (offset != null) 'offset': offset,
    };
    final result = await call<Map<String, dynamic>>('tasks.list', params);
    final list = result['tasks'] as List<dynamic>? ?? [];
    return list.map((e) => AgentTask.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Get a single task by ID.
  Future<AgentTask> getTask(String taskId) async {
    final result = await call<Map<String, dynamic>>('tasks.get', {'task_id': taskId});
    return AgentTask.fromJson(result['task'] as Map<String, dynamic>);
  }

  /// Atomically claim a task for an agent. Returns the claimed task.
  Future<({AgentTask task, bool isResume})> claimTask(
    String taskId,
    String agentId,
  ) async {
    final result = await call<Map<String, dynamic>>('tasks.claim', {
      'task_id': taskId,
      'agent_id': agentId,
    });
    return (
      task: AgentTask.fromJson(result['task'] as Map<String, dynamic>),
      isResume: result['is_resume'] as bool? ?? false,
    );
  }

  /// Release a claimed task back to pending.
  Future<void> releaseTask(String taskId, String agentId) async {
    await call<Map<String, dynamic>>('tasks.release', {
      'task_id': taskId,
      'agent_id': agentId,
    });
  }

  /// Send a heartbeat for an in-progress task.
  Future<void> taskHeartbeat(String taskId, String agentId) async {
    await call<Map<String, dynamic>>('tasks.heartbeat', {
      'task_id': taskId,
      'agent_id': agentId,
    });
  }

  /// Update task status.
  Future<AgentTask> updateTaskStatus(
    String taskId,
    String status, {
    String? notes,
    String? blockReason,
    String? agentId,
  }) async {
    final result = await call<Map<String, dynamic>>('tasks.updateStatus', {
      'task_id': taskId,
      'status': status,
      if (notes != null) 'notes': notes,
      if (blockReason != null) 'block_reason': blockReason,
      if (agentId != null) 'agent_id': agentId,
    });
    return AgentTask.fromJson(result['task'] as Map<String, dynamic>);
  }

  /// Add a new task to the queue.
  Future<AgentTask> addTask(Map<String, dynamic> params) async {
    final result = await call<Map<String, dynamic>>('tasks.addTask', params);
    return AgentTask.fromJson(result['task'] as Map<String, dynamic>);
  }

  /// Log an activity entry (auto type).
  Future<String> logActivity({
    required String agentId,
    required String action,
    required String repoPath,
    String? taskId,
    String? phase,
    String? detail,
    String? entryType,
  }) async {
    final result = await call<Map<String, dynamic>>('tasks.logActivity', {
      'agent_id': agentId,
      'action': action,
      'repo_path': repoPath,
      if (taskId != null) 'task_id': taskId,
      if (phase != null) 'phase': phase,
      if (detail != null) 'detail': detail,
      if (entryType != null) 'entry_type': entryType,
    });
    return result['id'] as String;
  }

  /// Post a human-readable note attached to a task.
  Future<String> postNote({
    required String agentId,
    required String note,
    required String repoPath,
    String? taskId,
    String? phase,
  }) async {
    final result = await call<Map<String, dynamic>>('tasks.note', {
      'agent_id': agentId,
      'note': note,
      'repo_path': repoPath,
      if (taskId != null) 'task_id': taskId,
      if (phase != null) 'phase': phase,
    });
    return result['id'] as String;
  }

  /// Query activity log entries.
  Future<List<ActivityLogEntry>> queryActivity({
    String? repoPath,
    String? taskId,
    String? agent,
    String? phase,
    String? entryType,
    String? action,
    int? since,
    int? limit,
    int? offset,
  }) async {
    final result = await call<Map<String, dynamic>>('tasks.activity', {
      if (repoPath != null) 'repo_path': repoPath,
      if (taskId != null) 'task_id': taskId,
      if (agent != null) 'agent': agent,
      if (phase != null) 'phase': phase,
      if (entryType != null) 'entry_type': entryType,
      if (action != null) 'action': action,
      if (since != null) 'since': since,
      if (limit != null) 'limit': limit,
      if (offset != null) 'offset': offset,
    });
    final list = result['entries'] as List<dynamic>? ?? [];
    return list
        .map((e) => ActivityLogEntry.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Get a summary count of tasks by status.
  Future<TaskSummary> taskSummary({String? repoPath}) async {
    final result = await call<Map<String, dynamic>>(
      'tasks.summary',
      {if (repoPath != null) 'repo_path': repoPath},
    );
    return TaskSummary.fromJson(result);
  }

  /// Import tasks from a planning markdown document.
  Future<int> tasksFromPlanning(String path, String repoPath) async {
    final result = await call<Map<String, dynamic>>('tasks.fromPlanning', {
      'path': path,
      'repo_path': repoPath,
    });
    return (result['imported'] as num?)?.toInt() ?? 0;
  }

  /// Sync active.md → DB.
  Future<int> syncTasks(String repoPath) async {
    final result = await call<Map<String, dynamic>>('tasks.sync', {
      'repo_path': repoPath,
    });
    return (result['synced'] as num?)?.toInt() ?? 0;
  }

  // ─── Agent registry API ────────────────────────────────────────────────────

  /// Register this agent with the daemon.
  Future<AgentView> registerAgent({
    required String agentId,
    String agentType = 'claude',
    String? sessionId,
    String repoPath = '',
  }) async {
    final result = await call<Map<String, dynamic>>('tasks.agents.register', {
      'agent_id': agentId,
      'agent_type': agentType,
      if (sessionId != null) 'session_id': sessionId,
      'repo_path': repoPath,
    });
    return AgentView.fromJson(result['agent'] as Map<String, dynamic>);
  }

  /// List registered agents.
  Future<List<AgentView>> listAgents({String? repoPath}) async {
    final result = await call<Map<String, dynamic>>(
      'tasks.agents.list',
      {if (repoPath != null) 'repo_path': repoPath},
    );
    final list = result['agents'] as List<dynamic>? ?? [];
    return list
        .map((e) => AgentView.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Send an agent heartbeat.
  Future<void> agentHeartbeat(String agentId) async {
    await call<Map<String, dynamic>>('tasks.agents.heartbeat', {
      'agent_id': agentId,
    });
  }

  /// Mark an agent as disconnected.
  Future<void> agentDisconnect(String agentId) async {
    await call<Map<String, dynamic>>('tasks.agents.disconnect', {
      'agent_id': agentId,
    });
  }

  // ─── Session mode API ──────────────────────────────────────────────────────

  /// Set the GCI mode for a session (NORMAL / LEARN / STORM / FORGE / CRUNCH).
  Future<void> setSessionMode(String sessionId, String mode) async {
    await call<Map<String, dynamic>>('session.setMode', {
      'session_id': sessionId,
      'mode': mode,
    });
  }

  // ─── Worktree API ──────────────────────────────────────────────────────────

  /// Create a git worktree for the given task and repo.
  Future<WorktreeInfo> createWorktree({
    required String taskId,
    required String taskTitle,
    required String repoPath,
  }) async {
    final result = await call<Map<String, dynamic>>('worktrees.create', {
      'task_id': taskId,
      'task_title': taskTitle,
      'repo_path': repoPath,
    });
    return WorktreeInfo.fromJson(result);
  }

  /// List all worktrees known to the daemon.
  Future<List<WorktreeInfo>> listWorktrees() async {
    final result = await call<Map<String, dynamic>>('worktrees.list', {});
    final list = result['worktrees'] as List<dynamic>? ?? [];
    return list
        .map((e) => WorktreeInfo.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Get a unified diff for the task worktree vs its base branch.
  Future<WorktreeDiff> worktreeDiff(String taskId) async {
    final result =
        await call<Map<String, dynamic>>('worktrees.diff', {'task_id': taskId});
    return WorktreeDiff.fromJson(result);
  }

  /// Stage all changes and create a commit in the task worktree.
  Future<String> commitWorktree(String taskId, String message) async {
    final result = await call<Map<String, dynamic>>('worktrees.commit', {
      'task_id': taskId,
      'message': message,
    });
    return result['sha'] as String? ?? '';
  }

  /// Mark the worktree as accepted and merge to main.
  Future<void> acceptWorktree(String taskId) async {
    await call<Map<String, dynamic>>(
        'worktrees.accept', {'task_id': taskId});
  }

  /// Reject (abandon) the task worktree, removing the branch.
  Future<void> rejectWorktree(String taskId, {String? reason}) async {
    await call<Map<String, dynamic>>('worktrees.reject', {
      'task_id': taskId,
      if (reason != null) 'reason': reason,
    });
  }

  /// Hard-delete the task worktree and its branch.
  Future<void> deleteWorktree(String taskId) async {
    await call<Map<String, dynamic>>(
        'worktrees.delete', {'task_id': taskId});
  }

  /// Merge a Done worktree to main (alias for accept flow).
  Future<void> mergeWorktree(String taskId) async {
    await call<Map<String, dynamic>>(
        'worktrees.merge', {'task_id': taskId});
  }

  /// Remove all merged/abandoned worktrees from disk and the registry.
  Future<int> cleanupWorktrees() async {
    final result =
        await call<Map<String, dynamic>>('worktrees.cleanup', {});
    return (result['removed'] as num?)?.toInt() ?? 0;
  }

  // ─── License API ───────────────────────────────────────────────────────────

  /// Get the cached license info (tier, features).
  Future<Map<String, dynamic>> getLicense() async {
    return call<Map<String, dynamic>>('license.get', {});
  }

  /// Refresh the license from the server and return the updated info.
  Future<Map<String, dynamic>> checkLicense() async {
    return call<Map<String, dynamic>>('license.check', {});
  }

  /// Get just the license tier string (free / personal_remote / cloud_basic …).
  Future<String> getLicenseTier() async {
    final result = await call<Map<String, dynamic>>('license.tier', {});
    return result['tier'] as String? ?? 'free';
  }

  // ─── System Resources API ──────────────────────────────────────────────────

  /// Get current RAM usage and session tier counts from `system.resources`.
  Future<Map<String, dynamic>> systemResources() async {
    return call<Map<String, dynamic>>('system.resources', {});
  }

  // ─── AFS API ───────────────────────────────────────────────────────────────

  /// Scaffold a .claude/ directory tree at the given path.
  Future<List<String>> afsInit(String path) async {
    final result = await call<Map<String, dynamic>>('afs.init', {'path': path});
    final created = result['created'] as List<dynamic>? ?? [];
    return created.cast<String>();
  }

  /// Get AFS status for a repo.
  Future<Map<String, dynamic>> afsStatus(String repoPath) async {
    return call<Map<String, dynamic>>('afs.status', {'repo_path': repoPath});
  }

  /// Register a project path for AFS file watching.
  Future<void> afsRegister(String repoPath) async {
    await call<Map<String, dynamic>>('afs.register', {'repo_path': repoPath});
  }

  // ─── Session Intelligence API ──────────────────────────────────────────────

  /// Returns the health state for a session.
  ///
  /// Returns a map with keys: healthScore, totalTurns, needsRefresh, etc.
  /// Returns null if the daemon doesn't support `session.health` yet.
  Future<Map<String, dynamic>?> sessionHealth(String sessionId) async {
    try {
      return await call<Map<String, dynamic>>(
          'session.health', {'sessionId': sessionId});
    } catch (_) {
      return null;
    }
  }

  /// Returns the split proposal for a prompt.
  ///
  /// Returns a map with keys: complexity, shouldSplit, proposal (nullable).
  /// Returns null if the daemon doesn't support `session.splitProposed` yet.
  Future<Map<String, dynamic>?> sessionSplitProposed(String prompt) async {
    try {
      return await call<Map<String, dynamic>>(
          'session.splitProposed', {'prompt': prompt});
    } catch (_) {
      return null;
    }
  }

  /// Returns the active coding standards for a session.
  /// Returns an empty list if the daemon doesn't support this RPC yet.
  Future<List<String>> sessionStandards(String sessionId) async {
    try {
      final result = await call<List<dynamic>>(
          'session.standards', {'sessionId': sessionId});
      return result.cast<String>();
    } catch (_) {
      return const [];
    }
  }

  /// Returns the detected provider knowledge contexts for a session.
  /// Returns an empty list if the daemon doesn't support this RPC yet.
  Future<List<String>> sessionProviderKnowledge(String sessionId) async {
    try {
      final result = await call<List<dynamic>>(
          'session.providerKnowledge', {'sessionId': sessionId});
      return result.cast<String>();
    } catch (_) {
      return const [];
    }
  }

  // ─── Token Usage API (Sprint H, MI.T14/T16) ───────────────────────────────

  /// Returns token + cost totals for a session.
  ///
  /// Keys: sessionId, inputTokens, outputTokens, estimatedCostUsd, messageCount.
  /// Returns null when the daemon doesn't support this RPC yet.
  Future<Map<String, dynamic>?> tokenSessionUsage(String sessionId) async {
    try {
      return await call<Map<String, dynamic>>(
          'token.sessionUsage', {'sessionId': sessionId});
    } catch (_) {
      return null;
    }
  }

  /// Returns usage broken down by model over a date range.
  ///
  /// [from] and [to] are optional ISO-8601 strings; defaults to current month.
  /// Returns a list of maps with keys: modelId, inputTokens, outputTokens,
  /// estimatedCostUsd, messageCount.
  Future<List<Map<String, dynamic>>> tokenTotalUsage({
    String? from,
    String? to,
  }) async {
    try {
      final params = <String, dynamic>{};
      if (from != null) params['from'] = from;
      if (to != null) params['to'] = to;
      final result = await call<List<dynamic>>('token.totalUsage', params);
      return result.cast<Map<String, dynamic>>();
    } catch (_) {
      return const [];
    }
  }

  /// Returns monthly spend vs optional budget cap.
  ///
  /// Keys: monthlySpendUsd, cap (null if none), pct (null if none),
  /// warning (bool), exceeded (bool).
  Future<Map<String, dynamic>?> tokenBudgetStatus({double? monthlyCap}) async {
    try {
      final params = <String, dynamic>{};
      if (monthlyCap != null) params['monthlyCap'] = monthlyCap;
      return await call<Map<String, dynamic>>('token.budgetStatus', params);
    } catch (_) {
      return null;
    }
  }

  // ─── Model Intelligence API (Sprint H) ────────────────────────────────────

  /// Pin a model to a session, bypassing auto-routing (MI.T12).
  ///
  /// Pass `model = null` to restore auto-routing.
  /// Returns the updated modelOverride value.
  Future<String?> setSessionModel(String sessionId, String? model) async {
    final result = await call<Map<String, dynamic>>(
        'session.setModel', {'sessionId': sessionId, 'model': model});
    return result['modelOverride'] as String?;
  }

  /// Add a path to the session's repo-context registry (MI.T11).
  ///
  /// Returns the created/updated context entry map.
  Future<Map<String, dynamic>?> addRepoContext(
    String sessionId,
    String path, {
    int priority = 5,
  }) async {
    try {
      return await call<Map<String, dynamic>>('session.addRepoContext', {
        'sessionId': sessionId,
        'path': path,
        'priority': priority,
      });
    } catch (_) {
      return null;
    }
  }

  /// List all repo-context entries for a session, highest-priority first (MI.T11).
  Future<List<Map<String, dynamic>>> listRepoContexts(String sessionId) async {
    try {
      final result = await call<Map<String, dynamic>>(
          'session.listRepoContexts', {'sessionId': sessionId});
      final items = result['contexts'] as List<dynamic>? ?? [];
      return items.cast<Map<String, dynamic>>();
    } catch (_) {
      return const [];
    }
  }

  /// Remove a repo-context entry by its ID (MI.T11).
  Future<void> removeRepoContext(String id) async {
    await call<Map<String, dynamic>>('session.removeRepoContext', {'id': id});
  }

  // ─── Doctor RPC helpers ────────────────────────────────────────────────────

  /// Run a doctor scan on `projectPath`.
  ///
  /// `scope` is one of `"all"`, `"afs"`, `"docs"`, `"release"` (default `"all"`).
  Future<Map<String, dynamic>> doctorScan(
    String projectPath, {
    String scope = 'all',
  }) async {
    return call<Map<String, dynamic>>('doctor.scan', {
      'project_path': projectPath,
      'scope': scope,
    });
  }

  /// Attempt auto-fix for the given finding codes.
  ///
  /// Pass an empty list to fix all fixable findings.
  Future<Map<String, dynamic>> doctorFix(
    String projectPath, {
    List<String> codes = const [],
  }) async {
    return call<Map<String, dynamic>>('doctor.fix', {
      'project_path': projectPath,
      'codes': codes,
    });
  }

  /// Approve the release plan for `version` in `projectPath`.
  Future<void> doctorApproveRelease(String projectPath, String version) async {
    await call<Map<String, dynamic>>('doctor.approveRelease', {
      'project_path': projectPath,
      'version': version,
    });
  }

  /// Install the pre-tag git hook in `projectPath`.
  Future<void> doctorHookInstall(String projectPath) async {
    await call<Map<String, dynamic>>('doctor.hookInstall', {
      'project_path': projectPath,
    });
  }

  /// Returns the current drift items detected by the daemon.
  /// Returns an empty list if the daemon doesn't support this RPC yet.
  Future<List<String>> driftList() async {
    try {
      final result = await call<List<dynamic>>('drift.list', {});
      return result.cast<String>();
    } catch (_) {
      return const [];
    }
  }

  // ─── Sprint DD — Workflow Recipes ─────────────────────────────────────────

  /// List all workflow recipes (built-in + user-defined).
  Future<Map<String, dynamic>> workflowList() async =>
      call<Map<String, dynamic>>('workflow.list', {});

  /// Create a new workflow recipe from YAML.
  Future<Map<String, dynamic>> workflowCreate({
    required String name,
    String description = '',
    required String yaml,
  }) async =>
      call<Map<String, dynamic>>(
        'workflow.create',
        {'name': name, 'description': description, 'yaml': yaml},
      );

  /// Run a workflow recipe in the given repo. Returns immediately with a runId.
  Future<Map<String, dynamic>> workflowRun({
    required String recipeId,
    String repoPath = '.',
  }) async =>
      call<Map<String, dynamic>>(
        'workflow.run',
        {'recipeId': recipeId, 'repoPath': repoPath},
      );

  /// Delete a user-defined workflow recipe by ID.
  Future<void> workflowDelete(String id) async =>
      call<Map<String, dynamic>>('workflow.delete', {'id': id});

  // ─── Sprint DD — Project Pulse ─────────────────────────────────────────────

  /// Fetch semantic change velocity for the last [days] days.
  Future<Map<String, dynamic>> projectPulse({int days = 7}) async =>
      call<Map<String, dynamic>>('project.pulse', {'days': days});

  // ─── Sprint DD — Tool Sovereignty ─────────────────────────────────────────

  /// Fetch the 7-day sovereignty report (other AI tools detected).
  Future<Map<String, dynamic>> sovereigntyReport() async =>
      call<Map<String, dynamic>>('sovereignty.report', {});

  // ─── Sprint DD — Session Replay ───────────────────────────────────────────

  /// Export a session to a portable base64 bundle.
  Future<Map<String, dynamic>> sessionExport(String sessionId) async =>
      call<Map<String, dynamic>>('session.export', {'sessionId': sessionId});

  /// Import a session bundle. Returns the new replay session ID.
  Future<Map<String, dynamic>> sessionImport(String bundle) async =>
      call<Map<String, dynamic>>('session.import', {'bundle': bundle});

  /// Start replaying an imported session at [speed]x.
  Future<Map<String, dynamic>> sessionReplay(String sessionId,
          {double speed = 1.0}) async =>
      call<Map<String, dynamic>>(
        'session.replay',
        {'sessionId': sessionId, 'speed': speed},
      );

  // ─── Sprint DD — NL Git ───────────────────────────────────────────────────

  /// Ask a natural language question about the git history of [repoPath].
  Future<Map<String, dynamic>> gitQuery({
    required String question,
    String repoPath = '.',
  }) async =>
      call<Map<String, dynamic>>(
        'git.query',
        {'question': question, 'repoPath': repoPath},
      );

  // ─── Sprint EE — CI Runner ────────────────────────────────────────────────

  /// Start a CI pipeline run in [repoPath].
  /// Returns `{ runId, status }`.
  Future<Map<String, dynamic>> ciRun({
    String repoPath = '.',
    String trigger = 'manual',
  }) async =>
      call<Map<String, dynamic>>(
        'ci.run',
        {'repo_path': repoPath, 'trigger': trigger},
      );

  /// Get status of a CI run by [runId].
  Future<Map<String, dynamic>> ciStatus(String runId) async =>
      call<Map<String, dynamic>>('ci.status', {'run_id': runId});

  /// Cancel a running CI pipeline.
  Future<Map<String, dynamic>> ciCancel(String runId) async =>
      call<Map<String, dynamic>>('ci.cancel', {'run_id': runId});

  // ─── Sprint EE — Session Sharing ─────────────────────────────────────────

  /// Create a share token for [sessionId] that expires in [expiresIn] seconds.
  Future<Map<String, dynamic>> sessionShare(
    String sessionId, {
    int expiresIn = 3600,
  }) async =>
      call<Map<String, dynamic>>(
        'session.share',
        {'session_id': sessionId, 'expires_in': expiresIn},
      );

  /// Revoke an active share token.
  Future<Map<String, dynamic>> sessionRevokeShare(String shareToken) async =>
      call<Map<String, dynamic>>(
        'session.revokeShare',
        {'share_token': shareToken},
      );

  /// List active share tokens for [sessionId].
  Future<ShareListResult> sessionShareList(String sessionId) async {
    final raw = await call<Map<String, dynamic>>(
      'session.shareList',
      {'session_id': sessionId},
    );
    return ShareListResult.fromJson(raw);
  }

  // ─── Sprint EE — Daily Digest ─────────────────────────────────────────────

  /// Fetch today's daily digest (sessions, metrics, top files).
  Future<Map<String, dynamic>> digestToday() async =>
      call<Map<String, dynamic>>('digest.today', {});

  // ─── Sprint ZZ — Instruction graph API ────────────────────────────────────

  /// Explain merged instructions for [path] (scope tree + preview).
  Future<Map<String, dynamic>> instructionsExplain(String path) async {
    try {
      return await call<Map<String, dynamic>>(
          'instructions.explain', {'path': path});
    } catch (_) {
      return const {};
    }
  }

  /// Budget report: bytes used vs budget for each provider (claude + codex).
  Future<Map<String, dynamic>> instructionsBudgetReport(
      String projectPath) async {
    try {
      return await call<Map<String, dynamic>>(
          'instructions.budgetReport', {'project_path': projectPath});
    } catch (_) {
      return const {};
    }
  }

  /// Compile instructions for [target] (`"claude"` or `"codex"`).
  Future<Map<String, dynamic>> instructionsCompile({
    required String projectPath,
    String target = 'claude',
    bool dryRun = false,
  }) async =>
      call<Map<String, dynamic>>('instructions.compile', {
        'target': target,
        'project_path': projectPath,
        'dry_run': dryRun,
      });

  /// Run the instruction linter and return an `InstructionLintReport` map.
  Future<Map<String, dynamic>> instructionsLint(String projectPath) async {
    try {
      return await call<Map<String, dynamic>>(
          'instructions.lint', {'project_path': projectPath});
    } catch (_) {
      return const {'passed': true, 'errors': 0, 'warnings': 0, 'issues': []};
    }
  }

  // ─── Sprint ZZ — Evidence pack API ────────────────────────────────────────

  /// Fetch the evidence pack for [taskId].  Returns null when none exists.
  Future<Map<String, dynamic>?> artifactsEvidencePack(String taskId) async {
    try {
      return await call<Map<String, dynamic>>(
          'artifacts.evidencePack', {'task_id': taskId});
    } catch (_) {
      return null;
    }
  }

  // ─── Internal ─────────────────────────────────────────────────────────────

  void _onDisconnect() {
    dev.log('WebSocket disconnected ($url)', name: 'clawd_client');
    _channel = null;
    _e2eSession = null;
    // Update mode without closing the stream controller — DaemonNotifier
    // will reconnect and we want to be able to emit the new mode then.
    if (!_connectionModeController.isClosed) {
      _setConnectionMode(ClawdConnectionMode.offline);
    }
    for (final c in _pending.values) {
      c.completeError(const ClawdDisconnectedError());
    }
    _pending.clear();
  }

  // ─── Relay/E2E helpers ─────────────────────────────────────────────────────

  Future<void> _performE2eHandshake() async {
    // Use a stable seed-derived keypair if provided; otherwise ephemeral.
    final seed = relayOptions?.e2eSeed;
    final handshake = seed != null
        ? await RelayE2eHandshake.createFromSeed(seed)
        : await RelayE2eHandshake.create();

    // Send client hello unencrypted — server needs our pubkey to derive the key.
    _sendRaw(jsonEncode({
      'type': 'e2e_hello',
      'pubkey': handshake.clientPubkeyB64,
    }));

    // Wait for server hello.
    final serverHello = await _waitForPushEvent(
      'e2e_hello',
      timeout: relayHandshakeTimeout,
    );
    final serverPubkey = serverHello['pubkey'] as String?;
    if (serverPubkey == null) {
      throw Exception('relay e2e_hello missing pubkey');
    }

    _e2eSession = await handshake.complete(serverPubkey);
    dev.log('E2E encryption established', name: 'clawd_client');
  }

  Future<Map<String, dynamic>> _waitForPushEvent(
    String type, {
    required Duration timeout,
  }) async {
    final completer = Completer<Map<String, dynamic>>();
    late StreamSubscription<Map<String, dynamic>> sub;
    sub = _pushEvents.stream.listen((event) {
      if (event['type'] == type && !completer.isCompleted) {
        sub.cancel();
        completer.complete(event);
      }
    });
    try {
      return await completer.future.timeout(timeout);
    } catch (_) {
      sub.cancel();
      rethrow;
    }
  }
}

// ─── Internal helper types ─────────────────────────────────────────────────────

/// A single RPC call that was enqueued while the client was offline.
class _QueuedCall {
  _QueuedCall({
    required this.method,
    required this.params,
    required this.completer,
  });

  final String method;
  final Map<String, dynamic>? params;
  final Completer<dynamic> completer;
}
