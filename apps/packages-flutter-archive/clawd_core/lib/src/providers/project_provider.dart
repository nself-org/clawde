import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'daemon_provider.dart';

// ─── Project list ─────────────────────────────────────────────────────────────

/// All projects known to the daemon.
/// Re-fetches on daemon connect. Mutated optimistically on create.
class ProjectListNotifier extends AsyncNotifier<List<Project>> {
  @override
  Future<List<Project>> build() async {
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    return _fetch();
  }

  Future<List<Project>> _fetch() async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<List<dynamic>>('project.list');
    return result
        .map((j) => Project.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }

  /// Create a new project. Returns the created [Project] and refreshes the list.
  Future<Project> create({
    required String name,
    String? rootPath,
    String? description,
  }) async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<Map<String, dynamic>>(
      'project.create',
      {
        'name': name,
        if (rootPath != null) 'root_path': rootPath,
        if (description != null) 'description': description,
      },
    );
    final project = Project.fromJson(result);
    await refresh();
    return project;
  }

  /// Delete a project by ID and refresh the list.
  Future<void> delete(String projectId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('project.delete', {'id': projectId});
    await refresh();
  }

  /// Add a repo path to a project.
  Future<void> addRepo(String projectId, String repoPath) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('project.addRepo', {
      'project_id': projectId,
      'repo_path': repoPath,
    });
    await refresh();
  }

  /// Remove a repo path from a project.
  Future<void> removeRepo(String projectId, String repoPath) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('project.removeRepo', {
      'project_id': projectId,
      'repo_path': repoPath,
    });
    await refresh();
  }
}

final projectListProvider =
    AsyncNotifierProvider<ProjectListNotifier, List<Project>>(
  ProjectListNotifier.new,
);

// ─── Active project ───────────────────────────────────────────────────────────

/// The currently selected project ID.
/// Set by the UI when the user picks a project; cleared when they deselect.
final activeProjectIdProvider = StateProvider<String?>((ref) => null);

/// Derives the full [Project] object for the active project ID.
/// Returns null when no project is selected or the list is still loading.
final activeProjectProvider = Provider<Project?>((ref) {
  final id = ref.watch(activeProjectIdProvider);
  if (id == null) return null;
  final projects = ref.watch(projectListProvider).valueOrNull ?? [];
  return projects.where((p) => p.id == id).firstOrNull;
});

// ─── Active project with repos ────────────────────────────────────────────────

/// Fetches the full [ProjectWithRepos] for the currently active project.
/// Returns null when no project is selected.
/// Automatically re-fetches whenever the active project ID changes.
class ActiveProjectWithReposNotifier
    extends AsyncNotifier<ProjectWithRepos?> {
  @override
  Future<ProjectWithRepos?> build() async {
    final activeId = ref.watch(activeProjectIdProvider);
    if (activeId == null) return null;

    // Re-fetch when daemon reconnects.
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    return _fetch(activeId);
  }

  Future<ProjectWithRepos?> _fetch(String projectId) async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<Map<String, dynamic>>(
      'project.get',
      {'id': projectId},
    );
    return ProjectWithRepos.fromJson(result);
  }

  Future<void> refresh() async {
    final activeId = ref.read(activeProjectIdProvider);
    if (activeId == null) {
      state = const AsyncValue.data(null);
      return;
    }
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(() => _fetch(activeId));
  }
}

final activeProjectWithReposProvider =
    AsyncNotifierProvider<ActiveProjectWithReposNotifier, ProjectWithRepos?>(
  ActiveProjectWithReposNotifier.new,
);
