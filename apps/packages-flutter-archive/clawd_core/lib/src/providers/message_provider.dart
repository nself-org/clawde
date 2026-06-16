import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'daemon_provider.dart';

/// Messages for a specific session. Keyed by session ID.
/// Appends/updates messages on `session.messageCreated` and
/// `session.messageUpdated` push events.
/// Supports ME-03 pagination via [loadMore].
///
/// V02.T13: incoming messageUpdated events are debounced to 80 ms to cap
/// rebuild rate at ~10/sec during fast streaming (avoids 50-100 redraws/sec).
/// V02.T14: initial load fetches 50 messages (was 20) for better lazy baseline.
class MessageListNotifier
    extends FamilyAsyncNotifier<List<Message>, String> {
  /// ID of the oldest loaded message — used as `before` cursor for loadMore.
  String? _oldestMessageId;

  // V02.T13 — debounce buffer for messageUpdated events
  final Map<String, ({String? content, String? status})> _updateBuffer = {};
  Timer? _flushTimer;

  @override
  Future<List<Message>> build(String sessionId) async {
    // Drain pending queue when daemon reconnects. Registered once in build()
    // so Riverpod tracks and cancels it; do NOT register inside send().
    ref.listen(daemonProvider, (prev, next) {
      if (!next.isConnected) return;
      if (prev?.isConnected == true) return;
      _drainQueue();
    });

    ref.listen(daemonPushEventsProvider, (_, next) {
      next.whenData((event) {
        final method = event['method'] as String?;
        if (method == null) return;
        final params = event['params'] as Map<String, dynamic>?;
        if (params == null) return;

        // Guard: only process events for this session.
        final evtSessionId = params['sessionId'] as String?;
        if (evtSessionId != arg) return;

        if (method == 'session.messageCreated') {
          final msgJson = params['message'] as Map<String, dynamic>?;
          if (msgJson != null) _appendMessage(Message.fromJson(msgJson));
        } else if (method == 'session.messageUpdated') {
          final msgId = params['messageId'] as String?;
          final content = params['content'] as String?;
          final status = params['status'] as String?;
          // V02.T13: buffer the update instead of applying immediately.
          if (msgId != null) {
            _bufferUpdate(msgId, content, status);
          }
        }
      });
    });

    final messages = await _fetchPage();
    if (messages.isNotEmpty) {
      _oldestMessageId = messages.first.id;
    }
    return messages;
  }

  /// Fetches one page of messages optionally before [before] cursor.
  /// V02.T14: page size is 50 (was 20) for better initial coverage.
  Future<List<Message>> _fetchPage({String? before}) async {
    final client = ref.read(daemonProvider.notifier).client;
    final params = <String, dynamic>{
      'sessionId': arg,
      'limit': 50, // V02.T14 — increased from 20
      if (before != null) 'before': before,
    };
    final result = await client.call<List<dynamic>>(
      'session.getMessages',
      params,
    );
    return result
        .map((j) => Message.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  void _appendMessage(Message msg) {
    state.whenData((messages) {
      state = AsyncValue.data([...messages, msg]);
    });
  }

  /// V02.T13 — Buffer a messageUpdated event and schedule a 80ms flush.
  void _bufferUpdate(String msgId, String? content, String? status) {
    _updateBuffer[msgId] = (content: content, status: status);
    _flushTimer?.cancel();
    _flushTimer = Timer(const Duration(milliseconds: 80), _flushUpdates);
  }

  /// V02.T13 — Apply all buffered updates in one state write (~10 redraws/sec).
  void _flushUpdates() {
    if (_updateBuffer.isEmpty) return;
    final pending = Map<String, ({String? content, String? status})>.from(
        _updateBuffer);
    _updateBuffer.clear();
    state.whenData((messages) {
      state = AsyncValue.data(
        messages.map((m) {
          final upd = pending[m.id];
          if (upd == null) return m;
          return Message(
            id: m.id,
            sessionId: m.sessionId,
            role: m.role,
            content: upd.content ?? m.content,
            status: upd.status ?? m.status,
            createdAt: m.createdAt,
            metadata: m.metadata,
          );
        }).toList(),
      );
    });
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final messages = await _fetchPage();
      if (messages.isNotEmpty) _oldestMessageId = messages.first.id;
      state = AsyncValue.data(messages);
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  /// ME-03 + V02.T14: Load older messages and prepend them to the current list.
  Future<void> loadMore() async {
    final current = state.valueOrNull;
    if (current == null || _oldestMessageId == null) return;
    final older = await _fetchPage(before: _oldestMessageId);
    if (older.isEmpty) return;
    _oldestMessageId = older.first.id;
    state = AsyncValue.data([...older, ...current]);
  }

  // Messages are retained in Riverpod state across reconnects — no cache flush on disconnect.

  /// SH-03: Pending queue — messages typed while offline are sent on reconnect.
  final List<String> _pendingQueue = [];

  /// Set of message contents currently being sent (in-flight). Prevents the
  /// same message from being queued twice if reconnect fires rapidly.
  final Set<String> _inFlight = {};

  Future<void> send(String content) async {
    final daemonState = ref.read(daemonProvider);
    if (!daemonState.isConnected) {
      // Guard: skip if this content is already queued or in-flight.
      if (_inFlight.contains(content)) return;
      if (_pendingQueue.contains(content)) return;

      // Queue the message and add a pending-state placeholder to the UI.
      _pendingQueue.add(content);
      _appendMessage(Message(
        id: 'pending-${DateTime.now().millisecondsSinceEpoch}',
        sessionId: arg,
        role: MessageRole.user,
        content: content,
        status: 'pending',
        createdAt: DateTime.now(),
        metadata: const {},
      ));
      // Drain listener is registered once in build() — no extra listener here.
      return;
    }
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('session.sendMessage', {
      'sessionId': arg,
      'content': content,
    });
    // The response message will arrive via push event and be appended above.
  }

  Future<void> _drainQueue() async {
    while (_pendingQueue.isNotEmpty) {
      final content = _pendingQueue.first;

      // Skip if already in-flight (e.g. rapid reconnect fired drain twice).
      if (_inFlight.contains(content)) {
        _pendingQueue.removeAt(0);
        continue;
      }

      _inFlight.add(content);
      try {
        final client = ref.read(daemonProvider.notifier).client;
        await client.call<void>('session.sendMessage', {
          'sessionId': arg,
          'content': content,
        });
        // Success — remove from both queue and in-flight set.
        _pendingQueue.removeAt(0);
        _inFlight.remove(content);
      } catch (_) {
        // Send failed — remove from in-flight so retry is possible, stop drain.
        _inFlight.remove(content);
        break;
      }
    }
  }
}

final messageListProvider = AsyncNotifierProviderFamily<MessageListNotifier,
    List<Message>, String>(
  MessageListNotifier.new,
);
