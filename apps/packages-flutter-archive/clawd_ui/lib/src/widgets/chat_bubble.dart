import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';
import '../theme/clawd_theme.dart';
import 'markdown_message.dart';

/// Renders a single [Message] as a chat bubble.
/// User messages are right-aligned; assistant messages left-aligned.
class ChatBubble extends StatelessWidget {
  const ChatBubble({
    super.key,
    required this.message,
    this.onToolCallTap,
  });

  final Message message;

  /// Called when the user taps a tool call reference inside the bubble.
  final void Function(String toolCallId)? onToolCallTap;

  bool get _isUser => message.role == MessageRole.user;

  @override
  Widget build(BuildContext context) {
    // A11Y.1 — screen reader label for each message bubble.
    final roleLabel = _isUser ? 'You' : 'Assistant';
    final contentSnippet = message.content.length > 120
        ? '${message.content.substring(0, 120)}…'
        : message.content;
    return Semantics(
      label: '$roleLabel: $contentSnippet',
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
        child: Row(
          mainAxisAlignment:
              _isUser ? MainAxisAlignment.end : MainAxisAlignment.start,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (!_isUser) _Avatar(role: message.role),
            const SizedBox(width: 8),
            Flexible(
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                decoration: BoxDecoration(
                  color: _isUser
                      ? ClawdTheme.userBubble
                      : ClawdTheme.assistantBubble,
                  borderRadius: BorderRadius.only(
                    topLeft: const Radius.circular(16),
                    topRight: const Radius.circular(16),
                    bottomLeft: Radius.circular(_isUser ? 16 : 4),
                    bottomRight: Radius.circular(_isUser ? 4 : 16),
                  ),
                  border: Border.all(color: ClawdTheme.surfaceBorder, width: 1),
                ),
                child: _isUser
                    ? Text(
                        message.content,
                        style: const TextStyle(fontSize: 14, height: 1.5),
                      )
                    : MarkdownMessage(content: message.content),
              ),
            ),
            const SizedBox(width: 8),
            if (_isUser) _Avatar(role: message.role),
          ],
        ),
      ),
    );
  }
}

class _Avatar extends StatelessWidget {
  const _Avatar({required this.role});
  final MessageRole role;

  @override
  Widget build(BuildContext context) {
    final isUser = role == MessageRole.user;
    return CircleAvatar(
      radius: 14,
      backgroundColor:
          isUser ? ClawdTheme.claw : ClawdTheme.surfaceElevated,
      child: Icon(
        isUser ? Icons.person : Icons.auto_awesome,
        size: 14,
        color: Colors.white,
      ),
    );
  }
}
