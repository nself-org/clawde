/// Tool sovereignty types for the clawd daemon.
///
/// Sprint DD TS.5 — sovereignty.report/events RPC types.
/// Tracks which other AI tools are writing to the current codebase.

// ─── Sovereignty Event ────────────────────────────────────────────────────────

class SovereigntyEvent {
  final String id;
  final String toolId;
  final String eventType;
  final List<String> filePaths;
  final DateTime detectedAt;

  const SovereigntyEvent({
    required this.id,
    required this.toolId,
    required this.eventType,
    required this.filePaths,
    required this.detectedAt,
  });

  factory SovereigntyEvent.fromJson(Map<String, dynamic> json) =>
      SovereigntyEvent(
        id: json['id'] as String? ?? '',
        toolId: json['toolId'] as String? ?? '',
        eventType: json['eventType'] as String? ?? '',
        filePaths: (json['filePaths'] as List?)?.cast<String>() ?? [],
        detectedAt: _parseTimestamp(json['detectedAt']),
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'toolId': toolId,
        'eventType': eventType,
        'filePaths': filePaths,
        'detectedAt': detectedAt.toIso8601String(),
      };

  @override
  String toString() =>
      'SovereigntyEvent(toolId: $toolId, eventType: $eventType)';
}

// ─── Tool Summary ─────────────────────────────────────────────────────────────

class ToolSummary {
  final String toolId;
  final int eventCount;
  final List<String> filesTouched;
  final String? lastSeen;

  const ToolSummary({
    required this.toolId,
    required this.eventCount,
    required this.filesTouched,
    this.lastSeen,
  });

  factory ToolSummary.fromJson(Map<String, dynamic> json) => ToolSummary(
        toolId: json['toolId'] as String? ?? '',
        eventCount: json['eventCount'] as int? ?? 0,
        filesTouched: (json['filesTouched'] as List?)?.cast<String>() ?? [],
        lastSeen: json['lastSeen'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'toolId': toolId,
        'eventCount': eventCount,
        'filesTouched': filesTouched,
        if (lastSeen != null) 'lastSeen': lastSeen,
      };

  @override
  String toString() =>
      'ToolSummary(toolId: $toolId, eventCount: $eventCount)';
}

// ─── Sovereignty Report ───────────────────────────────────────────────────────

class SovereigntyReport {
  final String period;
  final List<ToolSummary> tools;
  final int totalEvents;

  const SovereigntyReport({
    required this.period,
    required this.tools,
    required this.totalEvents,
  });

  factory SovereigntyReport.fromJson(Map<String, dynamic> json) =>
      SovereigntyReport(
        period: json['period'] as String? ?? '7d',
        tools: (json['tools'] as List?)
                ?.map((t) =>
                    ToolSummary.fromJson(t as Map<String, dynamic>))
                .toList() ??
            [],
        totalEvents: json['totalEvents'] as int? ?? 0,
      );

  Map<String, dynamic> toJson() => {
        'period': period,
        'tools': tools.map((t) => t.toJson()).toList(),
        'totalEvents': totalEvents,
      };

  bool get hasOtherTools => tools.isNotEmpty;

  @override
  String toString() =>
      'SovereigntyReport(period: $period, tools: ${tools.length})';
}

// ─── Push Event ───────────────────────────────────────────────────────────────

class SovereigntyToolDetectedEvent {
  final String toolId;
  final String eventType;
  final List<String> filePaths;

  const SovereigntyToolDetectedEvent({
    required this.toolId,
    required this.eventType,
    required this.filePaths,
  });

  factory SovereigntyToolDetectedEvent.fromJson(Map<String, dynamic> json) =>
      SovereigntyToolDetectedEvent(
        toolId: json['toolId'] as String? ?? '',
        eventType: json['eventType'] as String? ?? '',
        filePaths: (json['filePaths'] as List?)?.cast<String>() ?? [],
      );
}

// ─── Internal ─────────────────────────────────────────────────────────────────

DateTime _parseTimestamp(dynamic raw) {
  if (raw is int) {
    return DateTime.fromMillisecondsSinceEpoch(raw * 1000, isUtc: true);
  }
  if (raw is String) {
    try {
      return DateTime.parse(raw);
    } catch (_) {}
  }
  return DateTime.fromMillisecondsSinceEpoch(0, isUtc: true);
}
