/// Full worktree record returned by `worktrees.create` and `worktrees.list`.
class WorktreeInfo {
  final String taskId;
  final String worktreePath;
  final String branch;
  final String repoPath;
  final String status; // active | done | abandoned | merged
  final String createdAt;

  const WorktreeInfo({
    required this.taskId,
    required this.worktreePath,
    required this.branch,
    required this.repoPath,
    required this.status,
    required this.createdAt,
  });

  factory WorktreeInfo.fromJson(Map<String, dynamic> json) => WorktreeInfo(
        taskId: json['task_id'] as String? ?? json['taskId'] as String? ?? '',
        worktreePath: json['worktree_path'] as String? ??
            json['worktreePath'] as String? ??
            '',
        branch: json['branch'] as String? ?? '',
        repoPath: json['repo_path'] as String? ??
            json['repoPath'] as String? ??
            '',
        status: json['status'] as String? ?? 'active',
        createdAt: json['created_at'] as String? ??
            json['createdAt'] as String? ??
            '',
      );

  Map<String, dynamic> toJson() => {
        'task_id': taskId,
        'worktree_path': worktreePath,
        'branch': branch,
        'repo_path': repoPath,
        'status': status,
        'created_at': createdAt,
      };
}

/// Worktree diff stats returned by `worktrees.diff`.
class WorktreeDiff {
  final String taskId;
  final String diff;
  final int filesChanged;
  final int insertions;
  final int deletions;

  const WorktreeDiff({
    required this.taskId,
    required this.diff,
    required this.filesChanged,
    required this.insertions,
    required this.deletions,
  });

  factory WorktreeDiff.fromJson(Map<String, dynamic> json) {
    final stats = json['stats'] as Map<String, dynamic>? ?? {};
    return WorktreeDiff(
      taskId: json['task_id'] as String? ?? '',
      diff: json['diff'] as String? ?? '',
      filesChanged: (stats['files_changed'] as num?)?.toInt() ?? 0,
      insertions: (stats['insertions'] as num?)?.toInt() ?? 0,
      deletions: (stats['deletions'] as num?)?.toInt() ?? 0,
    );
  }
}

/// Legacy worktree status used by the task diff review UI.
class WorktreeStatus {
  final String taskId;
  final String branch;
  final String path;
  final int changeCount;
  final bool pendingMerge;
  final bool isStale;

  const WorktreeStatus({
    required this.taskId,
    required this.branch,
    required this.path,
    required this.changeCount,
    required this.pendingMerge,
    required this.isStale,
  });

  factory WorktreeStatus.fromJson(Map<String, dynamic> json) => WorktreeStatus(
        taskId: json['task_id'] as String? ?? json['taskId'] as String? ?? '',
        branch: json['branch'] as String? ?? '',
        path: json['path'] as String? ??
            json['worktree_path'] as String? ??
            json['worktreePath'] as String? ??
            '',
        changeCount: (json['change_count'] as num?)?.toInt() ??
            (json['changeCount'] as num?)?.toInt() ??
            0,
        pendingMerge: json['pending_merge'] as bool? ??
            json['pendingMerge'] as bool? ??
            false,
        isStale: json['is_stale'] as bool? ?? json['isStale'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'task_id': taskId,
        'branch': branch,
        'path': path,
        'change_count': changeCount,
        'pending_merge': pendingMerge,
        'is_stale': isStale,
      };
}
