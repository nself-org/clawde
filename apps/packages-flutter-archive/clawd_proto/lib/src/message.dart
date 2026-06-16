/// Message types for session chat history.

import 'dart:developer' as dev;

enum MessageRole { user, assistant, system, tool }

/// Parse a MessageRole from a JSON string, defaulting to [MessageRole.system]
/// for unknown values rather than throwing an unhandled exception.
MessageRole _parseRole(String? raw) {
  if (raw == null) return MessageRole.system;
  try {
    return MessageRole.values.byName(raw);
  } catch (_) {
    dev.log('unknown MessageRole: $raw — defaulting to system',
        name: 'clawd_proto');
    return MessageRole.system;
  }
}

class Message {
  final String id;
  final String sessionId;
  final MessageRole role;
  final String content;
  final String status;
  final DateTime createdAt;
  final Map<String, dynamic> metadata;

  const Message({
    required this.id,
    required this.sessionId,
    required this.role,
    required this.content,
    required this.status,
    required this.createdAt,
    this.metadata = const {},
  });

  factory Message.fromJson(Map<String, dynamic> json) => Message(
        id: json['id'] as String,
        sessionId: json['sessionId'] as String,
        role: _parseRole(json['role'] as String?),
        content: json['content'] as String,
        status: json['status'] as String? ?? 'done',
        createdAt: DateTime.parse(json['createdAt'] as String),
        metadata:
            (json['metadata'] as Map<String, dynamic>?) ?? const {},
      );
}
