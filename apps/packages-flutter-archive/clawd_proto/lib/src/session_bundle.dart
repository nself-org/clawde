/// Session replay and export types for the clawd daemon.
///
/// Sprint DD SR.6 — session.export/import/replay RPC types.

// ─── Session Bundle ───────────────────────────────────────────────────────────

class SessionBundle {
  /// Base64-encoded gzip-compressed JSON bundle.
  final String bundle;
  final String originalSessionId;
  final String exportedAt;
  final int messageCount;

  const SessionBundle({
    required this.bundle,
    required this.originalSessionId,
    required this.exportedAt,
    required this.messageCount,
  });

  factory SessionBundle.fromJson(Map<String, dynamic> json) => SessionBundle(
        bundle: json['bundle'] as String? ?? '',
        originalSessionId: json['originalSessionId'] as String? ?? '',
        exportedAt: json['exportedAt'] as String? ?? '',
        messageCount: json['messageCount'] as int? ?? 0,
      );

  Map<String, dynamic> toJson() => {
        'bundle': bundle,
        'originalSessionId': originalSessionId,
        'exportedAt': exportedAt,
        'messageCount': messageCount,
      };

  @override
  String toString() =>
      'SessionBundle(originalSessionId: $originalSessionId, messages: $messageCount)';
}

// ─── Import Result ────────────────────────────────────────────────────────────

class ImportResult {
  final String sessionId;
  final int importedMessages;

  const ImportResult({
    required this.sessionId,
    required this.importedMessages,
  });

  factory ImportResult.fromJson(Map<String, dynamic> json) => ImportResult(
        sessionId: json['sessionId'] as String? ?? '',
        importedMessages: json['importedMessages'] as int? ?? 0,
      );

  @override
  String toString() =>
      'ImportResult(sessionId: $sessionId, messages: $importedMessages)';
}

// ─── Replay Session ───────────────────────────────────────────────────────────

class ReplaySession {
  final String sessionId;
  final int totalMessages;
  final double speed;

  const ReplaySession({
    required this.sessionId,
    required this.totalMessages,
    required this.speed,
  });

  factory ReplaySession.fromJson(Map<String, dynamic> json) => ReplaySession(
        sessionId: json['sessionId'] as String? ?? '',
        totalMessages: json['totalMessages'] as int? ?? 0,
        speed: (json['speed'] as num?)?.toDouble() ?? 1.0,
      );

  @override
  String toString() =>
      'ReplaySession(sessionId: $sessionId, messages: $totalMessages, speed: $speed)';
}
