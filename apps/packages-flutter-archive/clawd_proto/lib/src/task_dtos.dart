/// DTOs for task queue RPC calls.

/// Parameters for listing tasks.
class TaskListParams {
  const TaskListParams({
    this.status,
    this.taskType,
    this.agent,
    this.severity,
    this.phase,
    this.tag,
    this.search,
    this.repoPath,
    this.limit,
    this.offset,
  });

  final String? status;
  final String? taskType;
  final String? agent;
  final String? severity;
  final String? phase;
  final String? tag;
  final String? search;
  final String? repoPath;
  final int? limit;
  final int? offset;

  Map<String, dynamic> toJson() => {
        if (status != null) 'status': status,
        if (taskType != null) 'type': taskType,
        if (agent != null) 'agent': agent,
        if (severity != null) 'severity': severity,
        if (phase != null) 'phase': phase,
        if (tag != null) 'tag': tag,
        if (search != null) 'search': search,
        if (repoPath != null) 'repoPath': repoPath,
        if (limit != null) 'limit': limit,
        if (offset != null) 'offset': offset,
      };
}

/// Parameters for querying the activity log.
class ActivityQueryParams {
  const ActivityQueryParams({
    this.repoPath,
    this.taskId,
    this.agent,
    this.phase,
    this.entryType,
    this.action,
    this.since,
    this.limit = 100,
    this.offset = 0,
  });

  final String? repoPath;
  final String? taskId;
  final String? agent;
  final String? phase;
  final String? entryType;
  final String? action;
  final int? since;
  final int limit;
  final int offset;

  Map<String, dynamic> toJson() => {
        if (repoPath != null) 'repoPath': repoPath,
        if (taskId != null) 'taskId': taskId,
        if (agent != null) 'agent': agent,
        if (phase != null) 'phase': phase,
        if (entryType != null) 'entryType': entryType,
        if (action != null) 'action': action,
        if (since != null) 'since': since,
        'limit': limit,
        'offset': offset,
      };
}

/// Specification for creating a new task.
class TaskSpec {
  const TaskSpec({
    required this.id,
    required this.title,
    required this.repoPath,
    this.description,
    this.taskType = 'task',
    this.severity = 'medium',
    this.phase,
    this.file,
    this.files = const [],
    this.tags = const [],
    this.estimatedMinutes,
  });

  final String id;
  final String title;
  final String repoPath;
  final String? description;
  final String taskType;
  final String severity;
  final String? phase;
  final String? file;
  final List<String> files;
  final List<String> tags;
  final int? estimatedMinutes;

  Map<String, dynamic> toJson() => {
        'id': id,
        'title': title,
        'repo_path': repoPath,
        if (description != null) 'description': description,
        'type': taskType,
        'severity': severity,
        if (phase != null) 'phase': phase,
        if (file != null) 'file': file,
        if (files.isNotEmpty) 'files': files,
        if (tags.isNotEmpty) 'tags': tags,
        if (estimatedMinutes != null) 'estimated_minutes': estimatedMinutes,
      };
}

/// Summary stats returned by tasks.summary.
class TaskSummary {
  const TaskSummary({
    required this.total,
    required this.byStatus,
    required this.byAgent,
    this.avgDurationMinutes,
    this.repoPath,
  });

  final int total;
  final Map<String, int> byStatus;
  final Map<String, int> byAgent;
  final double? avgDurationMinutes;
  final String? repoPath;

  factory TaskSummary.fromJson(Map<String, dynamic> json) {
    final byStatus = <String, int>{};
    if (json['byStatus'] is Map) {
      (json['byStatus'] as Map).forEach((k, v) {
        byStatus[k.toString()] = (v as num).toInt();
      });
    }
    final byAgent = <String, int>{};
    if (json['byAgent'] is Map) {
      (json['byAgent'] as Map).forEach((k, v) {
        byAgent[k.toString()] = (v as num).toInt();
      });
    }
    return TaskSummary(
      total: (json['total'] as num?)?.toInt() ?? 0,
      byStatus: byStatus,
      byAgent: byAgent,
      avgDurationMinutes: (json['avgDurationMinutes'] as num?)?.toDouble(),
      repoPath: json['repoPath'] as String?,
    );
  }
}
