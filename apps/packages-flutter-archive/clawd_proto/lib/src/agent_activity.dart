/// Type of an activity log entry.
enum ActivityEntryType {
  auto,
  note,
  system;

  static ActivityEntryType fromString(String s) {
    return ActivityEntryType.values.firstWhere(
      (t) => t.name == s,
      orElse: () => ActivityEntryType.auto,
    );
  }

  String toJsonStr() => name;
}

/// A single entry in the agent activity log.
class ActivityLogEntry {
  const ActivityLogEntry({
    required this.id,
    required this.agent,
    required this.action,
    required this.entryType,
    required this.ts,
    this.taskId,
    this.phase,
    this.detail,
    this.repoPath,
  });

  final String id;
  final String? taskId;
  final String agent;
  final String action;
  final String? detail;
  final ActivityEntryType entryType;
  final String? phase;

  /// Unix timestamp (seconds).
  final int ts;
  final String? repoPath;

  factory ActivityLogEntry.fromJson(Map<String, dynamic> json) =>
      ActivityLogEntry(
        id: json['id'] as String? ?? '',
        taskId: json['taskId'] as String? ?? json['task_id'] as String?,
        agent: json['agent'] as String? ?? '',
        action: json['action'] as String? ?? '',
        detail: json['detail'] as String?,
        entryType: ActivityEntryType.fromString(
          json['entryType'] as String? ??
              json['entry_type'] as String? ??
              'auto',
        ),
        phase: json['phase'] as String?,
        ts: (json['ts'] as num).toInt(),
        repoPath:
            json['repoPath'] as String? ?? json['repo_path'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        if (taskId != null) 'taskId': taskId,
        'agent': agent,
        'action': action,
        if (detail != null) 'detail': detail,
        'entryType': entryType.toJsonStr(),
        if (phase != null) 'phase': phase,
        'ts': ts,
        if (repoPath != null) 'repoPath': repoPath,
      };
}

/// Status of a registered agent.
enum AgentViewStatus {
  active,
  idle,
  offline;

  static AgentViewStatus fromString(String s) {
    return AgentViewStatus.values.firstWhere(
      (t) => t.name == s,
      orElse: () => AgentViewStatus.offline,
    );
  }

  String toJsonStr() => name;
}

/// A registered agent as seen by the dashboard.
class AgentView {
  const AgentView({
    required this.agentId,
    required this.status,
    required this.repoPath,
    this.agentType = 'claude',
    this.sessionId,
    this.currentTaskId,
    this.lastSeen,
    this.connectedAt,
    this.projectPath,
  });

  final String agentId;
  final AgentViewStatus status;
  final String repoPath;
  final String agentType;
  final String? sessionId;
  final String? currentTaskId;

  /// Unix timestamp (seconds).
  final int? lastSeen;

  /// Unix timestamp (seconds).
  final int? connectedAt;
  final String? projectPath;

  String get displayName {
    const maxLen = 20;
    if (agentId.length <= maxLen) return agentId;
    return '${agentId.substring(0, maxLen)}\u2026';
  }

  factory AgentView.fromJson(Map<String, dynamic> json) => AgentView(
        agentId: json['agentId'] as String? ?? json['agent_id'] as String? ?? '',
        status: AgentViewStatus.fromString(json['status'] as String? ?? 'offline'),
        repoPath: json['repoPath'] as String? ??
            json['repo_path'] as String? ??
            '',
        agentType: json['agentType'] as String? ??
            json['agent_type'] as String? ??
            'claude',
        sessionId:
            json['sessionId'] as String? ?? json['session_id'] as String?,
        currentTaskId: json['currentTaskId'] as String? ??
            json['current_task_id'] as String?,
        lastSeen: (json['lastSeen'] as num?)?.toInt() ??
            (json['last_seen'] as num?)?.toInt(),
        connectedAt: (json['connectedAt'] as num?)?.toInt() ??
            (json['connected_at'] as num?)?.toInt(),
        projectPath: json['projectPath'] as String? ??
            json['project_path'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'agentId': agentId,
        'agentType': agentType,
        'status': status.toJsonStr(),
        'repoPath': repoPath,
        if (sessionId != null) 'sessionId': sessionId,
        if (currentTaskId != null) 'currentTaskId': currentTaskId,
        if (lastSeen != null) 'lastSeen': lastSeen,
        if (connectedAt != null) 'connectedAt': connectedAt,
        if (projectPath != null) 'projectPath': projectPath,
      };

  AgentView copyWith({
    AgentViewStatus? status,
    String? currentTaskId,
    int? lastSeen,
  }) =>
      AgentView(
        agentId: agentId,
        status: status ?? this.status,
        repoPath: repoPath,
        agentType: agentType,
        sessionId: sessionId,
        currentTaskId: currentTaskId ?? this.currentTaskId,
        lastSeen: lastSeen ?? this.lastSeen,
        connectedAt: connectedAt,
        projectPath: projectPath,
      );
}
