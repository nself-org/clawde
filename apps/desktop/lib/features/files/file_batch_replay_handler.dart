// Handler for `file.batchReplay` daemon push notifications.
//
// The daemon broadcasts this notification on client reconnect when there are
// buffered file operations that occurred while the client was disconnected.
// This provider captures the replayed ops list so the UI can display them.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';

// ── State ─────────────────────────────────────────────────────────────────────

/// A single file operation entry from a `file.batchReplay` notification.
class ReplayedFileOp {

  factory ReplayedFileOp.fromJson(Map<String, dynamic> json) {
    return ReplayedFileOp(
      op: json['op'] as String? ?? '',
      path: json['path'] as String? ?? '',
      sessionId: json['sessionId'] as String? ?? '',
      timestamp: json['timestamp'] as String? ?? '',
      newPath: json['newPath'] as String?,
    );
  }
  const ReplayedFileOp({
    required this.op,
    required this.path,
    required this.sessionId,
    required this.timestamp,
    this.newPath,
  });

  /// The operation type: "read", "write", "delete", or "rename".
  final String op;

  /// Absolute path of the affected file.
  final String path;

  /// Session ID that performed the operation.
  final String sessionId;

  /// ISO-8601 timestamp of the operation.
  final String timestamp;

  /// Destination path for "rename" operations; null otherwise.
  final String? newPath;
}

/// State exposed by [FileBatchReplayNotifier].
class FileBatchReplayState {
  const FileBatchReplayState({
    required this.ops,
    required this.pendingCount,
  });

  /// The replayed file operations awaiting acknowledgement.
  final List<ReplayedFileOp> ops;

  /// The count reported by the daemon for this replay batch.
  final int pendingCount;

  /// Whether there is a replay batch awaiting acknowledgement.
  bool get hasPendingOps => ops.isNotEmpty;

  FileBatchReplayState copyWith({
    List<ReplayedFileOp>? ops,
    int? pendingCount,
  }) {
    return FileBatchReplayState(
      ops: ops ?? this.ops,
      pendingCount: pendingCount ?? this.pendingCount,
    );
  }
}

// ── Provider ──────────────────────────────────────────────────────────────────

/// Provider that captures `file.batchReplay` daemon push notifications.
///
/// Expose as an [AutoDisposeAsyncNotifierProvider] so it tears down cleanly
/// when no widget is watching.
final fileBatchReplayProvider =
    AsyncNotifierProvider.autoDispose<FileBatchReplay, FileBatchReplayState>(
  FileBatchReplay.new,
);

class FileBatchReplay extends AutoDisposeAsyncNotifier<FileBatchReplayState> {
  StreamSubscription<Map<String, dynamic>>? _subscription;

  @override
  Future<FileBatchReplayState> build() async {
    final daemon = ref.watch(daemonProvider);
    // Cancel any prior subscription when the provider rebuilds.
    _subscription?.cancel();
    _subscription = null;

    if (!daemon.isConnected) {
      return const FileBatchReplayState(ops: [], pendingCount: 0);
    }

    _subscribe();

    ref.onDispose(() {
      _subscription?.cancel();
      _subscription = null;
    });

    return const FileBatchReplayState(ops: [], pendingCount: 0);
  }

  void _subscribe() {
    final client = ref.read(daemonProvider.notifier).client;
    _subscription = client.pushEvents.listen((event) {
      if (event['method'] != 'file.batchReplay') return;

      final params = event['params'];
      if (params is! Map<String, dynamic>) return;

      final rawOps = params['ops'];
      if (rawOps is! List) return;

      final ops = rawOps
          .whereType<Map<String, dynamic>>()
          .map(ReplayedFileOp.fromJson)
          .toList();

      final count = (params['count'] as num?)?.toInt() ?? ops.length;

      state = AsyncData(FileBatchReplayState(
        ops: ops,
        pendingCount: count,
      ));
    });
  }

  /// Mark the current replay batch as processed and clear the ops list.
  void acknowledge() {
    state = const AsyncData(FileBatchReplayState(ops: [], pendingCount: 0));
  }
}
