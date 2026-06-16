// SPDX-License-Identifier: MIT
/// Riverpod provider for session health data (Sprint G, SI.T15).
///
/// Wraps the `session.health` RPC call introduced in Sprint G.
/// The provider returns null (silently) when the daemon doesn't support
/// the RPC yet, so older daemon builds don't crash the UI.
library;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'daemon_provider.dart';

/// Health state for a session, keyed by [sessionId].
///
/// Returns a map with the following keys when available:
///   sessionId, healthScore (int 0–100), totalTurns, consecutiveLowQuality,
///   shortResponseCount, toolErrorCount, truncationCount, needsRefresh (bool).
///
/// Returns null if the daemon doesn't support `session.health` yet.
final sessionHealthProvider =
    FutureProvider.autoDispose.family<Map<String, dynamic>?, String>(
        (ref, sessionId) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.sessionHealth(sessionId);
});
