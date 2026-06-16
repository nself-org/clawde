import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'daemon_provider.dart';

class RepoState {
  final RepoStatus? repoStatus;
  final bool isLoading;
  final String? error;
  final String? currentPath;

  const RepoState({
    this.repoStatus,
    this.isLoading = false,
    this.error,
    this.currentPath,
  });

  RepoState copyWith({
    RepoStatus? repoStatus,
    bool? isLoading,
    String? error,
    String? currentPath,
  }) =>
      RepoState(
        repoStatus: repoStatus ?? this.repoStatus,
        isLoading: isLoading ?? this.isLoading,
        error: error ?? this.error,
        currentPath: currentPath ?? this.currentPath,
      );
}

class RepoNotifier extends AsyncNotifier<RepoState> {
  @override
  Future<RepoState> build() async {
    // Listen to repo.statusChanged push events and auto-refresh.
    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        if (event['method'] == 'repo.statusChanged') {
          refresh();
        }
      });
    });

    return const RepoState();
  }

  /// Open a repository — calls repo.open and stores the path for future refreshes.
  Future<void> open(String repoPath) async {
    state = const AsyncValue.loading();
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final result = await client.call<Map<String, dynamic>>(
        'repo.open',
        {'repoPath': repoPath},
      );
      final repoStatus = RepoStatus.fromJson(result);
      state = AsyncValue.data(RepoState(
        repoStatus: repoStatus,
        currentPath: repoPath,
      ));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  /// Refresh repo status using the stored current path.
  Future<void> refresh() async {
    final currentPath = state.valueOrNull?.currentPath;
    if (currentPath == null) return;

    state = AsyncValue.data((state.valueOrNull ?? const RepoState()).copyWith(isLoading: true));
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final result = await client.call<Map<String, dynamic>>(
        'repo.status',
        {'repoPath': currentPath},
      );
      final repoStatus = RepoStatus.fromJson(result);
      state = AsyncValue.data(RepoState(
        repoStatus: repoStatus,
        currentPath: currentPath,
      ));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  /// Clear the active repository state. The daemon has no repo.close method —
  /// the repo watcher stops automatically when the session ends.
  Future<void> close() async {
    state = const AsyncValue.data(RepoState());
  }

  /// Get full diff of all unstaged changes in the active repo.
  Future<String> getDiff() async {
    final currentPath = state.valueOrNull?.currentPath;
    if (currentPath == null) throw StateError('No repository open');
    final client = ref.read(daemonProvider.notifier).client;
    return await client.call<String>('repo.diff', {'repoPath': currentPath});
  }

  /// Get diff for a single file. Pass [staged] = true for staged changes.
  Future<String> getFileDiff(String filePath, {bool staged = false}) async {
    final currentPath = state.valueOrNull?.currentPath;
    if (currentPath == null) throw StateError('No repository open');
    final client = ref.read(daemonProvider.notifier).client;
    return await client.call<String>('repo.fileDiff', {
      'repoPath': currentPath,
      'path': filePath,
      'staged': staged,
    });
  }
}

final repoProvider = AsyncNotifierProvider<RepoNotifier, RepoState>(
  RepoNotifier.new,
);
