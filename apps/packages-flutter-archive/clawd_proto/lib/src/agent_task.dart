import 'dart:convert';

/// Status of an agent task.
enum TaskStatus {
  pending,
  planned,
  claimed,
  active,
  inProgress,
  needsApproval,
  codeReview,
  done,
  blocked,
  canceled,
  failed,
  deferred,
  interrupted,
  inQa;

  static TaskStatus fromString(String s) {
    switch (s) {
      case 'pending':
        return TaskStatus.pending;
      case 'planned':
        return TaskStatus.planned;
      case 'claimed':
        return TaskStatus.claimed;
      case 'active':
        return TaskStatus.active;
      case 'in_progress':
        return TaskStatus.inProgress;
      case 'needs_approval':
        return TaskStatus.needsApproval;
      case 'code_review':
        return TaskStatus.codeReview;
      case 'done':
        return TaskStatus.done;
      case 'blocked':
        return TaskStatus.blocked;
      case 'canceled':
        return TaskStatus.canceled;
      case 'failed':
        return TaskStatus.failed;
      case 'deferred':
        return TaskStatus.deferred;
      case 'interrupted':
        return TaskStatus.interrupted;
      case 'in_qa':
        return TaskStatus.inQa;
      default:
        return TaskStatus.pending;
    }
  }

  String toJsonStr() {
    switch (this) {
      case TaskStatus.pending:
        return 'pending';
      case TaskStatus.planned:
        return 'planned';
      case TaskStatus.claimed:
        return 'claimed';
      case TaskStatus.active:
        return 'active';
      case TaskStatus.inProgress:
        return 'in_progress';
      case TaskStatus.needsApproval:
        return 'needs_approval';
      case TaskStatus.codeReview:
        return 'code_review';
      case TaskStatus.done:
        return 'done';
      case TaskStatus.blocked:
        return 'blocked';
      case TaskStatus.canceled:
        return 'canceled';
      case TaskStatus.failed:
        return 'failed';
      case TaskStatus.deferred:
        return 'deferred';
      case TaskStatus.interrupted:
        return 'interrupted';
      case TaskStatus.inQa:
        return 'in_qa';
    }
  }

  String get displayName {
    switch (this) {
      case TaskStatus.pending:
        return 'Pending';
      case TaskStatus.planned:
        return 'Planned';
      case TaskStatus.claimed:
        return 'Claimed';
      case TaskStatus.active:
        return 'Active';
      case TaskStatus.inProgress:
        return 'In Progress';
      case TaskStatus.needsApproval:
        return 'Needs Approval';
      case TaskStatus.codeReview:
        return 'Code Review';
      case TaskStatus.done:
        return 'Done';
      case TaskStatus.blocked:
        return 'Blocked';
      case TaskStatus.canceled:
        return 'Canceled';
      case TaskStatus.failed:
        return 'Failed';
      case TaskStatus.deferred:
        return 'Deferred';
      case TaskStatus.interrupted:
        return 'Interrupted';
      case TaskStatus.inQa:
        return 'In QA';
    }
  }
}

/// Type of an agent task.
enum TaskType {
  task,
  bug,
  feature,
  chore,
  docs,
  test,
  qa,
  research;

  static TaskType fromString(String s) {
    return TaskType.values.firstWhere(
      (t) => t.name == s,
      orElse: () => TaskType.task,
    );
  }

  String toJsonStr() => name;
}

/// Severity / priority of a task.
enum TaskSeverity {
  critical,
  high,
  medium,
  low;

  static TaskSeverity fromString(String s) {
    return TaskSeverity.values.firstWhere(
      (t) => t.name == s,
      orElse: () => TaskSeverity.medium,
    );
  }

  String toJsonStr() => name;
}

/// A tracked agent task — lives in the daemon DB and queue.json.
class AgentTask {
  const AgentTask({
    required this.id,
    required this.title,
    required this.status,
    this.description,
    this.taskType = TaskType.task,
    this.severity = TaskSeverity.medium,
    this.phase,
    this.agent,
    this.claimedBy,
    this.claimedAt,
    this.startedAt,
    this.completedAt,
    this.lastHeartbeat,
    this.file,
    this.files = const [],
    this.tags = const [],
    this.notes,
    this.blockReason,
    this.estimatedMinutes,
    this.actualMinutes,
    this.repoPath,
    this.createdAt,
    this.updatedAt,
  });

  final String id;
  final String title;
  final TaskStatus status;
  final String? description;
  final TaskType taskType;
  final TaskSeverity severity;
  final String? phase;
  final String? agent;
  final String? claimedBy;
  final String? claimedAt;
  final String? startedAt;
  final String? completedAt;
  final String? lastHeartbeat;
  final String? file;
  final List<String> files;
  final List<String> tags;
  final String? notes;
  final String? blockReason;
  final int? estimatedMinutes;
  final int? actualMinutes;
  final String? repoPath;
  final String? createdAt;
  final String? updatedAt;

  factory AgentTask.fromJson(Map<String, dynamic> json) {
    List<String> parseStringList(dynamic v) {
      if (v == null) return [];
      if (v is List) return v.map((e) => e.toString()).toList();
      if (v is String) {
        try {
          final decoded = jsonDecode(v);
          if (decoded is List) return decoded.map((e) => e.toString()).toList();
        } catch (_) {}
      }
      return [];
    }

    return AgentTask(
      id: json['id'] as String,
      title: json['title'] as String,
      status: TaskStatus.fromString(json['status'] as String? ?? 'pending'),
      description: json['description'] as String?,
      taskType: TaskType.fromString(json['type'] as String? ?? 'task'),
      severity:
          TaskSeverity.fromString(json['severity'] as String? ?? 'medium'),
      phase: json['phase'] as String?,
      agent: json['agent'] as String?,
      claimedBy:
          json['claimedBy'] as String? ?? json['claimed_by'] as String?,
      claimedAt:
          json['claimedAt'] as String? ?? json['claimed_at'] as String?,
      startedAt:
          json['startedAt'] as String? ?? json['started_at'] as String?,
      completedAt:
          json['completedAt'] as String? ?? json['completed_at'] as String?,
      lastHeartbeat: json['lastHeartbeat'] as String? ??
          json['last_heartbeat'] as String?,
      file: json['file'] as String?,
      files: parseStringList(json['files']),
      tags: parseStringList(json['tags']),
      notes: json['notes'] as String?,
      blockReason:
          json['blockReason'] as String? ?? json['block_reason'] as String?,
      estimatedMinutes: json['estimatedMinutes'] as int? ??
          json['estimated_minutes'] as int?,
      actualMinutes:
          json['actualMinutes'] as int? ?? json['actual_minutes'] as int?,
      repoPath: json['repoPath'] as String? ?? json['repo_path'] as String?,
      createdAt:
          json['createdAt'] as String? ?? json['created_at'] as String?,
      updatedAt:
          json['updatedAt'] as String? ?? json['updated_at'] as String?,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'title': title,
        'status': status.toJsonStr(),
        if (description != null) 'description': description,
        'type': taskType.toJsonStr(),
        'severity': severity.toJsonStr(),
        if (phase != null) 'phase': phase,
        if (agent != null) 'agent': agent,
        if (claimedBy != null) 'claimedBy': claimedBy,
        if (claimedAt != null) 'claimedAt': claimedAt,
        if (startedAt != null) 'startedAt': startedAt,
        if (completedAt != null) 'completedAt': completedAt,
        if (lastHeartbeat != null) 'lastHeartbeat': lastHeartbeat,
        if (file != null) 'file': file,
        'files': files,
        'tags': tags,
        if (notes != null) 'notes': notes,
        if (blockReason != null) 'blockReason': blockReason,
        if (estimatedMinutes != null) 'estimatedMinutes': estimatedMinutes,
        if (actualMinutes != null) 'actualMinutes': actualMinutes,
        if (repoPath != null) 'repoPath': repoPath,
        if (createdAt != null) 'createdAt': createdAt,
        if (updatedAt != null) 'updatedAt': updatedAt,
      };

  AgentTask copyWith({
    TaskStatus? status,
    String? claimedBy,
    String? notes,
    String? blockReason,
    String? completedAt,
    String? lastHeartbeat,
  }) =>
      AgentTask(
        id: id,
        title: title,
        status: status ?? this.status,
        description: description,
        taskType: taskType,
        severity: severity,
        phase: phase,
        agent: agent,
        claimedBy: claimedBy ?? this.claimedBy,
        claimedAt: claimedAt,
        startedAt: startedAt,
        completedAt: completedAt ?? this.completedAt,
        lastHeartbeat: lastHeartbeat ?? this.lastHeartbeat,
        file: file,
        files: files,
        tags: tags,
        notes: notes ?? this.notes,
        blockReason: blockReason ?? this.blockReason,
        estimatedMinutes: estimatedMinutes,
        actualMinutes: actualMinutes,
        repoPath: repoPath,
        createdAt: createdAt,
        updatedAt: updatedAt,
      );
}
