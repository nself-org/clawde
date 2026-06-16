/// Device pairing types for the clawd daemon.
///
/// Mirrors the daemon pairing module — devices trust each other via PIN exchange.

import 'dart:developer' as dev;

enum DevicePlatform {
  ios,
  android,
  macos,
  windows,
  linux,
  web;

  static DevicePlatform fromString(String s) => DevicePlatform.values.firstWhere(
        (e) => e.name == s,
        orElse: () {
          dev.log('Unknown device platform: $s', name: 'clawd_proto');
          return DevicePlatform.linux;
        },
      );

  /// Icon name hint for the UI layer to map to platform-specific icons.
  ///
  /// clawd_proto is pure Dart (no Flutter dependency) so we return a string
  /// token rather than a Flutter IconData. The UI layer maps these to Icons:
  ///   'smartphone'    → Icons.smartphone
  ///   'laptop_mac'    → Icons.laptop_mac
  ///   'laptop_windows'→ Icons.laptop_windows
  ///   'computer'      → Icons.computer
  String get iconName => switch (this) {
        DevicePlatform.ios || DevicePlatform.android => 'smartphone',
        DevicePlatform.macos => 'laptop_mac',
        DevicePlatform.windows => 'laptop_windows',
        DevicePlatform.linux || DevicePlatform.web => 'computer',
      };

  String get displayName => switch (this) {
        DevicePlatform.ios => 'iOS',
        DevicePlatform.android => 'Android',
        DevicePlatform.macos => 'macOS',
        DevicePlatform.windows => 'Windows',
        DevicePlatform.linux => 'Linux',
        DevicePlatform.web => 'Web',
      };
}

class PairedDevice {
  final String id;
  final String name;
  final DevicePlatform platform;
  final DateTime createdAt;
  final DateTime? lastSeenAt;
  final bool revoked;

  const PairedDevice({
    required this.id,
    required this.name,
    required this.platform,
    required this.createdAt,
    this.lastSeenAt,
    this.revoked = false,
  });

  factory PairedDevice.fromJson(Map<String, dynamic> json) {
    return PairedDevice(
      id: json['id'] as String,
      name: json['name'] as String,
      platform: DevicePlatform.fromString(json['platform'] as String? ?? 'linux'),
      createdAt: _parseTimestamp(json['created_at']),
      lastSeenAt: json['last_seen_at'] != null
          ? _parseTimestamp(json['last_seen_at'])
          : null,
      revoked: json['revoked'] as bool? ?? false,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'platform': platform.name,
        'created_at': createdAt.millisecondsSinceEpoch ~/ 1000,
        if (lastSeenAt != null)
          'last_seen_at': lastSeenAt!.millisecondsSinceEpoch ~/ 1000,
        'revoked': revoked,
      };

  PairedDevice copyWith({
    String? id,
    String? name,
    DevicePlatform? platform,
    DateTime? createdAt,
    DateTime? lastSeenAt,
    bool? revoked,
  }) =>
      PairedDevice(
        id: id ?? this.id,
        name: name ?? this.name,
        platform: platform ?? this.platform,
        createdAt: createdAt ?? this.createdAt,
        lastSeenAt: lastSeenAt ?? this.lastSeenAt,
        revoked: revoked ?? this.revoked,
      );

  @override
  String toString() => 'PairedDevice(id: $id, name: $name, platform: ${platform.name})';
}

/// Returned from the `daemon.pairPin` RPC — the PIN and connection info
/// needed by a device to complete pairing.
class PairInfo {
  final String pin;
  final int expiresInSeconds;
  final String daemonId;
  final String relayUrl;
  final String hostName;

  const PairInfo({
    required this.pin,
    required this.expiresInSeconds,
    required this.daemonId,
    required this.relayUrl,
    required this.hostName,
  });

  factory PairInfo.fromJson(Map<String, dynamic> json) {
    return PairInfo(
      pin: json['pin'] as String,
      expiresInSeconds: json['expires_in_seconds'] as int? ??
          json['expiresInSeconds'] as int? ??
          300,
      daemonId: json['daemon_id'] as String? ?? json['daemonId'] as String? ?? '',
      relayUrl: json['relay_url'] as String? ?? json['relayUrl'] as String? ?? '',
      hostName: json['host_name'] as String? ?? json['hostName'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'pin': pin,
        'expires_in_seconds': expiresInSeconds,
        'daemon_id': daemonId,
        'relay_url': relayUrl,
        'host_name': hostName,
      };

  @override
  String toString() => 'PairInfo(pin: $pin, daemonId: $daemonId)';
}

/// Returned from the `device.pair` RPC — the device credentials issued after
/// successful PIN verification.
class PairResult {
  final String deviceId;
  final String deviceToken;
  final String hostName;
  final String daemonId;
  final String relayUrl;

  const PairResult({
    required this.deviceId,
    required this.deviceToken,
    required this.hostName,
    required this.daemonId,
    required this.relayUrl,
  });

  factory PairResult.fromJson(Map<String, dynamic> json) {
    return PairResult(
      deviceId: json['device_id'] as String? ?? json['deviceId'] as String? ?? '',
      deviceToken: json['device_token'] as String? ?? json['deviceToken'] as String? ?? '',
      hostName: json['host_name'] as String? ?? json['hostName'] as String? ?? '',
      daemonId: json['daemon_id'] as String? ?? json['daemonId'] as String? ?? '',
      relayUrl: json['relay_url'] as String? ?? json['relayUrl'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'device_id': deviceId,
        'device_token': deviceToken,
        'host_name': hostName,
        'daemon_id': daemonId,
        'relay_url': relayUrl,
      };

  @override
  String toString() => 'PairResult(deviceId: $deviceId, hostName: $hostName)';
}

/// Parse a timestamp field that may be a Unix epoch integer (seconds)
/// or an ISO 8601 string.
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
