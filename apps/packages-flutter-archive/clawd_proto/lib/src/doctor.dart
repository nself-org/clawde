/// Doctor types for the `doctor.scan` / `doctor.fix` / `doctor.approveRelease`
/// JSON-RPC 2.0 methods (Phase D64).
///
/// Mirrors Rust types in `daemon/src/doctor/mod.rs`.

// ─── DoctorSeverity ──────────────────────────────────────────────────────────

enum DoctorSeverity {
  critical,
  high,
  medium,
  low,
  info;

  static DoctorSeverity fromJson(String value) => switch (value) {
        'critical' => DoctorSeverity.critical,
        'high' => DoctorSeverity.high,
        'medium' => DoctorSeverity.medium,
        'low' => DoctorSeverity.low,
        _ => DoctorSeverity.info,
      };

  String toJson() => name;

  /// Penalty applied to the score (0-100) for each finding of this severity.
  int get penalty => switch (this) {
        DoctorSeverity.critical => 20,
        DoctorSeverity.high => 10,
        DoctorSeverity.medium => 5,
        DoctorSeverity.low => 2,
        DoctorSeverity.info => 0,
      };
}

// ─── DoctorFinding ───────────────────────────────────────────────────────────

/// A single diagnostic finding returned by `doctor.scan`.
class DoctorFinding {
  /// Machine-readable code, e.g. `"afs.missing_vision"`.
  final String code;
  final DoctorSeverity severity;
  final String message;

  /// Absolute path to the problematic file or directory, if applicable.
  final String? path;

  /// Whether `doctor.fix` can automatically resolve this finding.
  final bool fixable;

  const DoctorFinding({
    required this.code,
    required this.severity,
    required this.message,
    this.path,
    required this.fixable,
  });

  factory DoctorFinding.fromJson(Map<String, dynamic> json) => DoctorFinding(
        code: json['code'] as String? ?? '',
        severity: DoctorSeverity.fromJson(json['severity'] as String? ?? 'info'),
        message: json['message'] as String? ?? '',
        path: json['path'] as String?,
        fixable: json['fixable'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'code': code,
        'severity': severity.toJson(),
        'message': message,
        if (path != null) 'path': path,
        'fixable': fixable,
      };
}

// ─── DoctorScanResult ────────────────────────────────────────────────────────

/// Result of `doctor.scan`.
class DoctorScanResult {
  /// Overall project health score 0–100 (100 = perfect).
  final int score;
  final List<DoctorFinding> findings;

  const DoctorScanResult({required this.score, required this.findings});

  factory DoctorScanResult.fromJson(Map<String, dynamic> json) =>
      DoctorScanResult(
        score: (json['score'] as num?)?.toInt() ?? 100,
        findings: (json['findings'] as List<dynamic>? ?? [])
            .map((e) => DoctorFinding.fromJson(e as Map<String, dynamic>))
            .toList(),
      );

  Map<String, dynamic> toJson() => {
        'score': score,
        'findings': findings.map((f) => f.toJson()).toList(),
      };

  /// True when there are no Critical or High findings.
  bool get isHealthy =>
      findings.every((f) =>
          f.severity != DoctorSeverity.critical &&
          f.severity != DoctorSeverity.high);
}

// ─── DoctorFixResult ─────────────────────────────────────────────────────────

/// Result of `doctor.fix`.
class DoctorFixResult {
  /// Codes that were successfully auto-fixed.
  final List<String> fixed;

  /// Codes that are fixable but could not be resolved automatically.
  final List<String> skipped;

  const DoctorFixResult({required this.fixed, required this.skipped});

  factory DoctorFixResult.fromJson(Map<String, dynamic> json) =>
      DoctorFixResult(
        fixed: (json['fixed'] as List<dynamic>? ?? []).cast<String>(),
        skipped: (json['skipped'] as List<dynamic>? ?? []).cast<String>(),
      );

  Map<String, dynamic> toJson() => {
        'fixed': fixed,
        'skipped': skipped,
      };
}
