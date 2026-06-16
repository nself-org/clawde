import 'package:clawd_proto/clawd_proto.dart';

/// Exports a session's chat history to a formatted markdown string.
///
/// The output format is:
/// ```
/// # Session: {title}
///
/// **Provider:** {provider} | **Status:** {status}
/// **Created:** {createdAt} | **Updated:** {updatedAt}
///
/// ---
///
/// ## Messages
///
/// ### {role} ({timestamp})
/// {content}
/// ---
/// ```
String exportSessionToMarkdown(Session session, List<Message> messages) {
  final buffer = StringBuffer();

  // Header
  buffer.writeln('# Session: ${session.title.isEmpty ? session.id : session.title}');
  buffer.writeln();
  buffer.writeln(
    '**Provider:** ${session.provider.name} | '
    '**Status:** ${session.status.name}',
  );
  buffer.writeln(
    '**Created:** ${_formatTimestamp(session.createdAt)} | '
    '**Updated:** ${_formatTimestamp(session.updatedAt)}',
  );
  buffer.writeln();
  buffer.writeln('---');
  buffer.writeln();

  // Messages section
  buffer.writeln('## Messages');
  buffer.writeln();

  if (messages.isEmpty) {
    buffer.writeln('_No messages in this session._');
  } else {
    for (final message in messages) {
      final roleLabel = _roleLabel(message.role);
      final timestamp = _formatTimestamp(message.createdAt);

      buffer.writeln('### $roleLabel ($timestamp)');
      buffer.writeln();
      buffer.writeln(message.content);
      buffer.writeln();
      buffer.writeln('---');
      buffer.writeln();
    }
  }

  return buffer.toString();
}

/// Returns a human-readable label for a message role.
String _roleLabel(MessageRole role) {
  switch (role) {
    case MessageRole.user:
      return 'User';
    case MessageRole.assistant:
      return 'Assistant';
    case MessageRole.system:
      return 'System';
    case MessageRole.tool:
      return 'Tool';
  }
}

/// Formats a [DateTime] as a readable timestamp string.
///
/// Produces: `YYYY-MM-DD HH:MM:SS`
String _formatTimestamp(DateTime dt) {
  final y = dt.year.toString().padLeft(4, '0');
  final m = dt.month.toString().padLeft(2, '0');
  final d = dt.day.toString().padLeft(2, '0');
  final h = dt.hour.toString().padLeft(2, '0');
  final min = dt.minute.toString().padLeft(2, '0');
  final s = dt.second.toString().padLeft(2, '0');
  return '$y-$m-$d $h:$min:$s';
}
