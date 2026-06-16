import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'daemon_provider.dart';

/// Notifier that holds the latest doctor scan result for a given project path.
///
/// Usage:
/// ```dart
/// final scan = ref.watch(doctorProvider('~/Sites/myproject'));
/// ```
class DoctorNotifier
    extends AutoDisposeFamilyAsyncNotifier<DoctorScanResult?, String> {
  late String projectPath;

  @override
  Future<DoctorScanResult?> build(String arg) async {
    projectPath = arg;
    // Don't auto-scan on startup — caller must trigger scan() explicitly.
    return null;
  }

  /// Run a full doctor scan on [projectPath].
  Future<void> scan({String scope = 'all'}) async {
    state = const AsyncValue.loading();
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final raw = await client.doctorScan(projectPath, scope: scope);
      state = AsyncValue.data(DoctorScanResult.fromJson(raw));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  /// Run auto-fix for [codes] (empty = fix all fixable).
  ///
  /// After fixing, automatically re-scans so the UI reflects updated state.
  Future<DoctorFixResult> fix({List<String> codes = const []}) async {
    final client = ref.read(daemonProvider.notifier).client;
    final raw = await client.doctorFix(projectPath, codes: codes);
    final result = DoctorFixResult.fromJson(raw);
    // Re-scan to update findings.
    await scan();
    return result;
  }

  /// Approve the release plan for [version].
  Future<void> approveRelease(String version) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.doctorApproveRelease(projectPath, version);
    await scan(scope: 'release');
  }
}

/// Provider family keyed by project path.
final doctorProvider = AsyncNotifierProvider.autoDispose
    .family<DoctorNotifier, DoctorScanResult?, String>(
  DoctorNotifier.new,
);

/// Convenience provider: health score for a project (null if not yet scanned).
final doctorScoreProvider =
    Provider.autoDispose.family<int?, String>((ref, path) {
  return ref.watch(doctorProvider(path)).valueOrNull?.score;
});
