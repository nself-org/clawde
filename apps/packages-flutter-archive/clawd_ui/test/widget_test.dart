// QA-03: Widget tests for all clawd_ui widgets.
// Covers: ChatBubble, SessionListTile, ToolCallCard, MessageInput,
//         ConnectionStatusIndicator, EmptyState, ErrorState.
import 'package:clawd_core/clawd_core.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'package:clawd_ui/clawd_ui.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

// ── Helpers ───────────────────────────────────────────────────────────────────

/// Wraps a widget in a minimal MaterialApp so it has Scaffold/Directionality.
Widget _wrap(Widget child, {List<Override> overrides = const []}) {
  return ProviderScope(
    overrides: overrides,
    child: MaterialApp(
      theme: ThemeData.dark(),
      home: Scaffold(body: child),
    ),
  );
}

/// Fake [DaemonNotifier] that exposes a fixed [DaemonState] for widget tests.
class _FakeDaemonNotifier extends DaemonNotifier {
  _FakeDaemonNotifier(this._fixed);
  final DaemonState _fixed;

  @override
  DaemonState build() => _fixed;

  @override
  Future<void> reconnect() async {} // no-op
}

/// Creates a [DaemonNotifier] override with the given [DaemonState].
Override _daemonOverride(DaemonState state) =>
    daemonProvider.overrideWith(() => _FakeDaemonNotifier(state));

// ── Test data ─────────────────────────────────────────────────────────────────

Message _userMsg({String content = 'Hello, AI!'}) => Message(
      id: 'msg-1',
      sessionId: 'sess-1',
      role: MessageRole.user,
      content: content,
      status: 'done',
      createdAt: DateTime(2024),
    );

Message _assistantMsg({String content = '**Hello** `code`'}) => Message(
      id: 'msg-2',
      sessionId: 'sess-1',
      role: MessageRole.assistant,
      content: content,
      status: 'done',
      createdAt: DateTime(2024),
    );

Session _session({
  String repoPath = '/home/user/myapp',
  SessionStatus status = SessionStatus.running,
}) =>
    Session(
      id: 'sess-1',
      repoPath: repoPath,
      title: 'Test session',
      provider: ProviderType.claude,
      status: status,
      createdAt: DateTime(2024),
      updatedAt: DateTime(2024),
      messageCount: 0,
    );

ToolCall _toolCall({ToolCallStatus status = ToolCallStatus.pending}) => ToolCall(
      id: 'tc-1',
      sessionId: 'sess-1',
      messageId: 'msg-1',
      toolName: 'bash',
      input: {'command': 'ls -la'},
      status: status,
      createdAt: DateTime(2024),
    );

// ── ChatBubble ────────────────────────────────────────────────────────────────

void main() {
  group('ChatBubble', () {
    testWidgets('user message is right-aligned', (tester) async {
      await tester.pumpWidget(_wrap(ChatBubble(message: _userMsg())));
      await tester.pump();

      final row = tester.widget<Row>(
        find.descendant(
          of: find.byType(ChatBubble),
          matching: find.byType(Row),
        ).first,
      );
      expect(row.mainAxisAlignment, MainAxisAlignment.end);
    });

    testWidgets('assistant message is left-aligned', (tester) async {
      await tester.pumpWidget(_wrap(ChatBubble(message: _assistantMsg())));
      await tester.pump();

      final row = tester.widget<Row>(
        find.descendant(
          of: find.byType(ChatBubble),
          matching: find.byType(Row),
        ).first,
      );
      expect(row.mainAxisAlignment, MainAxisAlignment.start);
    });

    testWidgets('user message renders plain text', (tester) async {
      await tester.pumpWidget(_wrap(ChatBubble(message: _userMsg(content: 'Hello!'))));
      await tester.pump();

      expect(find.text('Hello!'), findsOneWidget);
    });

    testWidgets('assistant message renders via MarkdownMessage', (tester) async {
      await tester.pumpWidget(
        _wrap(ChatBubble(message: _assistantMsg(content: '**bold** text'))),
      );
      await tester.pump();

      // MarkdownMessage widget should be present for assistant messages.
      expect(find.byType(MarkdownMessage), findsOneWidget);
    });
  });

  // ── SessionListTile ─────────────────────────────────────────────────────────

  group('SessionListTile', () {
    testWidgets('displays repo name from path', (tester) async {
      await tester.pumpWidget(
        _wrap(SessionListTile(session: _session(repoPath: '/home/user/myapp'))),
      );
      await tester.pump();

      expect(find.text('myapp'), findsOneWidget);
    });

    testWidgets('onTap is called when tapped', (tester) async {
      var tapped = false;
      await tester.pumpWidget(
        _wrap(SessionListTile(
          session: _session(),
          onTap: () => tapped = true,
        )),
      );
      await tester.pump();

      await tester.tap(find.byType(ListTile));
      expect(tapped, isTrue);
    });

    testWidgets('selected state is applied to ListTile', (tester) async {
      await tester.pumpWidget(
        _wrap(SessionListTile(session: _session(), isSelected: true)),
      );
      await tester.pump();

      final tile = tester.widget<ListTile>(find.byType(ListTile));
      expect(tile.selected, isTrue);
    });

    testWidgets('non-selected tile is not selected', (tester) async {
      await tester.pumpWidget(
        _wrap(SessionListTile(session: _session(), isSelected: false)),
      );
      await tester.pump();

      final tile = tester.widget<ListTile>(find.byType(ListTile));
      expect(tile.selected, isFalse);
    });
  });

  // ── ToolCallCard ────────────────────────────────────────────────────────────

  group('ToolCallCard', () {
    testWidgets('shows approve and reject buttons for pending tool call',
        (tester) async {
      var approved = false;
      var rejected = false;
      await tester.pumpWidget(
        _wrap(ToolCallCard(
          toolCall: _toolCall(status: ToolCallStatus.pending),
          onApprove: () => approved = true,
          onReject: () => rejected = true,
        )),
      );
      await tester.pump();

      expect(find.text('Approve'), findsOneWidget);
      expect(find.text('Reject'), findsOneWidget);

      await tester.tap(find.text('Approve'));
      expect(approved, isTrue);

      await tester.tap(find.text('Reject'));
      expect(rejected, isTrue);
    });

    testWidgets('hides approve/reject for non-pending tool call', (tester) async {
      await tester.pumpWidget(
        _wrap(ToolCallCard(
          toolCall: _toolCall(status: ToolCallStatus.completed),
          onApprove: () {},
          onReject: () {},
        )),
      );
      await tester.pump();

      expect(find.text('Approve'), findsNothing);
      expect(find.text('Reject'), findsNothing);
    });

    testWidgets('shows tool name', (tester) async {
      await tester.pumpWidget(
        _wrap(ToolCallCard(toolCall: _toolCall())),
      );
      await tester.pump();

      expect(find.text('bash'), findsOneWidget);
    });
  });

  // ── MessageInput ────────────────────────────────────────────────────────────

  group('MessageInput', () {
    testWidgets('onSend is called when send button is tapped', (tester) async {
      String? sent;
      await tester.pumpWidget(
        _wrap(MessageInput(onSend: (t) => sent = t)),
      );
      await tester.pump();

      await tester.enterText(find.byType(TextField), 'Hello daemon');
      await tester.tap(find.byType(IconButton));
      await tester.pump();

      expect(sent, 'Hello daemon');
    });

    testWidgets('send button is disabled when isLoading is true', (tester) async {
      var sent = false;
      await tester.pumpWidget(
        _wrap(MessageInput(isLoading: true, onSend: (_) => sent = true)),
      );
      await tester.pump();

      final btn = tester.widget<IconButton>(find.byType(IconButton));
      expect(btn.onPressed, isNull);
      expect(sent, isFalse);
    });

    testWidgets('send button is disabled when enabled is false', (tester) async {
      var sent = false;
      await tester.pumpWidget(
        _wrap(MessageInput(enabled: false, onSend: (_) => sent = true)),
      );
      await tester.pump();

      await tester.enterText(find.byType(TextField), 'test');
      final btn = tester.widget<IconButton>(find.byType(IconButton));
      expect(btn.onPressed, isNull);
      expect(sent, isFalse);
    });
  });

  // ── ConnectionStatusIndicator ───────────────────────────────────────────────

  group('ConnectionStatusIndicator', () {
    testWidgets('shows Connected when daemon is connected', (tester) async {
      await tester.pumpWidget(
        _wrap(
          const ConnectionStatusIndicator(),
          overrides: [
            _daemonOverride(const DaemonState(status: DaemonStatus.connected)),
          ],
        ),
      );
      await tester.pump();

      expect(find.text('Connected'), findsOneWidget);
    });

    testWidgets('shows Connecting when daemon is connecting', (tester) async {
      await tester.pumpWidget(
        _wrap(
          const ConnectionStatusIndicator(),
          overrides: [
            _daemonOverride(const DaemonState(status: DaemonStatus.connecting)),
          ],
        ),
      );
      await tester.pump();

      expect(find.text('Connecting…'), findsOneWidget);
    });

    testWidgets('shows retry label when reconnectAttempt > 0', (tester) async {
      await tester.pumpWidget(
        _wrap(
          const ConnectionStatusIndicator(),
          overrides: [
            _daemonOverride(const DaemonState(
              status: DaemonStatus.connecting,
              reconnectAttempt: 3,
            )),
          ],
        ),
      );
      await tester.pump();

      expect(find.text('Retry #3…'), findsOneWidget);
    });

    testWidgets('shows Offline when disconnected', (tester) async {
      await tester.pumpWidget(
        _wrap(
          const ConnectionStatusIndicator(),
          overrides: [
            _daemonOverride(
                const DaemonState(status: DaemonStatus.disconnected)),
          ],
        ),
      );
      await tester.pump();

      expect(find.text('Offline – tap'), findsOneWidget);
    });
  });

  // ── EmptyState ──────────────────────────────────────────────────────────────

  group('EmptyState', () {
    testWidgets('shows icon, title, and no subtitle by default', (tester) async {
      await tester.pumpWidget(
        _wrap(const EmptyState(
          icon: Icons.inbox,
          title: 'Nothing here',
        )),
      );
      await tester.pump();

      expect(find.text('Nothing here'), findsOneWidget);
      expect(find.byIcon(Icons.inbox), findsOneWidget);
    });

    testWidgets('shows subtitle when provided', (tester) async {
      await tester.pumpWidget(
        _wrap(const EmptyState(
          icon: Icons.inbox,
          title: 'Nothing here',
          subtitle: 'Try adding something',
        )),
      );
      await tester.pump();

      expect(find.text('Try adding something'), findsOneWidget);
    });

    testWidgets('action button calls onAction callback', (tester) async {
      var tapped = false;
      await tester.pumpWidget(
        _wrap(EmptyState(
          icon: Icons.inbox,
          title: 'Nothing here',
          actionLabel: 'Add item',
          onAction: () => tapped = true,
        )),
      );
      await tester.pump();

      await tester.tap(find.text('Add item'));
      expect(tapped, isTrue);
    });

    testWidgets('no action button when onAction is null', (tester) async {
      await tester.pumpWidget(
        _wrap(const EmptyState(
          icon: Icons.inbox,
          title: 'Nothing here',
          actionLabel: 'Add item',
          // onAction intentionally null
        )),
      );
      await tester.pump();

      expect(find.byType(FilledButton), findsNothing);
    });
  });

  // ── ErrorState ──────────────────────────────────────────────────────────────

  group('ErrorState', () {
    testWidgets('shows icon and title', (tester) async {
      await tester.pumpWidget(
        _wrap(const ErrorState(
          icon: Icons.error_outline,
          title: 'Something went wrong',
        )),
      );
      await tester.pump();

      expect(find.text('Something went wrong'), findsOneWidget);
      expect(find.byIcon(Icons.error_outline), findsOneWidget);
    });

    testWidgets('shows description when provided', (tester) async {
      await tester.pumpWidget(
        _wrap(const ErrorState(
          icon: Icons.error_outline,
          title: 'Error',
          description: 'Check your connection',
        )),
      );
      await tester.pump();

      expect(find.text('Check your connection'), findsOneWidget);
    });

    testWidgets('retry button calls onRetry callback', (tester) async {
      var retried = false;
      await tester.pumpWidget(
        _wrap(ErrorState(
          icon: Icons.error_outline,
          title: 'Error',
          onRetry: () => retried = true,
        )),
      );
      await tester.pump();

      await tester.tap(find.text('Retry'));
      expect(retried, isTrue);
    });

    testWidgets('no retry button when onRetry is null', (tester) async {
      await tester.pumpWidget(
        _wrap(const ErrorState(
          icon: Icons.error_outline,
          title: 'Error',
        )),
      );
      await tester.pump();

      expect(find.text('Retry'), findsNothing);
    });
  });

  // ── TokenUsagePanel — MI.T29 ─────────────────────────────────────────────

  group('TokenUsagePanel', () {
    testWidgets('shows formatted cost in collapsed header', (tester) async {
      await tester.pumpWidget(_wrap(const TokenUsagePanel(
        inputTokens: 1500,
        outputTokens: 750,
        estimatedCostUsd: 0.0123,
      )));
      await tester.pump();

      // Cost string rendered in header.
      expect(find.textContaining('0.0123'), findsOneWidget);
      // 1500 tokens → '1.5k' (input stat label value).
      expect(find.textContaining('1.5k'), findsOneWidget);
    });

    testWidgets('collapsed by default when no warnings', (tester) async {
      await tester.pumpWidget(_wrap(const TokenUsagePanel(
        inputTokens: 100,
        outputTokens: 50,
      )));
      await tester.pump();

      // Expanded detail rows not present when collapsed.
      expect(find.text('Input tokens'), findsNothing);
      expect(find.text('Session cost'), findsNothing);
    });

    testWidgets('auto-expands when budgetWarning is true', (tester) async {
      await tester.pumpWidget(_wrap(const TokenUsagePanel(
        budgetWarning: true,
        monthlySpendUsd: 8.0,
        monthlyCap: 10.0,
      )));
      await tester.pump();

      // Detail rows visible when auto-expanded.
      expect(find.text('Input tokens'), findsOneWidget);
      // Warning label shown in header.
      expect(find.text('Budget warning'), findsOneWidget);
    });

    testWidgets('shows Budget exceeded label when budgetExceeded', (tester) async {
      await tester.pumpWidget(_wrap(const TokenUsagePanel(
        budgetExceeded: true,
        monthlySpendUsd: 12.0,
        monthlyCap: 10.0,
      )));
      await tester.pump();

      expect(find.text('Budget exceeded'), findsOneWidget);
      // Expanded — detail rows visible too.
      expect(find.text('Input tokens'), findsOneWidget);
    });

    testWidgets('tap header toggles expanded state', (tester) async {
      await tester.pumpWidget(_wrap(const TokenUsagePanel(
        inputTokens: 500,
      )));
      await tester.pump();

      // Collapsed initially.
      expect(find.text('Input tokens'), findsNothing);

      // Tap the GestureDetector in the panel header.
      final headerGesture = find.descendant(
        of: find.byType(TokenUsagePanel),
        matching: find.byType(GestureDetector),
      ).first;
      await tester.tap(headerGesture);
      await tester.pump();

      // Now expanded.
      expect(find.text('Input tokens'), findsOneWidget);

      // Tap again to collapse.
      await tester.tap(headerGesture);
      await tester.pump();

      expect(find.text('Input tokens'), findsNothing);
    });

    testWidgets('shows monthly cap detail rows when monthlyCap provided', (tester) async {
      await tester.pumpWidget(_wrap(const TokenUsagePanel(
        budgetWarning: true,
        monthlySpendUsd: 8.5,
        monthlyCap: 10.0,
      )));
      await tester.pump();

      expect(find.text('Monthly spend'), findsOneWidget);
      expect(find.text('Monthly cap'), findsOneWidget);
      expect(find.textContaining('10.00'), findsOneWidget);
    });

    testWidgets('no monthly detail rows when monthlyCap is null', (tester) async {
      await tester.pumpWidget(_wrap(const TokenUsagePanel(
        monthlySpendUsd: 5.0,
        budgetWarning: true, // ensures expanded
      )));
      await tester.pump();

      expect(find.text('Monthly spend'), findsNothing);
      expect(find.text('Monthly cap'), findsNothing);
    });
  });

  // ── ModelChip — MI.T29 ────────────────────────────────────────────────────

  group('ModelChip', () {
    testWidgets('hidden when modelOverride is null', (tester) async {
      await tester.pumpWidget(_wrap(const ModelChip(modelOverride: null)));
      await tester.pump();

      expect(find.text('sonnet'), findsNothing);
      expect(find.text('opus'), findsNothing);
      expect(find.text('haiku'), findsNothing);
    });

    testWidgets('shows "sonnet" for claude-sonnet-4-6', (tester) async {
      await tester.pumpWidget(_wrap(const ModelChip(
        modelOverride: 'claude-sonnet-4-6',
      )));
      await tester.pump();

      expect(find.text('sonnet'), findsOneWidget);
    });

    testWidgets('shows "opus" for claude-opus model', (tester) async {
      await tester.pumpWidget(_wrap(const ModelChip(
        modelOverride: 'claude-opus-4-6',
      )));
      await tester.pump();

      expect(find.text('opus'), findsOneWidget);
    });

    testWidgets('shows "haiku" for haiku model', (tester) async {
      await tester.pumpWidget(_wrap(const ModelChip(
        modelOverride: 'claude-haiku-4-5-20251001',
      )));
      await tester.pump();

      expect(find.text('haiku'), findsOneWidget);
    });

    testWidgets('onTap fires when chip is tapped', (tester) async {
      var tapped = false;
      await tester.pumpWidget(_wrap(ModelChip(
        modelOverride: 'claude-sonnet-4-6',
        onTap: () => tapped = true,
      )));
      await tester.pump();

      await tester.tap(find.byType(InkWell));
      expect(tapped, isTrue);
    });

    testWidgets('truncates unknown model ID longer than 10 chars', (tester) async {
      await tester.pumpWidget(_wrap(const ModelChip(
        modelOverride: 'gpt-4o-ultra-max',
      )));
      await tester.pump();

      // First 10 chars of 'gpt-4o-ultra-max' = 'gpt-4o-ult' then '…'
      expect(find.text('gpt-4o-ult\u2026'), findsOneWidget);
    });

    testWidgets('short unknown model shown as-is', (tester) async {
      await tester.pumpWidget(_wrap(const ModelChip(
        modelOverride: 'gpt-4o',
      )));
      await tester.pump();

      expect(find.text('gpt-4o'), findsOneWidget);
    });
  });

  // ── ModelIndicator — MI.T29 ───────────────────────────────────────────────

  group('ModelIndicator', () {
    testWidgets('hidden when modelOverride is null', (tester) async {
      await tester.pumpWidget(_wrap(const ModelIndicator(modelOverride: null)));
      await tester.pump();

      expect(find.text('haiku'), findsNothing);
      expect(find.text('sonnet'), findsNothing);
    });

    testWidgets('shows "haiku" label for haiku model', (tester) async {
      await tester.pumpWidget(_wrap(const ModelIndicator(
        modelOverride: 'claude-haiku-4-5-20251001',
      )));
      await tester.pump();

      expect(find.text('haiku'), findsOneWidget);
    });

    testWidgets('shows "sonnet" label for sonnet model', (tester) async {
      await tester.pumpWidget(_wrap(const ModelIndicator(
        modelOverride: 'claude-sonnet-4-6',
      )));
      await tester.pump();

      expect(find.text('sonnet'), findsOneWidget);
    });
  });
}
