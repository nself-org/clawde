import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'package:clawd_ui/clawd_ui.dart';
import 'package:clawde/features/chat/diff_review.dart';

// ─── List item union ──────────────────────────────────────────────────────────

sealed class _ListItem {
  DateTime get sortKey;
}

final class _MessageItem extends _ListItem {
  _MessageItem(this.message);
  final Message message;
  @override
  DateTime get sortKey => message.createdAt;
}

final class _FileEditItem extends _ListItem {
  _FileEditItem(this.toolCall);
  final ToolCall toolCall;
  @override
  DateTime get sortKey => toolCall.completedAt ?? toolCall.createdAt;
}

// ─── MessageList ─────────────────────────────────────────────────────────────

class MessageList extends ConsumerStatefulWidget {
  const MessageList({super.key, required this.sessionId});

  final String sessionId;

  @override
  ConsumerState<MessageList> createState() => _MessageListState();
}

class _MessageListState extends ConsumerState<MessageList> {
  final _scrollController = ScrollController();
  bool _loadingMore = false;

  // Tool names that produce file edits visible in the message list.
  static const _fileEditTools = {
    'Write',
    'Edit',
    'str_replace_editor',
    'create_file',
    'replace_all',
  };

  @override
  void initState() {
    super.initState();
    // Listen for new messages to auto-scroll. Must be in initState, not
    // build(), so the listener is registered exactly once per widget lifetime.
    ref.listenManual(messageListProvider(widget.sessionId), (prev, next) {
      final prevCount = prev?.valueOrNull?.length ?? 0;
      final nextCount = next.valueOrNull?.length ?? 0;
      if (nextCount > prevCount) _scrollToBottom();
    });

    // V02.T14 — scroll-up triggers loadMore (infinite scroll up for history).
    _scrollController.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        );
      }
    });
  }

  /// V02.T14 — fire loadMore when within 200px of the top.
  Future<void> _onScroll() async {
    if (_loadingMore) return;
    if (!_scrollController.hasClients) return;
    if (_scrollController.position.pixels <= 200) {
      setState(() => _loadingMore = true);
      await ref
          .read(messageListProvider(widget.sessionId).notifier)
          .loadMore();
      if (mounted) setState(() => _loadingMore = false);
    }
  }

  // ── File-edit helpers ──────────────────────────────────────────────────────

  static bool _isFileEditTool(String toolName) =>
      _fileEditTools.contains(toolName);

  static String? _filePathFromInput(Map<String, dynamic> input) =>
      (input['file_path'] ?? input['path']) as String?;

  static String _operationFromToolName(String toolName) => switch (toolName) {
        'Write' || 'create_file' => 'create',
        _ => 'edit',
      };

  /// Build a minimal unified diff from Edit-style inputs (old_string/new_string).
  static String? _diffFromInput(Map<String, dynamic> input) {
    final oldStr = input['old_string'] as String?;
    final newStr = input['new_string'] as String?;
    if (oldStr == null || newStr == null) return null;
    final buf = StringBuffer('@@ edit @@\n');
    for (final l in oldStr.split('\n')) { buf.writeln('-$l'); }
    for (final l in newStr.split('\n')) { buf.writeln('+$l'); }
    return buf.toString();
  }

  static (int added, int removed) _lineCountsFromInput(
      Map<String, dynamic> input, String toolName) {
    if (toolName == 'Write' || toolName == 'create_file') {
      final content = input['content'] as String? ?? '';
      return (content.split('\n').length, 0);
    }
    final oldStr = (input['old_string'] as String?) ?? '';
    final newStr = (input['new_string'] as String?) ?? '';
    return (newStr.split('\n').length, oldStr.split('\n').length);
  }

  Widget _buildFileEditCard(ToolCall tc, BuildContext context) {
    final path = _filePathFromInput(tc.input)!;
    final operation = _operationFromToolName(tc.toolName);
    final diff = _diffFromInput(tc.input);
    final (added, removed) = _lineCountsFromInput(tc.input, tc.toolName);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      child: FileEditCard(
        filePath: path,
        operation: operation,
        linesAdded: added,
        linesRemoved: removed,
        diffContent: diff,
        onOpenFullDiff: diff == null
            ? null
            : () {
                DiffReviewDialog.show(
                  context,
                  filePath: path,
                  diffContent: diff,
                );
              },
      ),
    );
  }

  // ── Build ──────────────────────────────────────────────────────────────────

  @override
  Widget build(BuildContext context) {
    final messagesAsync = ref.watch(messageListProvider(widget.sessionId));
    final toolCallsAsync = ref.watch(toolCallProvider(widget.sessionId));

    return messagesAsync.when(
      loading: () => const _SkeletonMessages(),
      error: (e, _) => ErrorState(
        icon: Icons.error_outline,
        title: 'Could not load messages',
        description: e.toString(),
        onRetry: () =>
            ref.refresh(messageListProvider(widget.sessionId)),
      ),
      data: (messages) {
        if (messages.isEmpty) {
          return const EmptyState(
            icon: Icons.chat_bubble_outline,
            title: 'No messages yet',
            subtitle: 'Send a message below',
          );
        }

        // UI.2 — merge messages and completed file-edit tool calls,
        // sorted chronologically so edits appear at the right point in history.
        final toolCalls = toolCallsAsync.valueOrNull ?? [];
        final fileEdits = toolCalls.where((tc) =>
            tc.status == ToolCallStatus.completed &&
            _isFileEditTool(tc.toolName) &&
            _filePathFromInput(tc.input) != null);

        final items = <_ListItem>[
          ...messages.map(_MessageItem.new),
          ...fileEdits.map(_FileEditItem.new),
        ]..sort((a, b) => a.sortKey.compareTo(b.sortKey));

        return Stack(
          children: [
            // PERF.1 — virtualized list with tuned cache extent for smooth
            // scrolling through 10,000+ messages without jank.
            ListView.builder(
              controller: _scrollController,
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: items.length,
              // Keep 1500px of items pre-rendered above/below viewport.
              scrollCacheExtent: const ScrollCacheExtent.pixels(1500),
              // Disable automatic keepAlives: messages are stateless,
              // no need to retain widget state when scrolled off-screen.
              addAutomaticKeepAlives: false,
              // Repaint boundaries prevent unaffected messages from
              // repainting when a single message updates.
              addRepaintBoundaries: true,
              itemBuilder: (context, i) => switch (items[i]) {
                _MessageItem(:final message) => ChatBubble(message: message),
                _FileEditItem(:final toolCall) =>
                  _buildFileEditCard(toolCall, context),
              },
            ),
            // V02.T14 — "loading older messages" indicator at top
            if (_loadingMore)
              Positioned(
                top: 8,
                left: 0,
                right: 0,
                child: Center(
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 6),
                    decoration: BoxDecoration(
                      color: Colors.black54,
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: const Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        SizedBox(
                          width: 12,
                          height: 12,
                          child: CircularProgressIndicator(
                            strokeWidth: 1.5,
                            color: Colors.white54,
                          ),
                        ),
                        SizedBox(width: 8),
                        Text(
                          'Loading older messages...',
                          style: TextStyle(
                            fontSize: 11,
                            color: Colors.white54,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
          ],
        );
      },
    );
  }
}

class _SkeletonMessages extends StatelessWidget {
  const _SkeletonMessages();

  @override
  Widget build(BuildContext context) {
    // PERF.1 — use ListView.builder even for skeletons to keep rendering path
    // consistent and avoid a full rebuild when real messages arrive.
    const skeletons = [
      _SkeletonBubble(isUser: false, width: 260),
      _SkeletonBubble(isUser: true, width: 180),
      _SkeletonBubble(isUser: false, width: 320),
    ];
    return ListView.builder(
      padding: const EdgeInsets.all(16),
      itemCount: skeletons.length * 2 - 1,
      itemBuilder: (_, i) =>
          i.isOdd ? const SizedBox(height: 8) : skeletons[i ~/ 2],
    );
  }
}

class _SkeletonBubble extends StatelessWidget {
  const _SkeletonBubble({required this.isUser, required this.width});
  final bool isUser;
  final double width;

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: isUser ? Alignment.centerRight : Alignment.centerLeft,
      child: Container(
        width: width,
        height: 48,
        margin: const EdgeInsets.symmetric(horizontal: 16),
        decoration: BoxDecoration(
          color: Colors.white10,
          borderRadius: BorderRadius.circular(12),
        ),
      ),
    );
  }
}
