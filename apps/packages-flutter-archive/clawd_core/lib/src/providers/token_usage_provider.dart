// SPDX-License-Identifier: MIT
/// Riverpod providers for token usage data (Sprint H, MI.T14/T16).
///
/// Wraps the `token.*` RPC calls introduced in Sprint H.
/// All providers return null/empty lists silently when the daemon doesn't
/// support the RPC yet, so older builds don't crash the UI.
library;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'daemon_provider.dart';

/// Token usage for a single session, keyed by [sessionId].
///
/// Returns a map with keys: inputTokens, outputTokens, estimatedCostUsd,
/// messageCount. Returns null if the daemon doesn't support this RPC yet.
final tokenSessionUsageProvider =
    FutureProvider.autoDispose.family<Map<String, dynamic>?, String>(
        (ref, sessionId) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.tokenSessionUsage(sessionId);
});

/// Monthly budget status (global — not per-session).
///
/// Returns a map with keys: monthlySpendUsd, cap, pct, warning, exceeded.
/// Returns null if the daemon doesn't support this RPC yet.
final tokenBudgetStatusProvider =
    FutureProvider.autoDispose<Map<String, dynamic>?>((ref) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.tokenBudgetStatus();
});

/// Usage breakdown by model for the current month (global).
///
/// Returns a list of maps with keys: modelId, inputTokens, outputTokens,
/// estimatedCostUsd, messageCount.
final tokenTotalUsageProvider =
    FutureProvider.autoDispose<List<Map<String, dynamic>>>((ref) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.tokenTotalUsage();
});
