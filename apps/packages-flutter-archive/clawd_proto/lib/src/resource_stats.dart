/// ResourceStats — snapshot returned by `system.resources`.

class ResourceRam {
  final int totalBytes;
  final int usedBytes;
  final int daemonBytes;
  final int usedPercent;

  const ResourceRam({
    required this.totalBytes,
    required this.usedBytes,
    required this.daemonBytes,
    required this.usedPercent,
  });

  factory ResourceRam.fromJson(Map<String, dynamic> json) => ResourceRam(
        totalBytes: (json['totalBytes'] as num?)?.toInt() ?? 0,
        usedBytes: (json['usedBytes'] as num?)?.toInt() ?? 0,
        daemonBytes: (json['daemonBytes'] as num?)?.toInt() ?? 0,
        usedPercent: (json['usedPercent'] as num?)?.toInt() ?? 0,
      );

  String get usedMb => '${(usedBytes / 1024 / 1024).round()} MB';
  String get daemonMb => '${(daemonBytes / 1024 / 1024).round()} MB';
  String get totalGb =>
      totalBytes > 0 ? '${(totalBytes / 1024 / 1024 / 1024).toStringAsFixed(1)} GB' : '?';
}

class ResourceSessionCounts {
  final int active;
  final int warm;
  final int cold;

  const ResourceSessionCounts({
    required this.active,
    required this.warm,
    required this.cold,
  });

  factory ResourceSessionCounts.fromJson(Map<String, dynamic> json) =>
      ResourceSessionCounts(
        active: (json['active'] as num?)?.toInt() ?? 0,
        warm: (json['warm'] as num?)?.toInt() ?? 0,
        cold: (json['cold'] as num?)?.toInt() ?? 0,
      );

  int get total => active + warm + cold;
}

class ResourceStats {
  final ResourceRam ram;
  final ResourceSessionCounts sessions;

  const ResourceStats({required this.ram, required this.sessions});

  factory ResourceStats.fromJson(Map<String, dynamic> json) => ResourceStats(
        ram: ResourceRam.fromJson(
            (json['ram'] as Map<String, dynamic>?) ?? {}),
        sessions: ResourceSessionCounts.fromJson(
            (json['sessions'] as Map<String, dynamic>?) ?? {}),
      );
}
