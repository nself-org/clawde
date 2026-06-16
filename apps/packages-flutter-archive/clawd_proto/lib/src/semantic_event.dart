/// Semantic event and project pulse types for the clawd daemon.
///
/// Sprint DD PP.5 — project.pulse RPC types.
/// Tracks semantic change velocity — features, bug fixes, refactors, tests.

// ─── Semantic Event ───────────────────────────────────────────────────────────

enum SemanticEventType {
  featureAdded,
  bugFixed,
  refactored,
  testAdded,
  configChanged,
  dependencyUpdated,
  unknown;

  static SemanticEventType fromString(String s) => switch (s) {
        'feature_added' => featureAdded,
        'bug_fixed' => bugFixed,
        'refactored' => refactored,
        'test_added' => testAdded,
        'config_changed' => configChanged,
        'dependency_updated' => dependencyUpdated,
        _ => unknown,
      };

  String get displayName => switch (this) {
        featureAdded => 'Feature Added',
        bugFixed => 'Bug Fixed',
        refactored => 'Refactored',
        testAdded => 'Test Added',
        configChanged => 'Config Changed',
        dependencyUpdated => 'Dependency Updated',
        unknown => 'Unknown',
      };
}

class SemanticEvent {
  final String id;
  final String? sessionId;
  final SemanticEventType eventType;
  final String summaryText;
  final List<String> affectedFiles;
  final DateTime createdAt;

  const SemanticEvent({
    required this.id,
    this.sessionId,
    required this.eventType,
    required this.summaryText,
    required this.affectedFiles,
    required this.createdAt,
  });

  factory SemanticEvent.fromJson(Map<String, dynamic> json) => SemanticEvent(
        id: json['id'] as String? ?? '',
        sessionId: json['sessionId'] as String?,
        eventType: SemanticEventType.fromString(
            json['eventType'] as String? ?? ''),
        summaryText: json['summaryText'] as String? ?? '',
        affectedFiles:
            (json['affectedFiles'] as List?)?.cast<String>() ?? [],
        createdAt: _parseTimestamp(json['createdAt']),
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        if (sessionId != null) 'sessionId': sessionId,
        'eventType': eventType.name,
        'summaryText': summaryText,
        'affectedFiles': affectedFiles,
        'createdAt': createdAt.toIso8601String(),
      };

  @override
  String toString() =>
      'SemanticEvent(type: ${eventType.displayName}, summary: $summaryText)';
}

// ─── Project Velocity ─────────────────────────────────────────────────────────

class ProjectVelocity {
  final int features;
  final int bugs;
  final int refactors;
  final int tests;
  final int configs;
  final int dependencies;

  const ProjectVelocity({
    this.features = 0,
    this.bugs = 0,
    this.refactors = 0,
    this.tests = 0,
    this.configs = 0,
    this.dependencies = 0,
  });

  factory ProjectVelocity.fromJson(Map<String, dynamic> json) =>
      ProjectVelocity(
        features: json['features'] as int? ?? 0,
        bugs: json['bugs'] as int? ?? 0,
        refactors: json['refactors'] as int? ?? 0,
        tests: json['tests'] as int? ?? 0,
        configs: json['configs'] as int? ?? 0,
        dependencies: json['dependencies'] as int? ?? 0,
      );

  int get total => features + bugs + refactors + tests + configs + dependencies;

  @override
  String toString() =>
      'ProjectVelocity(features: $features, bugs: $bugs, refactors: $refactors)';
}

// ─── Project Pulse ────────────────────────────────────────────────────────────

class ProjectPulse {
  final String period;
  final ProjectVelocity velocity;
  final List<SemanticEvent> events;

  const ProjectPulse({
    required this.period,
    required this.velocity,
    required this.events,
  });

  factory ProjectPulse.fromJson(Map<String, dynamic> json) => ProjectPulse(
        period: json['period'] as String? ?? '7d',
        velocity: json['velocity'] != null
            ? ProjectVelocity.fromJson(
                json['velocity'] as Map<String, dynamic>)
            : const ProjectVelocity(),
        events: (json['events'] as List?)
                ?.map((e) =>
                    SemanticEvent.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
      );

  Map<String, dynamic> toJson() => {
        'period': period,
        'velocity': {
          'features': velocity.features,
          'bugs': velocity.bugs,
          'refactors': velocity.refactors,
          'tests': velocity.tests,
          'configs': velocity.configs,
          'dependencies': velocity.dependencies,
        },
        'events': events.map((e) => e.toJson()).toList(),
      };

  @override
  String toString() =>
      'ProjectPulse(period: $period, events: ${events.length})';
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
