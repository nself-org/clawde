// SPDX-License-Identifier: MIT
// Dart types for the code completion RPC (Sprint GG, CC.8).

/// A single inline code-completion suggestion.
class Insertion {
  const Insertion({
    required this.text,
    required this.startLine,
    required this.endLine,
    this.confidence = 1.0,
  });

  /// Text to insert at the cursor position.
  final String text;

  /// 0-based start line of the suggestion range (inclusive).
  final int startLine;

  /// 0-based end line of the suggestion range (inclusive).
  final int endLine;

  /// Confidence score 0.0–1.0.
  final double confidence;

  factory Insertion.fromJson(Map<String, dynamic> json) => Insertion(
        text: json['text'] as String? ?? '',
        startLine: json['startLine'] as int? ?? 0,
        endLine: json['endLine'] as int? ?? 0,
        confidence: (json['confidence'] as num?)?.toDouble() ?? 1.0,
      );

  Map<String, dynamic> toJson() => {
        'text': text,
        'startLine': startLine,
        'endLine': endLine,
        'confidence': confidence,
      };
}

/// Request parameters for `completion.complete`.
class CompletionRequest {
  const CompletionRequest({
    required this.filePath,
    required this.prefix,
    required this.suffix,
    required this.cursorLine,
    required this.cursorCol,
    this.fileContent = '',
    this.sessionId = '',
  });

  final String filePath;
  final String prefix;
  final String suffix;
  final int cursorLine;
  final int cursorCol;

  /// Full file content for repo context injection (optional).
  final String fileContent;

  /// Session ID to route the completion through (required for actual completions).
  final String sessionId;

  Map<String, dynamic> toJson() => {
        'filePath': filePath,
        'prefix': prefix,
        'suffix': suffix,
        'cursorLine': cursorLine,
        'cursorCol': cursorCol,
        if (fileContent.isNotEmpty) 'fileContent': fileContent,
        if (sessionId.isNotEmpty) 'sessionId': sessionId,
      };
}

/// Response from `completion.complete`.
class CompletionResponse {
  const CompletionResponse({
    required this.insertions,
    required this.source,
  });

  /// Ordered list of suggestions (best first).
  final List<Insertion> insertions;

  /// Where the completion came from: `"cache"` or `"provider"`.
  final String source;

  bool get fromCache => source == 'cache';
  bool get hasResults => insertions.isNotEmpty;

  factory CompletionResponse.fromJson(Map<String, dynamic> json) =>
      CompletionResponse(
        insertions: (json['insertions'] as List<dynamic>?)
                ?.map((e) => Insertion.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
        source: json['source'] as String? ?? 'provider',
      );
}
