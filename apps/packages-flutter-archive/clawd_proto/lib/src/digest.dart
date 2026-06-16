/// Mobile daily digest types for the clawd daemon.
///
/// Sprint EE DD.4 — digest.today RPC types.
/// Summarizes a day's AI session activity for mobile notification + digest screen.

// ─── Digest Metrics ───────────────────────────────────────────────────────────

class DigestMetrics {
  final int sessionsRun;
  final int tasksCompleted;
  final int tasksInProgress;
  final List<String> topFiles;
  final double evalAvg;
  final Map<String, int> velocity;

  const DigestMetrics({
    this.sessionsRun = 0,
    this.tasksCompleted = 0,
    this.tasksInProgress = 0,
    this.topFiles = const [],
    this.evalAvg = 0.0,
    this.velocity = const {},
  });

  factory DigestMetrics.fromJson(Map<String, dynamic> json) => DigestMetrics(
        sessionsRun: json['sessionsRun'] as int? ?? 0,
        tasksCompleted: json['tasksCompleted'] as int? ?? 0,
        tasksInProgress: json['tasksInProgress'] as int? ?? 0,
        topFiles: (json['topFiles'] as List?)?.cast<String>() ?? [],
        evalAvg: (json['evalAvg'] as num?)?.toDouble() ?? 0.0,
        velocity: (json['velocity'] as Map?)?.cast<String, int>() ?? {},
      );

  String get summary {
    final parts = <String>[];
    if (tasksCompleted > 0) {
      parts.add('$tasksCompleted task${tasksCompleted == 1 ? '' : 's'} done');
    }
    if (tasksInProgress > 0) {
      parts.add('$tasksInProgress in progress');
    }
    if (parts.isEmpty) return 'No activity today';
    return parts.join(', ');
  }
}

// ─── Digest Entry (per session) ───────────────────────────────────────────────

class DigestEntry {
  final String sessionId;
  final String? sessionTitle;
  final String provider;
  final int messagesCount;
  final int tasksCompleted;
  final List<String> filesChanged;
  final String startedAt;
  final String? endedAt;

  const DigestEntry({
    required this.sessionId,
    this.sessionTitle,
    required this.provider,
    required this.messagesCount,
    required this.tasksCompleted,
    required this.filesChanged,
    required this.startedAt,
    this.endedAt,
  });

  factory DigestEntry.fromJson(Map<String, dynamic> json) => DigestEntry(
        sessionId: json['sessionId'] as String? ?? '',
        sessionTitle: json['sessionTitle'] as String?,
        provider: json['provider'] as String? ?? '',
        messagesCount: json['messagesCount'] as int? ?? 0,
        tasksCompleted: json['tasksCompleted'] as int? ?? 0,
        filesChanged:
            (json['filesChanged'] as List?)?.cast<String>() ?? [],
        startedAt: json['startedAt'] as String? ?? '',
        endedAt: json['endedAt'] as String?,
      );
}

// ─── Daily Digest ─────────────────────────────────────────────────────────────

class DailyDigest {
  final String date;
  final DigestMetrics metrics;
  final List<DigestEntry> sessions;

  const DailyDigest({
    required this.date,
    required this.metrics,
    required this.sessions,
  });

  factory DailyDigest.fromJson(Map<String, dynamic> json) => DailyDigest(
        date: json['date'] as String? ?? '',
        metrics: json['metrics'] != null
            ? DigestMetrics.fromJson(json['metrics'] as Map<String, dynamic>)
            : const DigestMetrics(),
        sessions: (json['sessions'] as List?)
                ?.map((s) =>
                    DigestEntry.fromJson(s as Map<String, dynamic>))
                .toList() ??
            [],
      );

  bool get hasActivity =>
      metrics.sessionsRun > 0 || metrics.tasksCompleted > 0;

  @override
  String toString() =>
      'DailyDigest(date: $date, sessions: ${sessions.length})';
}

// ─── Digest Card (archived/UI) ────────────────────────────────────────────────

class DigestCard {
  final DailyDigest digest;
  final bool isArchived;

  const DigestCard({required this.digest, this.isArchived = false});
}
