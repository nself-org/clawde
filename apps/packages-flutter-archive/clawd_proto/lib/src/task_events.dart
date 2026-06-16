import 'agent_activity.dart';
import 'agent_task.dart';

/// Push event: a task was claimed by an agent.
class TaskClaimedEvent {
  const TaskClaimedEvent({
    required this.taskId,
    required this.agentId,
    this.isResume = false,
  });

  final String taskId;
  final String agentId;
  final bool isResume;

  factory TaskClaimedEvent.fromJson(Map<String, dynamic> json) =>
      TaskClaimedEvent(
        taskId:
            json['task_id'] as String? ?? json['taskId'] as String? ?? '',
        agentId:
            json['agent_id'] as String? ?? json['agentId'] as String? ?? '',
        isResume: json['is_resume'] as bool? ?? false,
      );
}

/// Push event: a task status changed.
class TaskStatusChangedEvent {
  const TaskStatusChangedEvent({
    required this.taskId,
    required this.status,
    this.notes,
  });

  final String taskId;
  final TaskStatus status;
  final String? notes;

  factory TaskStatusChangedEvent.fromJson(Map<String, dynamic> json) =>
      TaskStatusChangedEvent(
        taskId:
            json['task_id'] as String? ?? json['taskId'] as String? ?? '',
        status: TaskStatus.fromString(
            json['status'] as String? ?? 'pending'),
        notes: json['notes'] as String?,
      );
}

/// Push event: an activity entry was logged.
class TaskActivityLoggedEvent {
  const TaskActivityLoggedEvent({required this.entry});

  final ActivityLogEntry entry;

  factory TaskActivityLoggedEvent.fromJson(Map<String, dynamic> json) =>
      TaskActivityLoggedEvent(
        entry: ActivityLogEntry.fromJson(
          json['entry'] as Map<String, dynamic>? ?? json,
        ),
      );
}

/// Push event: a batch of activity entries (for high-volume protection).
class ActivityBatchEvent {
  const ActivityBatchEvent({required this.entries});

  final List<ActivityLogEntry> entries;

  factory ActivityBatchEvent.fromJson(Map<String, dynamic> json) {
    final raw = json['entries'] as List? ?? [];
    return ActivityBatchEvent(
      entries: raw
          .map((e) =>
              ActivityLogEntry.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// Push event: an agent connected to the daemon.
class AgentConnectedEvent {
  const AgentConnectedEvent({
    required this.agentId,
    this.sessionId,
    this.projectPath,
  });

  final String agentId;
  final String? sessionId;
  final String? projectPath;

  factory AgentConnectedEvent.fromJson(Map<String, dynamic> json) =>
      AgentConnectedEvent(
        agentId:
            json['agentId'] as String? ?? json['agent_id'] as String? ?? '',
        sessionId: json['sessionId'] as String? ??
            json['session_id'] as String?,
        projectPath: json['projectPath'] as String? ??
            json['project_path'] as String?,
      );
}

/// Push event: an agent was assigned to a task.
class AgentAssignedEvent {
  const AgentAssignedEvent({
    required this.agentId,
    required this.taskId,
  });

  final String agentId;
  final String taskId;

  factory AgentAssignedEvent.fromJson(Map<String, dynamic> json) =>
      AgentAssignedEvent(
        agentId:
            json['agentId'] as String? ?? json['agent_id'] as String? ?? '',
        taskId:
            json['taskId'] as String? ?? json['task_id'] as String? ?? '',
      );
}

/// Push event: a task was interrupted (heartbeat timeout).
class TaskInterruptedEvent {
  const TaskInterruptedEvent({required this.taskId, this.agentId});

  final String taskId;
  final String? agentId;

  factory TaskInterruptedEvent.fromJson(Map<String, dynamic> json) =>
      TaskInterruptedEvent(
        taskId:
            json['taskId'] as String? ?? json['task_id'] as String? ?? '',
        agentId:
            json['agentId'] as String? ?? json['agent_id'] as String?,
      );
}

/// Push event: AFS watcher synced active.md to the task DB.
class AfsActiveMdSyncedEvent {
  const AfsActiveMdSyncedEvent({
    required this.repoPath,
    required this.imported,
  });

  final String repoPath;

  /// Number of new tasks imported from active.md.
  final int imported;

  factory AfsActiveMdSyncedEvent.fromJson(Map<String, dynamic> json) =>
      AfsActiveMdSyncedEvent(
        repoPath:
            json['repoPath'] as String? ?? json['repo_path'] as String? ?? '',
        imported: json['imported'] as int? ?? 0,
      );
}

/// Push event: a file in .claude/planning/ was updated.
class AfsPlanningUpdatedEvent {
  const AfsPlanningUpdatedEvent({
    required this.repoPath,
    required this.file,
  });

  final String repoPath;

  /// Relative path of the changed planning file.
  final String file;

  factory AfsPlanningUpdatedEvent.fromJson(Map<String, dynamic> json) =>
      AfsPlanningUpdatedEvent(
        repoPath:
            json['repoPath'] as String? ?? json['repo_path'] as String? ?? '',
        file: json['file'] as String? ?? '',
      );
}

/// Push event: new inbox message arrived via AFS watcher.
class InboxMessageReceivedEvent {
  const InboxMessageReceivedEvent({
    required this.repoPath,
    required this.filePath,
    required this.filename,
  });

  final String repoPath;
  final String filePath;
  final String filename;

  factory InboxMessageReceivedEvent.fromJson(Map<String, dynamic> json) =>
      InboxMessageReceivedEvent(
        repoPath:
            json['repoPath'] as String? ?? json['repo_path'] as String? ?? '',
        filePath:
            json['filePath'] as String? ?? json['file_path'] as String? ?? '',
        filename: json['filename'] as String? ?? '',
      );
}
