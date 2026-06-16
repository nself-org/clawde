/// CI runner types for the clawd daemon.
///
/// Sprint EE CI.7 — ci.run/status/cancel RPC types.

// ─── CI Status ────────────────────────────────────────────────────────────────

enum CiStatus {
  running,
  success,
  failure,
  canceled;

  static CiStatus fromString(String s) => switch (s) {
        'running' => running,
        'success' => success,
        'failure' => failure,
        'canceled' => canceled,
        _ => running,
      };

  bool get isTerminal => this != running;
  bool get succeeded => this == success;
}

// ─── CI Step ──────────────────────────────────────────────────────────────────

class CiStep {
  final String name;
  final String? task;
  final String? command;
  final int timeoutS;
  final bool continueOnError;

  const CiStep({
    required this.name,
    this.task,
    this.command,
    this.timeoutS = 300,
    this.continueOnError = false,
  });

  factory CiStep.fromJson(Map<String, dynamic> json) => CiStep(
        name: json['name'] as String? ?? '',
        task: json['task'] as String?,
        command: json['command'] as String?,
        timeoutS: json['timeout_s'] as int? ?? 300,
        continueOnError: json['continue_on_error'] as bool? ?? false,
      );
}

// ─── CI Step Result ───────────────────────────────────────────────────────────

class CiStepResult {
  final int stepIndex;
  final String stepName;
  final String status;
  final String output;
  final int durationMs;

  const CiStepResult({
    required this.stepIndex,
    required this.stepName,
    required this.status,
    required this.output,
    required this.durationMs,
  });

  factory CiStepResult.fromJson(Map<String, dynamic> json) => CiStepResult(
        stepIndex: json['stepIndex'] as int? ?? 0,
        stepName: json['stepName'] as String? ?? '',
        status: json['status'] as String? ?? '',
        output: json['output'] as String? ?? '',
        durationMs: json['durationMs'] as int? ?? 0,
      );

  bool get succeeded => status == 'success';
}

// ─── CI Run ───────────────────────────────────────────────────────────────────

class CiRun {
  final String runId;
  final CiStatus status;
  final List<CiStepResult> steps;

  const CiRun({
    required this.runId,
    required this.status,
    required this.steps,
  });

  factory CiRun.fromJson(Map<String, dynamic> json) => CiRun(
        runId: json['runId'] as String? ?? '',
        status: CiStatus.fromString(json['status'] as String? ?? 'running'),
        steps: (json['steps'] as List?)
                ?.map((s) =>
                    CiStepResult.fromJson(s as Map<String, dynamic>))
                .toList() ??
            [],
      );

  @override
  String toString() =>
      'CiRun(runId: $runId, status: $status, steps: ${steps.length})';
}

// ─── Push Events ──────────────────────────────────────────────────────────────

class CiStepStartedEvent {
  final String runId;
  final int stepIndex;
  final String stepName;
  final int totalSteps;

  const CiStepStartedEvent({
    required this.runId,
    required this.stepIndex,
    required this.stepName,
    required this.totalSteps,
  });

  factory CiStepStartedEvent.fromJson(Map<String, dynamic> json) =>
      CiStepStartedEvent(
        runId: json['runId'] as String? ?? '',
        stepIndex: json['stepIndex'] as int? ?? 0,
        stepName: json['stepName'] as String? ?? '',
        totalSteps: json['totalSteps'] as int? ?? 0,
      );
}

class CiCompleteEvent {
  final String runId;
  final String status;
  final int stepsRun;
  final int totalSteps;

  const CiCompleteEvent({
    required this.runId,
    required this.status,
    required this.stepsRun,
    required this.totalSteps,
  });

  factory CiCompleteEvent.fromJson(Map<String, dynamic> json) =>
      CiCompleteEvent(
        runId: json['runId'] as String? ?? '',
        status: json['status'] as String? ?? '',
        stepsRun: json['stepsRun'] as int? ?? 0,
        totalSteps: json['totalSteps'] as int? ?? 0,
      );
}
