/// Session types for the clawd daemon.

import 'dart:developer' as dev;

enum SessionStatus { idle, running, paused, completed, error }

enum ProviderType { claude, codex, cursor }

/// GCI mode for a session.
enum SessionMode { normal, learn, storm, forge, crunch }

/// Resource tier assigned by the ResourceGovernor.
enum SessionTier { active, warm, cold }

class Session {
  final String id;
  final String repoPath;
  final String title;
  final ProviderType provider;
  final SessionStatus status;
  final DateTime createdAt;
  final DateTime updatedAt;
  final int messageCount;
  /// GCI mode: which dev mode is active for this session.
  final SessionMode mode;
  /// Resource tier assigned by the resource governor.
  final SessionTier tier;
  /// Explicit model override set by the user via session.setModel.
  /// Null = auto-route; non-null = pinned model ID (MI.T12).
  final String? modelOverride;

  const Session({
    required this.id,
    required this.repoPath,
    required this.title,
    required this.provider,
    required this.status,
    required this.createdAt,
    required this.updatedAt,
    required this.messageCount,
    this.mode = SessionMode.normal,
    this.tier = SessionTier.cold,
    this.modelOverride,
  });

  factory Session.fromJson(Map<String, dynamic> json) {
    final providerStr = json['provider'] as String? ?? '';
    final statusStr = json['status'] as String? ?? 'idle';
    return Session(
      id: json['id'] as String,
      repoPath: json['repoPath'] as String,
      title: json['title'] as String? ?? '',
      provider: _parseProvider(providerStr),
      status: _parseStatus(statusStr),
      createdAt: DateTime.parse(json['createdAt'] as String),
      updatedAt: DateTime.parse(json['updatedAt'] as String),
      messageCount: json['messageCount'] as int? ?? 0,
      mode: _parseMode(json['mode'] as String? ?? 'NORMAL'),
      tier: _parseTier(json['tier'] as String? ?? 'cold'),
      modelOverride: json['modelOverride'] as String?,
    );
  }

  static ProviderType _parseProvider(String s) {
    try {
      return ProviderType.values.byName(s);
    } catch (_) {
      dev.log('unknown provider: $s', name: 'clawd_proto');
      return ProviderType.claude;
    }
  }

  static SessionStatus _parseStatus(String s) {
    try {
      return SessionStatus.values.byName(s);
    } catch (_) {
      dev.log('unknown status: $s', name: 'clawd_proto');
      return SessionStatus.idle;
    }
  }

  static SessionMode _parseMode(String s) {
    return switch (s.toUpperCase()) {
      'LEARN' => SessionMode.learn,
      'STORM' => SessionMode.storm,
      'FORGE' => SessionMode.forge,
      'CRUNCH' => SessionMode.crunch,
      _ => SessionMode.normal,
    };
  }

  static SessionTier _parseTier(String s) {
    return switch (s.toLowerCase()) {
      'active' => SessionTier.active,
      'warm' => SessionTier.warm,
      _ => SessionTier.cold,
    };
  }
}
