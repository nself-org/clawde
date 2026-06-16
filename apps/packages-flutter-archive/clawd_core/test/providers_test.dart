// CK-01: Unit tests for clawd_core providers.
//
// Tests use ProviderContainer with overrides to avoid real WebSocket
// connections. SharedPreferences is mocked via setMockInitialValues.
import 'dart:async';

import 'package:clawd_client/clawd_client.dart';
import 'package:clawd_core/clawd_core.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:shared_preferences/shared_preferences.dart';

// ── Mocks ─────────────────────────────────────────────────────────────────────

class MockClawdClient extends Mock implements ClawdClient {}

// ── JSON serialisation helpers (proto types have fromJson only) ───────────────

Map<String, dynamic> _sessionJson(Session s) => {
      'id': s.id,
      'repoPath': s.repoPath,
      'title': s.title,
      'provider': s.provider.name,
      'status': s.status.name,
      'createdAt': s.createdAt.toIso8601String(),
      'updatedAt': s.updatedAt.toIso8601String(),
      'messageCount': s.messageCount,
    };

Map<String, dynamic> _msgJson(Message m) => {
      'id': m.id,
      'sessionId': m.sessionId,
      'role': m.role.name,
      'content': m.content,
      'status': m.status,
      'createdAt': m.createdAt.toIso8601String(),
    };

// ── Helpers ───────────────────────────────────────────────────────────────────

/// Creates a minimal connected DaemonState with a mock client injected.
///
/// [callHandler] is called for every `client.call()` invocation.  It receives
/// the method name and optional params and must return the decoded JSON result
/// (the same type that `ClawdClient.call<T>` would return).
ProviderContainer buildContainer({
  required MockClawdClient mockClient,
  List<Override> extraOverrides = const [],
  Stream<Map<String, dynamic>>? pushEvents,
}) {
  // Expose a push-events stream we can control from tests.
  final effectivePushEvents =
      pushEvents ?? const Stream<Map<String, dynamic>>.empty();

  return ProviderContainer(
    overrides: [
      // Inject our mock client so providers that call
      // `ref.read(daemonProvider.notifier).client` get the mock.
      daemonProvider.overrideWith(() => _MockDaemonNotifier(mockClient)),
      // Expose a controllable push-events stream.
      daemonPushEventsProvider.overrideWith((_) => effectivePushEvents),
      // bootstrapToken not needed — mock client ignores auth.
      bootstrapTokenProvider.overrideWithValue(null),
      ...extraOverrides,
    ],
  );
}

/// A DaemonNotifier that returns a pre-built connected state with
/// the supplied mock client, without touching any real WebSocket.
class _MockDaemonNotifier extends DaemonNotifier {
  _MockDaemonNotifier(this._mockClient);

  final MockClawdClient _mockClient;

  @override
  DaemonState build() => const DaemonState(status: DaemonStatus.connected);

  @override
  ClawdClient get client => _mockClient;
}

// ── AppSettings — pure unit tests ─────────────────────────────────────────────

void main() {
  group('AppSettings', () {
    test('uses correct defaults', () {
      const s = AppSettings();
      expect(s.daemonUrl, 'ws://127.0.0.1:4300');
      expect(s.defaultProvider, ProviderType.claude);
      expect(s.autoReconnect, true);
      expect(s.theme, 'dark');
    });

    test('copyWith replaces only the specified fields', () {
      const original = AppSettings(daemonUrl: 'ws://x:4300', theme: 'light');
      final updated = original.copyWith(theme: 'dark');
      expect(updated.daemonUrl, 'ws://x:4300');
      expect(updated.theme, 'dark');
      expect(updated.autoReconnect, true); // unchanged
    });

    test('copyWith with no args returns equivalent object', () {
      const original = AppSettings(
        daemonUrl: 'ws://y:4300',
        theme: 'light',
        autoReconnect: false,
        defaultProvider: ProviderType.codex,
      );
      final copy = original.copyWith();
      expect(copy.daemonUrl, original.daemonUrl);
      expect(copy.theme, original.theme);
      expect(copy.autoReconnect, original.autoReconnect);
      expect(copy.defaultProvider, original.defaultProvider);
    });
  });

  // ── SettingsNotifier ──────────────────────────────────────────────────────

  group('SettingsNotifier', () {
    setUp(() {
      SharedPreferences.setMockInitialValues({});
    });

    test('loads defaults when SharedPreferences is empty', () async {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final settings = await container.read(settingsProvider.future);
      expect(settings.daemonUrl, 'ws://127.0.0.1:4300');
      expect(settings.defaultProvider, ProviderType.claude);
      expect(settings.autoReconnect, true);
      expect(settings.theme, 'dark');
    });

    test('loads stored daemon URL from SharedPreferences', () async {
      SharedPreferences.setMockInitialValues({
        'settings.daemon_url': 'ws://192.168.1.5:4300',
      });
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final settings = await container.read(settingsProvider.future);
      expect(settings.daemonUrl, 'ws://192.168.1.5:4300');
    });

    test('setDaemonUrl persists to SharedPreferences and updates state',
        () async {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      await container.read(settingsProvider.future);
      await container
          .read(settingsProvider.notifier)
          .setDaemonUrl('ws://10.0.0.1:4300');

      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('settings.daemon_url'), 'ws://10.0.0.1:4300');
      expect(
        container.read(settingsProvider).valueOrNull?.daemonUrl,
        'ws://10.0.0.1:4300',
      );
    });

    test('setDefaultProvider persists and updates state', () async {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      await container.read(settingsProvider.future);
      await container
          .read(settingsProvider.notifier)
          .setDefaultProvider(ProviderType.codex);

      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('settings.default_provider'), 'codex');
      expect(
        container.read(settingsProvider).valueOrNull?.defaultProvider,
        ProviderType.codex,
      );
    });

    test('setAutoReconnect persists and updates state', () async {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      await container.read(settingsProvider.future);
      await container
          .read(settingsProvider.notifier)
          .setAutoReconnect(false);

      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getBool('settings.auto_reconnect'), false);
      expect(
        container.read(settingsProvider).valueOrNull?.autoReconnect,
        false,
      );
    });

    test('setTheme persists and updates state', () async {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      await container.read(settingsProvider.future);
      await container.read(settingsProvider.notifier).setTheme('light');

      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('settings.theme'), 'light');
      expect(
        container.read(settingsProvider).valueOrNull?.theme,
        'light',
      );
    });

    test('unknown provider name falls back to claude', () async {
      SharedPreferences.setMockInitialValues({
        'settings.default_provider': 'unknown_provider',
      });
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final settings = await container.read(settingsProvider.future);
      expect(settings.defaultProvider, ProviderType.claude);
    });
  });

  // ── SessionListNotifier ───────────────────────────────────────────────────

  group('SessionListNotifier', () {
    late MockClawdClient mockClient;

    setUp(() {
      mockClient = MockClawdClient();
      SharedPreferences.setMockInitialValues({});
    });

    Session _makeSession({
      String id = 'sess1',
      SessionStatus status = SessionStatus.idle,
    }) =>
        Session(
          id: id,
          repoPath: '/tmp/repo',
          title: '',
          provider: ProviderType.claude,
          status: status,
          createdAt: DateTime(2026),
          updatedAt: DateTime(2026),
          messageCount: 0,
        );

    test('initial load calls session.list and populates state', () async {
      final session = _makeSession();
      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async => [_sessionJson(session)]);

      final container = buildContainer(mockClient: mockClient);
      addTearDown(container.dispose);

      final sessions =
          await container.read(sessionListProvider.future);
      expect(sessions.length, 1);
      expect(sessions.first.id, 'sess1');
    });

    test('session.statusChanged push event updates status optimistically',
        () async {
      final session = _makeSession();
      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async => [_sessionJson(session)]);

      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      // Wait for initial load.
      await container.read(sessionListProvider.future);

      // Emit status-changed push event.
      pushController.add({
        'method': 'session.statusChanged',
        'params': {'sessionId': 'sess1', 'status': 'running'},
      });
      await Future<void>.delayed(Duration.zero);

      final sessions = container.read(sessionListProvider).valueOrNull;
      expect(sessions?.first.status, SessionStatus.running);
    });

    test('session.statusChanged for unknown sessionId is ignored', () async {
      final session = _makeSession();
      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async => [_sessionJson(session)]);

      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(sessionListProvider.future);

      // Event for a different session — state must not change.
      pushController.add({
        'method': 'session.statusChanged',
        'params': {'sessionId': 'other_sess', 'status': 'running'},
      });
      await Future<void>.delayed(Duration.zero);

      final sessions = container.read(sessionListProvider).valueOrNull;
      expect(sessions?.first.status, SessionStatus.idle);
    });

    test('session.statusChanged with unknown status is ignored gracefully',
        () async {
      final session = _makeSession();
      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async => [_sessionJson(session)]);

      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(sessionListProvider.future);

      pushController.add({
        'method': 'session.statusChanged',
        'params': {'sessionId': 'sess1', 'status': 'totally_unknown_xyz'},
      });
      await Future<void>.delayed(Duration.zero);

      final sessions = container.read(sessionListProvider).valueOrNull;
      // Status must remain idle — unknown status is silently skipped.
      expect(sessions?.first.status, SessionStatus.idle);
    });

    test('create calls session.create and refreshes list', () async {
      final existing = _makeSession();
      final created = _makeSession(id: 'sess2', status: SessionStatus.idle);

      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async => [_sessionJson(existing)]);

      when(() => mockClient.call<Map<String, dynamic>>(
            'session.create',
            any(),
          )).thenAnswer((_) async => _sessionJson(created));

      // After create() calls refresh(), session.list returns both sessions.
      int listCallCount = 0;
      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async {
        listCallCount++;
        return listCallCount == 1
            ? [_sessionJson(existing)]
            : [_sessionJson(existing), _sessionJson(created)];
      });

      final container = buildContainer(mockClient: mockClient);
      addTearDown(container.dispose);

      await container.read(sessionListProvider.future);

      await container.read(sessionListProvider.notifier).create(
            repoPath: '/tmp/repo',
            provider: ProviderType.claude,
          );

      final sessions = container.read(sessionListProvider).valueOrNull;
      expect(sessions?.length, 2);
      expect(sessions?.any((s) => s.id == 'sess2'), true);
    });

    test('delete calls session.delete and refreshes list', () async {
      final session = _makeSession();
      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async => [_sessionJson(session)]);
      when(() => mockClient.call<void>(
            'session.delete',
            any(),
          )).thenAnswer((_) async {});

      // After delete, list returns empty.
      int listCallCount = 0;
      when(() => mockClient.call<List<dynamic>>(
            'session.list',
            any(),
          )).thenAnswer((_) async {
        listCallCount++;
        return listCallCount == 1 ? [_sessionJson(session)] : [];
      });

      final container = buildContainer(mockClient: mockClient);
      addTearDown(container.dispose);

      await container.read(sessionListProvider.future);
      await container.read(sessionListProvider.notifier).delete('sess1');

      verify(() => mockClient.call<void>('session.delete', {'sessionId': 'sess1'}));
      final sessions = container.read(sessionListProvider).valueOrNull;
      expect(sessions, isEmpty);
    });
  });

  // ── ToolCallNotifier ──────────────────────────────────────────────────────

  group('ToolCallNotifier', () {
    late MockClawdClient mockClient;

    setUp(() {
      mockClient = MockClawdClient();
      SharedPreferences.setMockInitialValues({});
    });

    ToolCall _makeTc({
      String id = 'tc1',
      ToolCallStatus status = ToolCallStatus.pending,
    }) =>
        ToolCall(
          id: id,
          sessionId: 'sess1',
          toolName: 'readFile',
          input: const {},
          status: status,
          createdAt: DateTime(2026),
        );

    test('starts with empty list', () async {
      final container = buildContainer(mockClient: mockClient);
      addTearDown(container.dispose);

      final calls = await container.read(toolCallProvider('sess1').future);
      expect(calls, isEmpty);
    });

    test('session.toolCallCreated push event adds tool call', () async {
      final tc = _makeTc();
      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(toolCallProvider('sess1').future);

      pushController.add({
        'method': 'session.toolCallCreated',
        'params': {
          'sessionId': 'sess1',
          'toolCall': {
            'id': tc.id,
            'toolName': tc.toolName,
            'input': tc.input,
            'status': 'pending',
            'createdAt': tc.createdAt.toIso8601String(),
          },
        },
      });
      await Future<void>.delayed(Duration.zero);

      final calls = container.read(toolCallProvider('sess1')).valueOrNull;
      expect(calls?.length, 1);
      expect(calls?.first.id, 'tc1');
      expect(calls?.first.status, ToolCallStatus.pending);
    });

    test('session.toolCallCreated for different session is ignored', () async {
      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(toolCallProvider('sess1').future);

      pushController.add({
        'method': 'session.toolCallCreated',
        'params': {
          'sessionId': 'OTHER_SESSION',
          'toolCall': {
            'id': 'tc_other',
            'toolName': 'readFile',
            'input': {},
            'status': 'pending',
            'createdAt': DateTime(2026).toIso8601String(),
          },
        },
      });
      await Future<void>.delayed(Duration.zero);

      final calls = container.read(toolCallProvider('sess1')).valueOrNull;
      expect(calls, isEmpty);
    });

    test('session.toolCallUpdated updates tool call status', () async {
      final tc = _makeTc();
      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(toolCallProvider('sess1').future);

      // First add the tool call.
      pushController.add({
        'method': 'session.toolCallCreated',
        'params': {
          'sessionId': 'sess1',
          'toolCall': {
            'id': tc.id,
            'toolName': tc.toolName,
            'input': tc.input,
            'status': 'pending',
            'createdAt': tc.createdAt.toIso8601String(),
          },
        },
      });
      await Future<void>.delayed(Duration.zero);

      // Then update its status to completed.
      pushController.add({
        'method': 'session.toolCallUpdated',
        'params': {
          'sessionId': 'sess1',
          'toolCallId': 'tc1',
          'status': 'done',
        },
      });
      await Future<void>.delayed(Duration.zero);

      final calls = container.read(toolCallProvider('sess1')).valueOrNull;
      expect(calls?.first.status, ToolCallStatus.completed);
    });

    test('approve calls tool.approve RPC', () async {
      when(() => mockClient.call<void>('tool.approve', any()))
          .thenAnswer((_) async {});

      final container = buildContainer(mockClient: mockClient);
      addTearDown(container.dispose);

      await container.read(toolCallProvider('sess1').future);
      await container.read(toolCallProvider('sess1').notifier).approve('tc1');

      verify(() => mockClient.call<void>('tool.approve', {
            'sessionId': 'sess1',
            'toolCallId': 'tc1',
          }));
    });

    test('reject calls tool.reject RPC', () async {
      when(() => mockClient.call<void>('tool.reject', any()))
          .thenAnswer((_) async {});

      final container = buildContainer(mockClient: mockClient);
      addTearDown(container.dispose);

      await container.read(toolCallProvider('sess1').future);
      await container.read(toolCallProvider('sess1').notifier).reject('tc1');

      verify(() => mockClient.call<void>('tool.reject', {
            'sessionId': 'sess1',
            'toolCallId': 'tc1',
          }));
    });
  });

  // ── MessageListNotifier ───────────────────────────────────────────────────

  group('MessageListNotifier', () {
    late MockClawdClient mockClient;

    setUp(() {
      mockClient = MockClawdClient();
      SharedPreferences.setMockInitialValues({});
    });

    Message _makeMsg({
      String id = 'msg1',
      String content = 'Hello',
      MessageRole role = MessageRole.user,
    }) =>
        Message(
          id: id,
          sessionId: 'sess1',
          role: role,
          content: content,
          status: 'done',
          createdAt: DateTime(2026),
        );

    test('initial load calls session.getMessages', () async {
      final msg = _makeMsg();
      when(() => mockClient.call<List<dynamic>>(
            'session.getMessages',
            any(),
          )).thenAnswer((_) async => [_msgJson(msg)]);

      final container = buildContainer(mockClient: mockClient);
      addTearDown(container.dispose);

      final messages =
          await container.read(messageListProvider('sess1').future);
      expect(messages.length, 1);
      expect(messages.first.id, 'msg1');
    });

    test('session.messageCreated push event appends message', () async {
      when(() => mockClient.call<List<dynamic>>(
            'session.getMessages',
            any(),
          )).thenAnswer((_) async => []);

      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(messageListProvider('sess1').future);

      final newMsg = _makeMsg(id: 'msg2', content: 'World');
      pushController.add({
        'method': 'session.messageCreated',
        'params': {
          'sessionId': 'sess1',
          'message': _msgJson(newMsg),
        },
      });
      await Future<void>.delayed(Duration.zero);

      final messages =
          container.read(messageListProvider('sess1')).valueOrNull;
      expect(messages?.length, 1);
      expect(messages?.first.id, 'msg2');
    });

    test('session.messageCreated for different session is ignored', () async {
      when(() => mockClient.call<List<dynamic>>(
            'session.getMessages',
            any(),
          )).thenAnswer((_) async => []);

      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(messageListProvider('sess1').future);

      final msg = _makeMsg(id: 'msg_other');
      pushController.add({
        'method': 'session.messageCreated',
        'params': {
          'sessionId': 'OTHER_SESSION',
          'message': _msgJson(msg),
        },
      });
      await Future<void>.delayed(Duration.zero);

      final messages =
          container.read(messageListProvider('sess1')).valueOrNull;
      expect(messages, isEmpty);
    });

    test('session.messageUpdated push event updates message content', () async {
      final msg = _makeMsg();
      when(() => mockClient.call<List<dynamic>>(
            'session.getMessages',
            any(),
          )).thenAnswer((_) async => [_msgJson(msg)]);

      final pushController =
          StreamController<Map<String, dynamic>>.broadcast();
      addTearDown(pushController.close);

      final container = buildContainer(
        mockClient: mockClient,
        pushEvents: pushController.stream,
      );
      addTearDown(container.dispose);

      await container.read(messageListProvider('sess1').future);

      pushController.add({
        'method': 'session.messageUpdated',
        'params': {
          'sessionId': 'sess1',
          'messageId': 'msg1',
          'content': 'Updated content',
        },
      });
      // Wait for the 80ms streaming debounce buffer to flush.
      await Future<void>.delayed(const Duration(milliseconds: 150));

      final messages =
          container.read(messageListProvider('sess1')).valueOrNull;
      expect(messages?.first.content, 'Updated content');
    });
  });
}
