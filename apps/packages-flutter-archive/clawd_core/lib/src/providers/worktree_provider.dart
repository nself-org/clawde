import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'daemon_provider.dart';

/// Fetches the [WorktreeInfo] for the given [taskId], or null if none exists.
///
/// Uses [worktrees.list] and returns the first matching entry. Returns null on
/// error so consumers can silently ignore missing worktrees.
final worktreeProvider =
    FutureProvider.family<WorktreeInfo?, String>((ref, taskId) async {
  final client = ref.read(daemonProvider.notifier).client;
  try {
    final list = await client.listWorktrees();
    return list.where((w) => w.taskId == taskId).firstOrNull;
  } catch (_) {
    return null;
  }
});
