import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'daemon_provider.dart';

/// All sessions known to the daemon. Refreshed on connect and on
/// `session.statusChanged` push events.
class SessionListNotifier extends AsyncNotifier<List<Session>> {
  @override
  Future<List<Session>> build() async {
    // ref.listen in AsyncNotifier.build() is safe in Riverpod 2.x —
    // the framework disposes these subscriptions when the provider is re-created.
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == null) return;

        if (method == 'session.statusChanged') {
          // Optimistic in-place update to avoid flicker.
          final params = event['params'] as Map<String, dynamic>?;
          final id = params?['sessionId'] as String?;
          final rawStatus = params?['status'] as String?;
          if (id != null && rawStatus != null) {
            final SessionStatus newStatus;
            try {
              newStatus = SessionStatus.values.byName(rawStatus);
            } catch (_) {
              return; // Unknown status — skip optimistic update
            }
            final current = state.valueOrNull;
            if (current != null) {
              state = AsyncValue.data(current
                  .map((s) => s.id == id ? _patchStatus(s, newStatus) : s)
                  .toList());
            }
          }
        }
      });
    });

    return _fetch();
  }

  Future<List<Session>> _fetch() async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<List<dynamic>>('session.list');
    return result
        .map((j) => Session.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      state = AsyncValue.data(await _fetch());
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<Session> create({
    required String repoPath,
    ProviderType provider = ProviderType.claude,
  }) async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<Map<String, dynamic>>(
      'session.create',
      {'repoPath': repoPath, 'provider': provider.name},
    );
    await refresh();
    return Session.fromJson(result);
  }

  /// Close a session. Maps to session.delete in the daemon (no separate close).
  Future<void> close(String sessionId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('session.delete', {'sessionId': sessionId});
    await refresh();
  }

  Future<void> pause(String sessionId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('session.pause', {'sessionId': sessionId});
    await refresh();
  }

  Future<void> resume(String sessionId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('session.resume', {'sessionId': sessionId});
    await refresh();
  }

  Future<void> cancel(String sessionId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('session.cancel', {'sessionId': sessionId});
    await refresh();
  }

  Future<void> delete(String sessionId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('session.delete', {'sessionId': sessionId});
    await refresh();
  }

  Session _patchStatus(Session s, SessionStatus newStatus) => Session(
        id: s.id,
        repoPath: s.repoPath,
        title: s.title,
        provider: s.provider,
        status: newStatus,
        createdAt: s.createdAt,
        updatedAt: s.updatedAt,
        messageCount: s.messageCount,
      );
}

final sessionListProvider =
    AsyncNotifierProvider<SessionListNotifier, List<Session>>(
  SessionListNotifier.new,
);

/// The currently focused session ID. Persisted in desktop/mobile navigation state.
final activeSessionIdProvider = StateProvider<String?>((ref) => null);

/// Derives the full Session object for the active session ID.
final activeSessionProvider = Provider<Session?>((ref) {
  final id = ref.watch(activeSessionIdProvider);
  if (id == null) return null;
  final sessions = ref.watch(sessionListProvider).valueOrNull ?? [];
  return sessions.where((s) => s.id == id).firstOrNull;
});
