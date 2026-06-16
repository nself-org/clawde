/// Providers for per-session indicator data: standards, provider knowledge,
/// and global drift items.
///
/// All three providers return empty lists when the daemon doesn't yet support
/// the corresponding RPC — they will light up automatically once the daemon
/// adds the methods in a future sprint.
library;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'daemon_provider.dart';

/// Active coding standards for a session, keyed by [sessionId].
///
/// Returns a `List<String>` of standard rule descriptions, or `[]` if the
/// daemon doesn't support `session.standards` yet.
final sessionStandardsProvider =
    FutureProvider.autoDispose.family<List<String>, String>((ref, sessionId) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.sessionStandards(sessionId);
});

/// Detected provider knowledge contexts for a session, keyed by [sessionId].
///
/// Returns a `List<String>` of provider names (e.g. ["Hetzner", "Stripe"]),
/// or `[]` if the daemon doesn't support `session.providerKnowledge` yet.
final sessionProviderKnowledgeProvider =
    FutureProvider.autoDispose.family<List<String>, String>((ref, sessionId) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.sessionProviderKnowledge(sessionId);
});

/// Global drift items detected by the daemon's drift scanner.
///
/// Returns a `List<String>` of drift item descriptions, or `[]` if the
/// daemon doesn't support `drift.list` yet.
final driftItemsProvider =
    FutureProvider.autoDispose<List<String>>((ref) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.driftList();
});
