import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'daemon_provider.dart';

/// Summary of changes made during a task — used by TaskDiffReview and CostDashboard.
class TaskChangeSummary {
  final String taskId;
  final int filesChanged;
  final int linesAdded;
  final int linesRemoved;
  final int testsRun;
  final int testsPassed;
  final List<String> riskFlags;
  final double costUsdEst;
  final int tokensUsed;

  const TaskChangeSummary({
    required this.taskId,
    required this.filesChanged,
    required this.linesAdded,
    required this.linesRemoved,
    required this.testsRun,
    required this.testsPassed,
    required this.riskFlags,
    required this.costUsdEst,
    required this.tokensUsed,
  });

  factory TaskChangeSummary.fromJson(Map<String, dynamic> json) =>
      TaskChangeSummary(
        // H9: Use null-safe fallback chain so that if both keys are absent
        // (e.g. daemon sends an unexpected shape) the parse does not throw a
        // null-cast exception. Falls back to empty string.
        taskId: json['task_id'] as String? ?? json['taskId'] as String? ?? '',
        filesChanged: (json['files_changed'] as num?)?.toInt() ??
            (json['filesChanged'] as num?)?.toInt() ??
            0,
        linesAdded: (json['lines_added'] as num?)?.toInt() ??
            (json['linesAdded'] as num?)?.toInt() ??
            0,
        linesRemoved: (json['lines_removed'] as num?)?.toInt() ??
            (json['linesRemoved'] as num?)?.toInt() ??
            0,
        testsRun: (json['tests_run'] as num?)?.toInt() ??
            (json['testsRun'] as num?)?.toInt() ??
            0,
        testsPassed: (json['tests_passed'] as num?)?.toInt() ??
            (json['testsPassed'] as num?)?.toInt() ??
            0,
        riskFlags: (json['risk_flags'] as List<dynamic>?)
                ?.map((e) => e.toString())
                .toList() ??
            [],
        costUsdEst: (json['cost_usd_est'] as num?)?.toDouble() ??
            (json['costUsdEst'] as num?)?.toDouble() ??
            0.0,
        tokensUsed: (json['tokens_used'] as num?)?.toInt() ??
            (json['tokensUsed'] as num?)?.toInt() ??
            0,
      );

  /// Empty summary returned when the daemon doesn't support traces.summary yet.
  factory TaskChangeSummary.empty(String taskId) => TaskChangeSummary(
        taskId: taskId,
        filesChanged: 0,
        linesAdded: 0,
        linesRemoved: 0,
        testsRun: 0,
        testsPassed: 0,
        riskFlags: [],
        costUsdEst: 0.0,
        tokensUsed: 0,
      );
}

/// Fetches the change summary for a single task via `traces.summary`.
/// Returns [TaskChangeSummary.empty] if the RPC is unavailable.
final taskSummaryProvider =
    FutureProvider.family<TaskChangeSummary, String>((ref, taskId) async {
  final client = ref.read(daemonProvider.notifier).client;
  try {
    final result = await client.call<Map<String, dynamic>>(
      'traces.summary',
      {'task_id': taskId},
    );
    return TaskChangeSummary.fromJson(result);
  } catch (_) {
    return TaskChangeSummary.empty(taskId);
  }
});
