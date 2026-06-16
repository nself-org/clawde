/// Project types for the clawd daemon.
///
/// Mirrors the daemon project module — projects group repos and sessions.

import 'dart:developer' as dev;

class Project {
  final String id;
  final String name;
  final String? rootPath;
  final String? description;
  final String? orgSlug;
  final DateTime createdAt;
  final DateTime updatedAt;
  final DateTime? lastActiveAt;

  const Project({
    required this.id,
    required this.name,
    this.rootPath,
    this.description,
    this.orgSlug,
    required this.createdAt,
    required this.updatedAt,
    this.lastActiveAt,
  });

  factory Project.fromJson(Map<String, dynamic> json) {
    return Project(
      id: json['id'] as String,
      name: json['name'] as String,
      rootPath: json['root_path'] as String?,
      description: json['description'] as String?,
      orgSlug: json['org_slug'] as String?,
      createdAt: _parseTimestamp(json['created_at']),
      updatedAt: _parseTimestamp(json['updated_at']),
      lastActiveAt: json['last_active_at'] != null
          ? _parseTimestamp(json['last_active_at'])
          : null,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        if (rootPath != null) 'root_path': rootPath,
        if (description != null) 'description': description,
        if (orgSlug != null) 'org_slug': orgSlug,
        'created_at': createdAt.millisecondsSinceEpoch ~/ 1000,
        'updated_at': updatedAt.millisecondsSinceEpoch ~/ 1000,
        if (lastActiveAt != null)
          'last_active_at': lastActiveAt!.millisecondsSinceEpoch ~/ 1000,
      };

  Project copyWith({
    String? id,
    String? name,
    String? rootPath,
    String? description,
    String? orgSlug,
    DateTime? createdAt,
    DateTime? updatedAt,
    DateTime? lastActiveAt,
  }) =>
      Project(
        id: id ?? this.id,
        name: name ?? this.name,
        rootPath: rootPath ?? this.rootPath,
        description: description ?? this.description,
        orgSlug: orgSlug ?? this.orgSlug,
        createdAt: createdAt ?? this.createdAt,
        updatedAt: updatedAt ?? this.updatedAt,
        lastActiveAt: lastActiveAt ?? this.lastActiveAt,
      );

  @override
  String toString() => 'Project(id: $id, name: $name)';
}

class ProjectRepo {
  final String projectId;
  final String repoPath;
  final DateTime addedAt;
  final DateTime? lastOpenedAt;

  const ProjectRepo({
    required this.projectId,
    required this.repoPath,
    required this.addedAt,
    this.lastOpenedAt,
  });

  factory ProjectRepo.fromJson(Map<String, dynamic> json) {
    return ProjectRepo(
      projectId: json['project_id'] as String,
      repoPath: json['repo_path'] as String,
      addedAt: _parseTimestamp(json['added_at']),
      lastOpenedAt: json['last_opened_at'] != null
          ? _parseTimestamp(json['last_opened_at'])
          : null,
    );
  }

  Map<String, dynamic> toJson() => {
        'project_id': projectId,
        'repo_path': repoPath,
        'added_at': addedAt.millisecondsSinceEpoch ~/ 1000,
        if (lastOpenedAt != null)
          'last_opened_at': lastOpenedAt!.millisecondsSinceEpoch ~/ 1000,
      };

  ProjectRepo copyWith({
    String? projectId,
    String? repoPath,
    DateTime? addedAt,
    DateTime? lastOpenedAt,
  }) =>
      ProjectRepo(
        projectId: projectId ?? this.projectId,
        repoPath: repoPath ?? this.repoPath,
        addedAt: addedAt ?? this.addedAt,
        lastOpenedAt: lastOpenedAt ?? this.lastOpenedAt,
      );

  /// Convenience: the directory name of the repo path.
  String get repoName => repoPath.split('/').last;

  @override
  String toString() => 'ProjectRepo(projectId: $projectId, repoPath: $repoPath)';
}

class ProjectWithRepos {
  final Project project;
  final List<ProjectRepo> repos;

  const ProjectWithRepos({
    required this.project,
    required this.repos,
  });

  factory ProjectWithRepos.fromJson(Map<String, dynamic> json) {
    final reposList = json['repos'] as List<dynamic>? ?? [];
    return ProjectWithRepos(
      project: Project.fromJson(json['project'] as Map<String, dynamic>),
      repos: reposList
          .map((r) => ProjectRepo.fromJson(r as Map<String, dynamic>))
          .toList(),
    );
  }

  Map<String, dynamic> toJson() => {
        'project': project.toJson(),
        'repos': repos.map((r) => r.toJson()).toList(),
      };

  @override
  String toString() =>
      'ProjectWithRepos(project: $project, repos: ${repos.length})';
}

/// Parse a timestamp field that may be a Unix epoch integer (seconds)
/// or an ISO 8601 string. Logs unknown formats and falls back to epoch.
DateTime _parseTimestamp(dynamic raw) {
  if (raw is int) {
    return DateTime.fromMillisecondsSinceEpoch(raw * 1000, isUtc: true);
  }
  if (raw is String) {
    try {
      return DateTime.parse(raw);
    } catch (_) {
      dev.log('Failed to parse timestamp: $raw', name: 'clawd_proto');
    }
  }
  dev.log('Unknown timestamp format: $raw (${raw.runtimeType})', name: 'clawd_proto');
  return DateTime.fromMillisecondsSinceEpoch(0, isUtc: true);
}
