import 'package:clawd_proto/clawd_proto.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'daemon_provider.dart';

/// Sprint DD PP.6 — project pulse provider.

/// Fetches the project pulse (semantic change velocity) for the last [days] days.
///
/// Defaults to 7 days. Auto-disposes when no longer watched.
final projectPulseProvider =
    FutureProvider.autoDispose.family<ProjectPulse, int>((ref, days) async {
  final client = ref.read(daemonProvider.notifier).client;
  final result = await client.projectPulse(days: days);
  return ProjectPulse.fromJson(result);
});

/// Convenience: pulse for the last 7 days (most common use case).
final pulse7dProvider =
    FutureProvider.autoDispose<ProjectPulse>((ref) async {
  return ref.watch(projectPulseProvider(7).future);
});
