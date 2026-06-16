import 'package:clawd_proto/clawd_proto.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'daemon_provider.dart';

/// Sprint DD WR.6 — workflow recipe providers.

// ─── Recipe list ──────────────────────────────────────────────────────────────

/// Fetches and caches the list of all workflow recipes.
///
/// Auto-disposes when no longer watched. Invalidate to refresh after a
/// `workflow.create` or `workflow.delete` call.
final workflowRecipesProvider =
    FutureProvider.autoDispose<List<WorkflowRecipe>>((ref) async {
  final client = ref.read(daemonProvider.notifier).client;
  final result = await client.workflowList();
  final list = (result['recipes'] as List?)?.cast<Map<String, dynamic>>() ?? [];
  return list.map(WorkflowRecipe.fromJson).toList();
});

// ─── Run workflow ─────────────────────────────────────────────────────────────

/// Runs a workflow recipe in the current repo.
///
/// Returns the run ID on success. This is a one-shot future — callers call
/// `ref.read(runWorkflowProvider(recipeId).future)` to trigger the run.
final runWorkflowProvider = FutureProvider.autoDispose
    .family<Map<String, dynamic>, String>((ref, recipeId) async {
  final client = ref.read(daemonProvider.notifier).client;
  return client.workflowRun(recipeId: recipeId);
});
