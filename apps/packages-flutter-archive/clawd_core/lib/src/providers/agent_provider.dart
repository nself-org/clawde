import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'daemon_provider.dart';

// ─── Agent list ───────────────────────────────────────────────────────────────

/// Provides the live list of registered agents from the daemon.
/// Parses as [AgentView] (matching `tasks.agents.list` response shape).
/// Re-fetches on connect and on `agent.statusChanged` push events.
class AgentListNotifier extends AsyncNotifier<List<AgentView>> {
  @override
  Future<List<AgentView>> build() async {
    // Re-fetch whenever the daemon reconnects.
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    // Optimistic in-place update on agent status changes.
    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == null) return;

        if (method == 'agent.statusChanged') {
          final params = event['params'] as Map<String, dynamic>?;
          final agentId = params?['agent_id'] as String? ?? params?['agentId'] as String?;
          final rawStatus = params?['status'] as String?;
          if (agentId != null && rawStatus != null) {
            final newStatus = AgentViewStatus.fromString(rawStatus);
            final current = state.valueOrNull;
            if (current != null) {
              state = AsyncValue.data(current
                  .map((a) => a.agentId == agentId ? a.copyWith(status: newStatus) : a)
                  .toList());
            }
          }
        } else if (method.startsWith('agent.')) {
          // Any other agent event — do a full refresh.
          refresh();
        }
      });
    });

    return _fetch();
  }

  Future<List<AgentView>> _fetch() async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<Map<String, dynamic>>('tasks.agents.list');
    final agents = result['agents'] as List<dynamic>? ?? [];
    return agents
        .map((j) => AgentView.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }
}

final agentsProvider =
    AsyncNotifierProvider<AgentListNotifier, List<AgentView>>(
  AgentListNotifier.new,
);

// ─── Approval queue ───────────────────────────────────────────────────────────

/// Provides the current list of pending approval requests.
/// Accumulates incoming `approval.requested` push events.
class ApprovalQueueNotifier extends AsyncNotifier<List<ApprovalRequest>> {
  @override
  Future<List<ApprovalRequest>> build() async {
    // Reset queue on reconnect.
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == null) return;

        if (method == 'tool.approvalRequested') {
          final params = event['params'] as Map<String, dynamic>?;
          if (params != null) {
            final newRequest = ApprovalRequest.fromJson(params);
            final current = state.valueOrNull ?? [];
            // Avoid duplicates.
            if (!current.any((r) => r.approvalId == newRequest.approvalId)) {
              state = AsyncValue.data([...current, newRequest]);
            }
          }
        } else if (method == 'task.approvalGranted' ||
            method == 'task.approvalDenied') {
          // Remove resolved approval from the queue.
          final params = event['params'] as Map<String, dynamic>?;
          final approvalId = params?['approval_id'] as String? ??
              params?['approvalId'] as String?;
          if (approvalId != null) {
            final current = state.valueOrNull;
            if (current != null) {
              state = AsyncValue.data(
                current.where((r) => r.approvalId != approvalId).toList(),
              );
            }
          }
        }
      });
    });

    return _fetch();
  }

  Future<List<ApprovalRequest>> _fetch() async {
    final client = ref.read(daemonProvider.notifier).client;
    try {
      final result =
          await client.call<Map<String, dynamic>>('approval.list');
      final approvals = result['approvals'] as List<dynamic>? ?? [];
      return approvals
          .map((j) => ApprovalRequest.fromJson(j as Map<String, dynamic>))
          .toList();
    } catch (_) {
      // approval.list may not exist in older daemon versions — return empty.
      return [];
    }
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }

  /// Approve a pending request by ID.
  Future<void> approve(String approvalId, {bool forTask = false}) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('approval.respond', {
      'approval_id': approvalId,
      'decision': 'grant',
    });
    _removeLocally(approvalId);
  }

  /// Deny a pending request by ID.
  Future<void> deny(String approvalId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('approval.respond', {
      'approval_id': approvalId,
      'decision': 'deny',
    });
    _removeLocally(approvalId);
  }

  void _removeLocally(String approvalId) {
    final current = state.valueOrNull;
    if (current != null) {
      state = AsyncValue.data(
        current.where((r) => r.approvalId != approvalId).toList(),
      );
    }
  }
}

final approvalQueueProvider =
    AsyncNotifierProvider<ApprovalQueueNotifier, List<ApprovalRequest>>(
  ApprovalQueueNotifier.new,
);
