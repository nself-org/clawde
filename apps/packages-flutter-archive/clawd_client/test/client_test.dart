// QA-02: Unit tests for ClawdClient.
// Tests use a mock WebSocket channel — no real network required.
import 'dart:async';
import 'dart:convert';

import 'package:clawd_client/clawd_client.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

// ── Mocks ─────────────────────────────────────────────────────────────────────

class MockWebSocketChannel extends Mock implements WebSocketChannel {}

class MockWebSocketSink extends Mock implements WebSocketSink {}

// ── Helpers ───────────────────────────────────────────────────────────────────

/// Builds a raw JSON-RPC success response.
String _successResponse(int id, dynamic result) => jsonEncode({
      'jsonrpc': '2.0',
      'id': id,
      'result': result,
    });

/// Builds a raw JSON-RPC error response.
String _errorResponse(int id, int code, String message) => jsonEncode({
      'jsonrpc': '2.0',
      'id': id,
      'error': {'code': code, 'message': message},
    });

/// Builds a server push notification (no id field).
String _pushNotification(String method, Map<String, dynamic> params) =>
    jsonEncode({'jsonrpc': '2.0', 'method': method, 'params': params});

// ── Test setup helpers ────────────────────────────────────────────────────────

typedef _Harness = ({
  ClawdClient client,
  MockWebSocketChannel channel,
  MockWebSocketSink sink,
  StreamController<dynamic> incoming,
  List<String> sent,
});

/// Creates a [ClawdClient] with a mock channel injected.
/// [incoming] simulates messages arriving from the daemon.
/// [sent] records all messages the client sends.
_Harness _buildHarness() {
  final channel = MockWebSocketChannel();
  final sink = MockWebSocketSink();
  final incoming = StreamController<dynamic>.broadcast();
  final sent = <String>[];

  when(() => channel.stream).thenAnswer((_) => incoming.stream);
  when(() => channel.sink).thenReturn(sink);
  when(() => sink.add(any())).thenAnswer((inv) {
    sent.add(inv.positionalArguments[0] as String);
  });
  when(() => sink.close()).thenAnswer((_) async {});

  final client = ClawdClient(
    channelFactory: (_) => channel,
    // Short timeout so timeout tests don't take 30 s.
    callTimeout: const Duration(seconds: 2),
  );

  return (
    client: client,
    channel: channel,
    sink: sink,
    incoming: incoming,
    sent: sent,
  );
}

// ── Tests ─────────────────────────────────────────────────────────────────────

void main() {
  setUpAll(() {
    // Required by mocktail for any() matchers.
    registerFallbackValue('');
  });

  group('ClawdClient', () {
    // ── call() resolves on success response ────────────────────────────────

    test('call() sends request and resolves when matching response arrives',
        () async {
      final h = _buildHarness();
      await h.client.connect();

      final resultFuture =
          h.client.call<Map<String, dynamic>>('daemon.status');

      // The client should have sent exactly one message.
      expect(h.sent, hasLength(1));

      final sent = jsonDecode(h.sent[0]) as Map<String, dynamic>;
      expect(sent['jsonrpc'], '2.0');
      expect(sent['method'], 'daemon.status');
      final id = sent['id'] as int;

      // Simulate daemon response.
      h.incoming.add(_successResponse(id, {'version': '0.1.0'}));

      final result = await resultFuture;
      expect(result['version'], '0.1.0');

      await h.incoming.close();
    });

    // ── call() throws ClawdRpcError on error response ──────────────────────

    test('call() throws ClawdRpcError when response has error field', () async {
      final h = _buildHarness();
      await h.client.connect();

      final resultFuture = h.client.call<dynamic>('session.create', {
        'repoPath': '/nonexistent',
        'provider': 'claude',
      });

      final sent = jsonDecode(h.sent[0]) as Map<String, dynamic>;
      final id = sent['id'] as int;

      // Simulate error response from daemon.
      h.incoming.add(_errorResponse(id, -32001, 'Session not found'));

      await expectLater(
        resultFuture,
        throwsA(isA<ClawdRpcError>()
            .having((e) => e.code, 'code', -32001)
            .having((e) => e.message, 'message', 'Session not found')),
      );

      await h.incoming.close();
    });

    // ── push events route to pushEvents stream ─────────────────────────────

    test('push event (no id) is emitted on pushEvents stream', () async {
      final h = _buildHarness();
      await h.client.connect();

      final eventsReceived = <Map<String, dynamic>>[];
      final sub = h.client.pushEvents.listen(eventsReceived.add);

      // Simulate a server-push notification.
      h.incoming.add(_pushNotification(
        'session.statusChanged',
        {'sessionId': 'sess-1', 'status': 'completed'},
      ));

      // Allow microtasks to run.
      await Future<void>.delayed(Duration.zero);

      expect(eventsReceived, hasLength(1));
      expect(eventsReceived[0]['method'], 'session.statusChanged');
      expect(eventsReceived[0]['params']['status'], 'completed');

      await sub.cancel();
      await h.incoming.close();
    });

    // ── disconnect during pending call throws ClawdDisconnectedError ───────

    test('disconnect during pending call() throws ClawdDisconnectedError',
        () async {
      final h = _buildHarness();
      await h.client.connect();

      final resultFuture = h.client.call<dynamic>('session.list');

      // Close the incoming stream to simulate daemon disconnect.
      await h.incoming.close();

      await expectLater(
        resultFuture,
        throwsA(isA<ClawdDisconnectedError>()),
      );
    });

    // ── call() on disconnected client throws immediately ───────────────────

    test('call() on disconnected client throws ClawdDisconnectedError', () {
      final h = _buildHarness();
      // Never call connect() — client is not connected.

      expect(
        () => h.client.call<dynamic>('daemon.status'),
        throwsA(isA<ClawdDisconnectedError>()),
      );
    });

    // ── reconnect: new calls work after reconnect ──────────────────────────

    test('new calls succeed after reconnect', () async {
      final h = _buildHarness();
      await h.client.connect();

      // Simulate disconnect.
      await h.incoming.close();

      // Reconnect (creates a fresh channel via the same factory).
      final h2 = _buildHarness();
      await h2.client.connect();

      final resultFuture = h2.client.call<dynamic>('daemon.status');
      final sent = jsonDecode(h2.sent[0]) as Map<String, dynamic>;
      final id = sent['id'] as int;

      h2.incoming.add(_successResponse(id, {'version': '0.2.0'}));

      final result = await resultFuture;
      expect(result['version'], '0.2.0');

      await h2.incoming.close();
    });
  });
}
