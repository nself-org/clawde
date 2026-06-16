/// Repository status types for git integration.

class RepoStatus {
  final String repoPath;
  final String? branch;
  final int ahead;
  final int behind;
  final bool hasConflicts;
  final String? lastUpdated;
  final List<FileStatus> files;

  const RepoStatus({
    required this.repoPath,
    this.branch,
    required this.ahead,
    required this.behind,
    required this.hasConflicts,
    this.lastUpdated,
    required this.files,
  });

  factory RepoStatus.fromJson(Map<String, dynamic> json) => RepoStatus(
        repoPath: json['repoPath'] as String,
        branch: json['branch'] as String?,
        ahead: json['ahead'] as int,
        behind: json['behind'] as int,
        hasConflicts: json['hasConflicts'] as bool? ?? false,
        lastUpdated: json['lastUpdated'] as String?,
        files: (json['files'] as List<dynamic>)
            .map((f) => FileStatus.fromJson(f as Map<String, dynamic>))
            .toList(),
      );
}

/// Matches the daemon's FileStatusKind enum (serde lowercase).
enum FileState {
  clean,
  modified,
  staged,
  deleted,
  untracked,
  conflict;

  static FileState fromString(String s) =>
      FileState.values.asNameMap()[s] ?? FileState.untracked;
}

class FileStatus {
  final String path;
  final FileState state;
  final String? oldPath;

  const FileStatus({
    required this.path,
    required this.state,
    this.oldPath,
  });

  factory FileStatus.fromJson(Map<String, dynamic> json) => FileStatus(
        path: json['path'] as String,
        // Daemon sends 'status' key with lowercase enum value (e.g. "modified")
        state: FileState.fromString(json['status'] as String? ?? 'untracked'),
        oldPath: json['oldPath'] as String?,
      );
}
