import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'daemon_provider.dart';

/// Snapshot of the daemon's current license state, including any active grace period.
class LicenseStatus {
  final String tier;
  final bool relayEnabled;
  final bool autoSwitchEnabled;

  /// Days remaining in dunning grace period.
  /// Non-null (including 0) means the account is in grace period.
  /// Null means no active grace period.
  final int? graceDaysRemaining;

  const LicenseStatus({
    required this.tier,
    required this.relayEnabled,
    required this.autoSwitchEnabled,
    this.graceDaysRemaining,
  });

  bool get inGracePeriod => graceDaysRemaining != null;

  /// Whether the ClawDE+ tier is active (personal-remote or any cloud tier).
  bool get clawdePlus => tier == 'personal-remote' ||
      tier == 'cloud-basic' ||
      tier == 'cloud-pro' ||
      tier == 'cloud-max' ||
      tier == 'teams' ||
      tier == 'enterprise';

  factory LicenseStatus.free() => const LicenseStatus(
        tier: 'free',
        relayEnabled: false,
        autoSwitchEnabled: false,
      );

  factory LicenseStatus.fromJson(Map<String, dynamic> json) {
    final features = json['features'] as Map<String, dynamic>? ?? {};
    return LicenseStatus(
      tier: json['tier'] as String? ?? 'free',
      relayEnabled: features['relay'] as bool? ?? false,
      autoSwitchEnabled: features['autoSwitch'] as bool? ?? false,
      graceDaysRemaining: json['graceDaysRemaining'] as int?,
    );
  }
}

/// Fetches the current license status from the daemon via `license.get`.
///
/// Rebuilds whenever the daemon connection changes. Returns [LicenseStatus.free]
/// when the daemon is not connected or the call fails.
final licenseProvider = FutureProvider<LicenseStatus>((ref) async {
  final daemon = ref.watch(daemonProvider);
  if (!daemon.isConnected) return LicenseStatus.free();

  try {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<Map<String, dynamic>>('license.get');
    return LicenseStatus.fromJson(result);
  } catch (_) {
    return LicenseStatus.free();
  }
});
