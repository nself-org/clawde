/// Push event types for task worktree lifecycle events.
/// Emitted by the daemon when worktrees are created, accepted, or rejected.

/// Emitted by `worktrees.create` — a new git worktree was created for a task.
class WorktreeCreatedEvent {
  final String taskId;
  final String worktreePath;
  final String branch;

  const WorktreeCreatedEvent({
    required this.taskId,
    required this.worktreePath,
    required this.branch,
  });

  factory WorktreeCreatedEvent.fromJson(Map<String, dynamic> json) =>
      WorktreeCreatedEvent(
        taskId: json['taskId'] as String? ?? json['task_id'] as String? ?? '',
        worktreePath: json['worktreePath'] as String? ??
            json['worktree_path'] as String? ??
            '',
        branch: json['branch'] as String? ?? '',
      );
}

/// Emitted by `worktrees.accept` — worktree changes were merged into main.
class WorktreeAcceptedEvent {
  final String taskId;
  final String branch;

  const WorktreeAcceptedEvent({
    required this.taskId,
    required this.branch,
  });

  factory WorktreeAcceptedEvent.fromJson(Map<String, dynamic> json) =>
      WorktreeAcceptedEvent(
        taskId: json['taskId'] as String? ?? json['task_id'] as String? ?? '',
        branch: json['branch'] as String? ?? '',
      );
}

/// Emitted by `worktrees.reject` — worktree changes were discarded.
class WorktreeRejectedEvent {
  final String taskId;
  final String branch;

  const WorktreeRejectedEvent({
    required this.taskId,
    required this.branch,
  });

  factory WorktreeRejectedEvent.fromJson(Map<String, dynamic> json) =>
      WorktreeRejectedEvent(
        taskId: json['taskId'] as String? ?? json['task_id'] as String? ?? '',
        branch: json['branch'] as String? ?? '',
      );
}
