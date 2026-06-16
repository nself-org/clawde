// SPDX-License-Identifier: MIT
/// Sprint ZZ EP — Evidence pack protocol types.
///
/// Mirrors the `artifacts.evidencePack` RPC response from the daemon.
library;

// ─── Diff stats ───────────────────────────────────────────────────────────────

/// Summary of file changes in a task's worktree.
class EvidenceDiffStats {
  const EvidenceDiffStats({
    required this.filesChanged,
    required this.insertions,
    required this.deletions,
    required this.files,
  });

  final int filesChanged;
  final int insertions;
  final int deletions;

  /// List of changed file paths.
  final List<String> files;

  factory EvidenceDiffStats.fromJson(Map<String, dynamic> j) {
    final rawFiles = j['files'] as List<dynamic>? ?? [];
    return EvidenceDiffStats(
      filesChanged: (j['files_changed'] as num?)?.toInt() ?? 0,
      insertions: (j['insertions'] as num?)?.toInt() ?? 0,
      deletions: (j['deletions'] as num?)?.toInt() ?? 0,
      files: rawFiles.map((f) => f.toString()).toList(),
    );
  }
}

// ─── Test results ─────────────────────────────────────────────────────────────

/// Aggregated test run results captured during the task.
class EvidenceTestResults {
  const EvidenceTestResults({
    required this.passed,
    required this.failed,
    required this.skipped,
    required this.durationMs,
    this.firstFailure,
  });

  final int passed;
  final int failed;
  final int skipped;
  final int durationMs;

  /// Short description of the first failing test, if any.
  final String? firstFailure;

  int get total => passed + failed + skipped;
  bool get allPassed => failed == 0;

  factory EvidenceTestResults.fromJson(Map<String, dynamic> j) =>
      EvidenceTestResults(
        passed: (j['passed'] as num?)?.toInt() ?? 0,
        failed: (j['failed'] as num?)?.toInt() ?? 0,
        skipped: (j['skipped'] as num?)?.toInt() ?? 0,
        durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
        firstFailure: j['first_failure'] as String?,
      );
}

// ─── Tool trace ───────────────────────────────────────────────────────────────

/// A single tool call recorded during a task.
class EvidenceToolTrace {
  const EvidenceToolTrace({
    required this.tool,
    required this.path,
    required this.decision,
    required this.durationMs,
  });

  /// Tool name (e.g. `"read_file"`, `"edit_file"`).
  final String tool;

  /// File or resource path the tool operated on.
  final String path;

  /// Outcome: `"allowed"`, `"blocked"`, `"warned"`.
  final String decision;

  final int durationMs;

  factory EvidenceToolTrace.fromJson(Map<String, dynamic> j) =>
      EvidenceToolTrace(
        tool: j['tool'] as String? ?? '',
        path: j['path'] as String? ?? '',
        decision: j['decision'] as String? ?? 'allowed',
        durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
      );
}

// ─── Evidence pack ────────────────────────────────────────────────────────────

/// Complete evidence pack for a completed task.
///
/// Returned by `artifacts.evidencePack(task_id)`.
class EvidencePack {
  const EvidencePack({
    required this.taskId,
    required this.runId,
    required this.instructionHash,
    required this.policyHash,
    required this.worktreeCommit,
    required this.diffStats,
    required this.testResults,
    required this.toolTrace,
    this.reviewerVerdict,
    required this.createdAt,
  });

  final String taskId;
  final String runId;
  final String instructionHash;
  final String policyHash;

  /// Git commit SHA of the worktree when the task completed.
  final String worktreeCommit;

  final EvidenceDiffStats diffStats;
  final EvidenceTestResults testResults;
  final List<EvidenceToolTrace> toolTrace;

  /// Overall verdict from the reviewer agent (or null if not reviewed).
  final String? reviewerVerdict;

  final String createdAt;

  factory EvidencePack.fromJson(Map<String, dynamic> j) {
    final rawTrace = j['tool_trace'] as List<dynamic>? ?? [];
    return EvidencePack(
      taskId: j['task_id'] as String? ?? '',
      runId: j['run_id'] as String? ?? '',
      instructionHash: j['instruction_hash'] as String? ?? '',
      policyHash: j['policy_hash'] as String? ?? '',
      worktreeCommit: j['worktree_commit'] as String? ?? '',
      diffStats: EvidenceDiffStats.fromJson(
          j['diff_stats'] as Map<String, dynamic>? ?? {}),
      testResults: EvidenceTestResults.fromJson(
          j['test_results'] as Map<String, dynamic>? ?? {}),
      toolTrace: rawTrace
          .map((t) =>
              EvidenceToolTrace.fromJson(t as Map<String, dynamic>))
          .toList(),
      reviewerVerdict: j['reviewer_verdict'] as String?,
      createdAt: j['created_at'] as String? ?? '',
    );
  }
}
