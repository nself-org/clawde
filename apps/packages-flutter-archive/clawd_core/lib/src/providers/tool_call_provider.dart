import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'daemon_provider.dart';

/// Tool calls for a session, accumulated from push events.
/// There is no listing RPC — state is built entirely from
/// `session.toolCallCreated` and `session.toolCallUpdated` push events.
class ToolCallNotifier
    extends FamilyAsyncNotifier<List<ToolCall>, String> {
  @override
  Future<List<ToolCall>> build(String sessionId) async {
    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == null) return;
        final params = event['params'] as Map<String, dynamic>?;
        if (params == null) return;

        // Guard: only process events for this session.
        final evtSessionId = params['sessionId'] as String?;
        if (evtSessionId != arg) return;

        if (method == 'session.toolCallCreated') {
          // sessionId is in outer params; toolCall sub-object lacks it.
          final tcJson = params['toolCall'] as Map<String, dynamic>?;
          if (tcJson != null) {
            final tc = ToolCall.fromJson({...tcJson, 'sessionId': arg});
            _addToolCall(tc);
          }
        } else if (method == 'session.toolCallUpdated') {
          final tcId = params['toolCallId'] as String?;
          final status = params['status'] as String?;
          if (tcId != null) _updateToolCallStatus(tcId, status);
        }
      });
    });

    // Start with an empty list — tool calls accumulate via push events.
    return const [];
  }

  void _addToolCall(ToolCall tc) {
    state.whenData((calls) {
      state = AsyncValue.data([...calls, tc]);
    });
  }

  void _updateToolCallStatus(String tcId, String? status) {
    if (status == null) return;
    state.whenData((calls) {
      state = AsyncValue.data(
        calls.map((tc) {
          if (tc.id != tcId) return tc;
          final parsedStatus = switch (status) {
            'done' || 'completed' => ToolCallStatus.completed,
            'running' => ToolCallStatus.running,
            'pending' => ToolCallStatus.pending,
            _ => ToolCallStatus.error,
          };
          return ToolCall(
            id: tc.id,
            sessionId: tc.sessionId,
            messageId: tc.messageId,
            toolName: tc.toolName,
            input: tc.input,
            status: parsedStatus,
            createdAt: tc.createdAt,
            completedAt: tc.completedAt,
          );
        }).toList(),
      );
    });
  }

  Future<void> approve(String toolCallId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('tool.approve', {
      'sessionId': arg,
      'toolCallId': toolCallId,
    });
  }

  Future<void> reject(String toolCallId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('tool.reject', {
      'sessionId': arg,
      'toolCallId': toolCallId,
    });
  }
}

final toolCallProvider = AsyncNotifierProviderFamily<ToolCallNotifier,
    List<ToolCall>, String>(
  ToolCallNotifier.new,
);

/// Count of pending tool calls for a session — drives badge indicators.
final pendingToolCallCountProvider =
    Provider.family<int, String>((ref, sessionId) {
  return ref
          .watch(toolCallProvider(sessionId))
          .valueOrNull
          ?.where((tc) => tc.status == ToolCallStatus.pending)
          .length ??
      0;
});
