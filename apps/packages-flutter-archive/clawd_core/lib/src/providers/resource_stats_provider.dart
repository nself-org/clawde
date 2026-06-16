import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'daemon_provider.dart';

/// Polls `system.resources` every 5 seconds and exposes current stats.
///
/// Returns null when the daemon is unreachable.
final resourceStatsProvider =
    StreamProvider.autoDispose<ResourceStats?>((ref) async* {
  final client = ref.read(daemonProvider.notifier).client;
  while (true) {
    try {
      final raw = await client.systemResources();
      yield ResourceStats.fromJson(raw);
    } catch (_) {
      yield null;
    }
    await Future<void>.delayed(const Duration(seconds: 5));
  }
});
