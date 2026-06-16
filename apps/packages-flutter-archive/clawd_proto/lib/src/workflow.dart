/// Workflow recipe types for the clawd daemon.
///
/// Sprint DD WR.5 — workflow.create/list/run/delete RPC types.

// ─── Workflow Recipe ──────────────────────────────────────────────────────────

class WorkflowStep {
  final String prompt;
  final String provider;
  final String? inheritFrom;

  const WorkflowStep({
    required this.prompt,
    required this.provider,
    this.inheritFrom,
  });

  factory WorkflowStep.fromJson(Map<String, dynamic> json) => WorkflowStep(
        prompt: json['prompt'] as String? ?? '',
        provider: json['provider'] as String? ?? 'claude',
        inheritFrom: json['inherit_from'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'prompt': prompt,
        'provider': provider,
        if (inheritFrom != null) 'inherit_from': inheritFrom,
      };
}

class WorkflowRecipe {
  final String id;
  final String name;
  final String description;
  final List<String> tags;
  final bool isBuiltin;
  final int runCount;
  final String templateYaml;
  final DateTime createdAt;

  const WorkflowRecipe({
    required this.id,
    required this.name,
    required this.description,
    required this.tags,
    required this.isBuiltin,
    required this.runCount,
    required this.templateYaml,
    required this.createdAt,
  });

  factory WorkflowRecipe.fromJson(Map<String, dynamic> json) => WorkflowRecipe(
        id: json['id'] as String,
        name: json['name'] as String? ?? '',
        description: json['description'] as String? ?? '',
        tags: (json['tags'] as List?)?.cast<String>() ?? [],
        isBuiltin: json['isBuiltin'] as bool? ?? false,
        runCount: json['runCount'] as int? ?? 0,
        templateYaml: json['templateYaml'] as String? ?? '',
        createdAt: _parseTimestamp(json['createdAt']),
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'description': description,
        'tags': tags,
        'isBuiltin': isBuiltin,
        'runCount': runCount,
        'templateYaml': templateYaml,
        'createdAt': createdAt.toIso8601String(),
      };

  @override
  String toString() => 'WorkflowRecipe(id: $id, name: $name)';
}

// ─── Workflow Run ─────────────────────────────────────────────────────────────

enum WorkflowRunStatus {
  running,
  completed,
  failed,
  canceled;

  static WorkflowRunStatus fromString(String s) => switch (s) {
        'running' => running,
        'completed' => completed,
        'failed' => failed,
        'canceled' => canceled,
        _ => running,
      };
}

class WorkflowRun {
  final String id;
  final String recipeId;
  final WorkflowRunStatus status;
  final int currentStep;
  final int totalSteps;
  final String? outputJson;
  final DateTime startedAt;
  final DateTime? finishedAt;

  const WorkflowRun({
    required this.id,
    required this.recipeId,
    required this.status,
    required this.currentStep,
    required this.totalSteps,
    this.outputJson,
    required this.startedAt,
    this.finishedAt,
  });

  factory WorkflowRun.fromJson(Map<String, dynamic> json) => WorkflowRun(
        id: json['id'] as String,
        recipeId: json['recipeId'] as String? ?? '',
        status: WorkflowRunStatus.fromString(
            json['status'] as String? ?? 'running'),
        currentStep: json['currentStep'] as int? ?? 0,
        totalSteps: json['totalSteps'] as int? ?? 0,
        outputJson: json['outputJson'] as String?,
        startedAt: _parseTimestamp(json['startedAt']),
        finishedAt: json['finishedAt'] != null
            ? _parseTimestamp(json['finishedAt'])
            : null,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'recipeId': recipeId,
        'status': status.name,
        'currentStep': currentStep,
        'totalSteps': totalSteps,
        if (outputJson != null) 'outputJson': outputJson,
        'startedAt': startedAt.toIso8601String(),
        if (finishedAt != null) 'finishedAt': finishedAt!.toIso8601String(),
      };

  @override
  String toString() =>
      'WorkflowRun(id: $id, recipeId: $recipeId, status: $status)';
}

// ─── Push Events ──────────────────────────────────────────────────────────────

class WorkflowStepCompletedEvent {
  final String runId;
  final int stepIndex;
  final String sessionId;
  final String status;

  const WorkflowStepCompletedEvent({
    required this.runId,
    required this.stepIndex,
    required this.sessionId,
    required this.status,
  });

  factory WorkflowStepCompletedEvent.fromJson(Map<String, dynamic> json) =>
      WorkflowStepCompletedEvent(
        runId: json['runId'] as String? ?? '',
        stepIndex: json['stepIndex'] as int? ?? 0,
        sessionId: json['sessionId'] as String? ?? '',
        status: json['status'] as String? ?? '',
      );
}

class WorkflowRanEvent {
  final String runId;
  final String recipeId;
  final String status;

  const WorkflowRanEvent({
    required this.runId,
    required this.recipeId,
    required this.status,
  });

  factory WorkflowRanEvent.fromJson(Map<String, dynamic> json) =>
      WorkflowRanEvent(
        runId: json['runId'] as String? ?? '',
        recipeId: json['recipeId'] as String? ?? '',
        status: json['status'] as String? ?? '',
      );
}

// ─── Internal ─────────────────────────────────────────────────────────────────

DateTime _parseTimestamp(dynamic raw) {
  if (raw is int) {
    return DateTime.fromMillisecondsSinceEpoch(raw * 1000, isUtc: true);
  }
  if (raw is String) {
    try {
      return DateTime.parse(raw);
    } catch (_) {}
  }
  return DateTime.fromMillisecondsSinceEpoch(0, isUtc: true);
}
