import 'dart:convert';
import 'dart:io';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'daemon_provider.dart';

// ─── Task list ────────────────────────────────────────────────────────────────

/// Filter params for [taskListProvider].
class TaskFilter {
  const TaskFilter({
    this.repoPath,
    this.status,
    this.agent,
    this.severity,
    this.phase,
    this.limit,
  });

  final String? repoPath;
  final String? status;
  final String? agent;
  final String? severity;
  final String? phase;
  final int? limit;

  @override
  bool operator ==(Object other) =>
      other is TaskFilter &&
      other.repoPath == repoPath &&
      other.status == status &&
      other.agent == agent &&
      other.severity == severity &&
      other.phase == phase &&
      other.limit == limit;

  @override
  int get hashCode => Object.hash(repoPath, status, agent, severity, phase, limit);
}

/// Provides a filtered list of agent tasks.
/// Refreshes on connect and on task.* push events.
class TaskListNotifier extends FamilyAsyncNotifier<List<AgentTask>, TaskFilter> {
  @override
  Future<List<AgentTask>> build(TaskFilter arg) async {
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == null) return;
        if (method.startsWith('task.') || method.startsWith('agent.')) {
          refresh();
        }
      });
    });

    return _fetch();
  }

  Future<List<AgentTask>> _fetch() {
    final client = ref.read(daemonProvider.notifier).client;
    return client.listTasks(
      repoPath: arg.repoPath,
      status: arg.status,
      agent: arg.agent,
      severity: arg.severity,
      phase: arg.phase,
      limit: arg.limit,
    );
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }
}

final taskListProvider =
    AsyncNotifierProviderFamily<TaskListNotifier, List<AgentTask>, TaskFilter>(
  TaskListNotifier.new,
);

// ─── Convenience per-status slices ───────────────────────────────────────────

/// Tasks grouped by status — returns a map of status → List<AgentTask>.
final tasksByStatusProvider =
    Provider.family<Map<String, List<AgentTask>>, String?>((ref, repoPath) {
  final all = ref.watch(taskListProvider(TaskFilter(repoPath: repoPath)));
  final tasks = all.valueOrNull ?? [];
  final map = <String, List<AgentTask>>{};
  for (final t in tasks) {
    (map[t.status.toJsonStr()] ??= []).add(t);
  }
  return map;
});

// ─── Task summary ─────────────────────────────────────────────────────────────

class TaskSummaryNotifier extends FamilyAsyncNotifier<TaskSummary, String?> {
  @override
  Future<TaskSummary> build(String? arg) async {
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method != null && method.startsWith('task.')) refresh();
      });
    });

    return _fetch();
  }

  Future<TaskSummary> _fetch() {
    final client = ref.read(daemonProvider.notifier).client;
    return client.taskSummary(repoPath: arg);
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }
}

final taskDashboardStatsProvider =
    AsyncNotifierProviderFamily<TaskSummaryNotifier, TaskSummary, String?>(
  TaskSummaryNotifier.new,
);

// ─── Activity feed ────────────────────────────────────────────────────────────

class ActivityFeedNotifier
    extends FamilyAsyncNotifier<List<ActivityLogEntry>, String?> {
  @override
  Future<List<ActivityLogEntry>> build(String? arg) async {
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == 'task.activityLogged') {
          // Prepend the new entry without full re-fetch for performance.
          final current = state.valueOrNull;
          if (current != null) {
            try {
              final params = event['params'] as Map<String, dynamic>?;
              if (params != null) {
                final entry = ActivityLogEntry.fromJson(params);
                if (arg == null || entry.repoPath == arg) {
                  state = AsyncValue.data([entry, ...current]);
                  return;
                }
              }
            } catch (_) {}
          }
          refresh();
        }
      });
    });

    return _fetch();
  }

  Future<List<ActivityLogEntry>> _fetch() {
    final client = ref.read(daemonProvider.notifier).client;
    return client.queryActivity(repoPath: arg, limit: 200);
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }
}

final activityFeedProvider =
    AsyncNotifierProviderFamily<ActivityFeedNotifier, List<ActivityLogEntry>, String?>(
  ActivityFeedNotifier.new,
);

// ─── Agent registry ───────────────────────────────────────────────────────────

class AgentViewListNotifier
    extends FamilyAsyncNotifier<List<AgentView>, String?> {
  @override
  Future<List<AgentView>> build(String? arg) async {
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == 'agent.connected' || method == 'agent.disconnected') {
          refresh();
        }
      });
    });

    return _fetch();
  }

  Future<List<AgentView>> _fetch() {
    final client = ref.read(daemonProvider.notifier).client;
    return client.listAgents(repoPath: arg);
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }
}

final agentListProvider =
    AsyncNotifierProviderFamily<AgentViewListNotifier, List<AgentView>, String?>(
  AgentViewListNotifier.new,
);

// ─── Selected task ────────────────────────────────────────────────────────────

/// Currently selected task ID (for the detail panel/sheet).
final selectedTaskIdProvider = StateProvider<String?>((ref) => null);

/// Derives the selected task from the task list cache — no extra RPC needed.
final selectedTaskProvider = Provider<AgentTask?>((ref) {
  final id = ref.watch(selectedTaskIdProvider);
  if (id == null) return null;
  final all = ref.watch(taskListProvider(const TaskFilter()));
  return all.valueOrNull?.where((t) => t.id == id).firstOrNull;
});

// ─── Active repo path / project selector ─────────────────────────────────────

/// The repo path currently visible in the dashboard.
/// null = "All Projects" view.
/// Both desktop and mobile set this when the user switches repos.
final activeRepoPathProvider = StateProvider<String?>((ref) => null);

// ─── Dashboard filter state ───────────────────────────────────────────────────

/// UI-level filter state for the agent dashboard filter bar.
/// Separate from [TaskFilter] (which is for RPC params).
class DashboardFilter {
  const DashboardFilter({
    this.agent,
    this.taskType,
    this.severity,
    this.status,
    this.phase,
    this.since,
  });

  final String? agent;
  final String? taskType;
  final String? severity;
  final String? status;
  final String? phase;

  /// Only show tasks updated after this timestamp (seconds since epoch).
  final int? since;

  DashboardFilter copyWith({
    String? agent,
    String? taskType,
    String? severity,
    String? status,
    String? phase,
    int? since,
  }) =>
      DashboardFilter(
        agent: agent ?? this.agent,
        taskType: taskType ?? this.taskType,
        severity: severity ?? this.severity,
        status: status ?? this.status,
        phase: phase ?? this.phase,
        since: since ?? this.since,
      );

  DashboardFilter clear() => const DashboardFilter();

  bool get isActive =>
      agent != null ||
      taskType != null ||
      severity != null ||
      status != null ||
      phase != null ||
      since != null;

  /// Convert to [TaskFilter] for RPC queries.
  TaskFilter toTaskFilter({String? repoPath}) => TaskFilter(
        repoPath: repoPath,
        status: status,
        agent: agent,
        severity: severity,
        phase: phase,
      );

  @override
  bool operator ==(Object other) =>
      other is DashboardFilter &&
      other.agent == agent &&
      other.taskType == taskType &&
      other.severity == severity &&
      other.status == status &&
      other.phase == phase &&
      other.since == since;

  @override
  int get hashCode =>
      Object.hash(agent, taskType, severity, status, phase, since);
}

/// Global dashboard filter. Reset to [DashboardFilter()] to clear all filters.
final dashboardFilterProvider =
    StateProvider<DashboardFilter>((ref) => const DashboardFilter());

// ─── Agent self-identity ──────────────────────────────────────────────────────

/// The identity of this running Claude Code agent, read from the session
/// context file written by the claim-task skill.
class AgentSelfContext {
  const AgentSelfContext({
    required this.agentId,
    this.sessionId,
    this.repoPath,
    this.taskId,
  });

  final String agentId;
  final String? sessionId;
  final String? repoPath;
  final String? taskId;

  factory AgentSelfContext.fromJson(Map<String, dynamic> json) =>
      AgentSelfContext(
        agentId: json['agent_id'] as String? ??
            json['agentId'] as String? ??
            'unknown',
        sessionId: json['session_id'] as String? ?? json['sessionId'] as String?,
        repoPath: json['repo_path'] as String? ?? json['repoPath'] as String?,
        taskId: json['task_id'] as String? ?? json['taskId'] as String?,
      );

  AgentSelfContext withTaskId(String? id) => AgentSelfContext(
        agentId: agentId,
        sessionId: sessionId,
        repoPath: repoPath,
        taskId: id,
      );
}

/// Reads `.claude/temp/.session-context.json` from [repoPath] to determine
/// this agent's identity. Returns null if file missing or unreadable.
///
/// Apps can also set this via [agentSelfOverrideProvider] to avoid file I/O.
final agentSelfProvider =
    FutureProvider.family<AgentSelfContext?, String?>((ref, repoPath) async {
  // Allow desktop layer to inject context without file reads.
  final override = ref.watch(agentSelfOverrideProvider);
  if (override != null) return override;

  if (repoPath == null) return null;

  try {
    final file = File('$repoPath/.claude/temp/.session-context.json');
    if (!file.existsSync()) return null;
    final raw = await file.readAsString();
    final json = jsonDecode(raw) as Map<String, dynamic>;
    return AgentSelfContext.fromJson(json);
  } catch (_) {
    return null;
  }
});

/// Override for [agentSelfProvider]. Desktop injects this after reading the
/// session context file at startup. Mobile leaves it null (uses file-based read).
final agentSelfOverrideProvider = StateProvider<AgentSelfContext?>((ref) => null);
