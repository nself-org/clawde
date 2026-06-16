// memory.dart — AI Memory protocol types (Sprint OO ME.10).

import 'package:meta/meta.dart';

@immutable
class MemoryEntryProto {
  const MemoryEntryProto({
    required this.id,
    required this.scope,
    required this.key,
    required this.value,
    required this.weight,
    required this.source,
    this.createdAt,
    this.updatedAt,
  });

  factory MemoryEntryProto.fromJson(Map<String, dynamic> json) =>
      MemoryEntryProto(
        id: json['id'] as String? ?? '',
        scope: json['scope'] as String? ?? 'global',
        key: json['key'] as String? ?? '',
        value: json['value'] as String? ?? '',
        weight: (json['weight'] as num?)?.toInt() ?? 5,
        source: json['source'] as String? ?? 'user',
        createdAt: json['created_at'] as String?,
        updatedAt: json['updated_at'] as String?,
      );

  final String id;
  final String scope;
  final String key;
  final String value;
  final int weight;
  final String source;
  final String? createdAt;
  final String? updatedAt;

  Map<String, dynamic> toJson() => {
        'id': id,
        'scope': scope,
        'key': key,
        'value': value,
        'weight': weight,
        'source': source,
        if (createdAt != null) 'created_at': createdAt,
        if (updatedAt != null) 'updated_at': updatedAt,
      };
}

@immutable
class MemoryListRequest {
  const MemoryListRequest({
    this.scope,
    this.repoPath,
    this.includeGlobal = true,
  });

  final String? scope;
  final String? repoPath;
  final bool includeGlobal;

  Map<String, dynamic> toJson() => {
        if (scope != null) 'scope': scope,
        if (repoPath != null) 'repo_path': repoPath,
        'include_global': includeGlobal,
      };
}

@immutable
class MemoryAddRequest {
  const MemoryAddRequest({
    required this.key,
    required this.value,
    this.scope = 'global',
    this.weight = 5,
    this.source = 'user',
    this.repoPath,
  });

  final String key;
  final String value;
  final String scope;
  final int weight;
  final String source;
  final String? repoPath;

  Map<String, dynamic> toJson() => {
        'key': key,
        'value': value,
        'scope': scope,
        'weight': weight,
        'source': source,
        if (repoPath != null) 'repo_path': repoPath,
      };
}

@immutable
class MemoryListResponse {
  const MemoryListResponse({required this.entries});

  factory MemoryListResponse.fromJson(Map<String, dynamic> json) =>
      MemoryListResponse(
        entries: (json['entries'] as List<dynamic>? ?? [])
            .map((e) =>
                MemoryEntryProto.fromJson(e as Map<String, dynamic>))
            .toList(),
      );

  final List<MemoryEntryProto> entries;
}
