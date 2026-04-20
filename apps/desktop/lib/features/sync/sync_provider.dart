import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';

// ─── State ────────────────────────────────────────────────────────────────────

/// Sync state exposed to the UI.
class SyncStatusState {
  const SyncStatusState({
    required this.syncEnabled,
    this.lastSyncAt,
    this.syncError,
  });

  /// Whether ClawDE+ server-sync is active for this daemon.
  final bool syncEnabled;

  /// Timestamp of the most recent successful sync cycle. Null until first sync.
  final DateTime? lastSyncAt;

  /// Non-null when the sync service reported an error on last poll.
  final String? syncError;

  SyncStatusState copyWith({
    bool? syncEnabled,
    DateTime? lastSyncAt,
    String? syncError,
  }) =>
      SyncStatusState(
        syncEnabled: syncEnabled ?? this.syncEnabled,
        lastSyncAt: lastSyncAt ?? this.lastSyncAt,
        syncError: syncError ?? this.syncError,
      );
}

// ─── Provider ────────────────────────────────────────────────────────────────

/// Provider that reflects the ClawDE+ server-sync feature-gate state.
///
/// The daemon performs the actual sync loop; this provider calls `license.get`
/// and reads `features.clawdePlus`. When [SyncStatusState.syncEnabled] is
/// true the daemon is syncing session snapshots to
/// `api.clawde.io/sync/sessions` every 30 seconds.
final syncStateProvider =
    NotifierProvider.autoDispose<SyncState, SyncStatusState>(
  SyncState.new,
);

class SyncState extends AutoDisposeNotifier<SyncStatusState> {
  @override
  SyncStatusState build() {
    // Kick off an async check immediately, then rebuild when daemon state changes.
    _checkLicenseAndBind();
    return const SyncStatusState(syncEnabled: false);
  }

  Future<void> _checkLicenseAndBind() async {
    // Re-run whenever daemon connection changes.
    final daemon = ref.watch(daemonProvider);
    if (!daemon.isConnected) {
      state = const SyncStatusState(syncEnabled: false);
      return;
    }

    try {
      final client = ref.read(daemonProvider.notifier).client;
      final result = await client.call<Map<String, dynamic>>('license.get');
      final features = result['features'] as Map<String, dynamic>? ?? {};
      final clawdePlus = features['clawdePlus'] as bool? ?? false;

      state = SyncStatusState(
        syncEnabled: clawdePlus,
        lastSyncAt: state.lastSyncAt,
        syncError: clawdePlus ? null : state.syncError,
      );
    } catch (e) {
      state = SyncStatusState(
        syncEnabled: false,
        syncError: e.toString(),
      );
    }
  }

  /// Manually refresh the license/sync status from the daemon.
  Future<void> refresh() => _checkLicenseAndBind();
}
