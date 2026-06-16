// SPDX-License-Identifier: MIT
/// Sprint ZZ IG — Instruction graph protocol types.
///
/// Mirrors the `instructions.*` RPC responses from the daemon.
library;

// ─── Instruction node ─────────────────────────────────────────────────────────

/// A single instruction node in the scope tree.
class InstructionNode {
  const InstructionNode({
    required this.id,
    required this.scope,
    required this.owner,
    required this.priority,
    required this.preview,
  });

  final String id;

  /// Filesystem scope glob this node applies to (e.g. `"src/**"`, `"*"`).
  final String scope;

  /// Owner — usually a provider name (`"claude"`, `"codex"`, `"global"`).
  final String owner;

  /// Merge priority (higher = applied later / wins conflicts).
  final int priority;

  /// First 120 characters of the node's instruction content.
  final String preview;

  factory InstructionNode.fromJson(Map<String, dynamic> j) => InstructionNode(
        id: j['id'] as String? ?? '',
        scope: j['scope'] as String? ?? '*',
        owner: j['owner'] as String? ?? 'global',
        priority: (j['priority'] as num?)?.toInt() ?? 0,
        preview: j['preview'] as String? ?? '',
      );
}

// ─── Explain result ───────────────────────────────────────────────────────────

/// Result of `instructions.explain(path)`.
class InstructionExplainResult {
  const InstructionExplainResult({
    required this.path,
    required this.nodes,
    required this.mergedPreview,
    required this.bytesUsed,
    required this.budgetBytes,
    required this.conflicts,
  });

  final String path;
  final List<InstructionNode> nodes;

  /// First 500 characters of the merged instruction for this path.
  final String mergedPreview;

  final int bytesUsed;
  final int budgetBytes;

  /// Conflict descriptions (owner A vs owner B on same key).
  final List<String> conflicts;

  /// Percentage of budget consumed (0–100+).
  int get usedPct => budgetBytes > 0 ? (bytesUsed * 100 ~/ budgetBytes) : 0;

  factory InstructionExplainResult.fromJson(Map<String, dynamic> j) {
    final rawNodes = j['nodes'] as List<dynamic>? ?? [];
    final rawConflicts = j['conflicts'] as List<dynamic>? ?? [];
    return InstructionExplainResult(
      path: j['path'] as String? ?? '.',
      nodes: rawNodes
          .map((n) => InstructionNode.fromJson(n as Map<String, dynamic>))
          .toList(),
      mergedPreview: j['merged_preview'] as String? ?? '',
      bytesUsed: (j['bytes_used'] as num?)?.toInt() ?? 0,
      budgetBytes: (j['budget_bytes'] as num?)?.toInt() ?? 8192,
      conflicts: rawConflicts.map((c) => c.toString()).toList(),
    );
  }
}

// ─── Budget report ────────────────────────────────────────────────────────────

/// Per-provider budget entry in `instructions.budgetReport`.
class InstructionBudget {
  const InstructionBudget({
    required this.bytesUsed,
    required this.budgetBytes,
    required this.pct,
    required this.overBudget,
  });

  final int bytesUsed;
  final int budgetBytes;
  final int pct;
  final bool overBudget;

  /// True when within 80–100 % of the budget.
  bool get nearBudget => pct >= 80 && !overBudget;

  factory InstructionBudget.fromJson(Map<String, dynamic> j) =>
      InstructionBudget(
        bytesUsed: (j['bytes_used'] as num?)?.toInt() ?? 0,
        budgetBytes: (j['budget_bytes'] as num?)?.toInt() ?? 8192,
        pct: (j['pct'] as num?)?.toInt() ?? 0,
        overBudget: j['over_budget'] as bool? ?? false,
      );
}

/// Result of `instructions.budgetReport(project_path)`.
class InstructionBudgetReport {
  const InstructionBudgetReport({required this.claude, required this.codex});

  final InstructionBudget claude;
  final InstructionBudget codex;

  factory InstructionBudgetReport.fromJson(Map<String, dynamic> j) =>
      InstructionBudgetReport(
        claude: InstructionBudget.fromJson(
            j['claude'] as Map<String, dynamic>? ?? {}),
        codex: InstructionBudget.fromJson(
            j['codex'] as Map<String, dynamic>? ?? {}),
      );
}

// ─── Lint report ─────────────────────────────────────────────────────────────

/// A single lint issue (error or warning).
class InstructionLintIssue {
  const InstructionLintIssue({
    required this.severity,
    required this.rule,
    required this.message,
    required this.nodeIds,
  });

  /// `"error"` or `"warning"`.
  final String severity;
  final String rule;
  final String message;
  final List<String> nodeIds;

  bool get isError => severity == 'error';

  factory InstructionLintIssue.fromJson(Map<String, dynamic> j) {
    final rawIds = j['node_ids'] as List<dynamic>? ?? [];
    return InstructionLintIssue(
      severity: j['severity'] as String? ?? 'warning',
      rule: j['rule'] as String? ?? '',
      message: j['message'] as String? ?? '',
      nodeIds: rawIds.map((e) => e.toString()).toList(),
    );
  }
}

/// Result of `instructions.lint(project_path)`.
class InstructionLintReport {
  const InstructionLintReport({
    required this.passed,
    required this.errors,
    required this.warnings,
    required this.issues,
  });

  final bool passed;
  final int errors;
  final int warnings;
  final List<InstructionLintIssue> issues;

  factory InstructionLintReport.fromJson(Map<String, dynamic> j) {
    final rawIssues = j['issues'] as List<dynamic>? ?? [];
    return InstructionLintReport(
      passed: j['passed'] as bool? ?? true,
      errors: (j['errors'] as num?)?.toInt() ?? 0,
      warnings: (j['warnings'] as num?)?.toInt() ?? 0,
      issues: rawIssues
          .map((i) =>
              InstructionLintIssue.fromJson(i as Map<String, dynamic>))
          .toList(),
    );
  }
}
