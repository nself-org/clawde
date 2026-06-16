/// Natural language git query types for the clawd daemon.
///
/// Sprint DD NL.4 — git.query RPC types.

// ─── Commit Summary ───────────────────────────────────────────────────────────

class CommitSummary {
  final String hash;
  final String subject;
  final String authorName;
  final String authorDate;

  const CommitSummary({
    required this.hash,
    required this.subject,
    required this.authorName,
    required this.authorDate,
  });

  factory CommitSummary.fromJson(Map<String, dynamic> json) => CommitSummary(
        hash: json['hash'] as String? ?? '',
        subject: json['subject'] as String? ?? '',
        authorName: json['authorName'] as String? ?? '',
        authorDate: json['authorDate'] as String? ?? '',
      );

  Map<String, dynamic> toJson() => {
        'hash': hash,
        'subject': subject,
        'authorName': authorName,
        'authorDate': authorDate,
      };

  String get shortHash => hash.length >= 7 ? hash.substring(0, 7) : hash;

  @override
  String toString() => 'CommitSummary($shortHash: $subject)';
}

// ─── Git Query Result ─────────────────────────────────────────────────────────

class GitQueryResult {
  final String question;
  final String narrative;
  final List<CommitSummary> commits;

  const GitQueryResult({
    required this.question,
    required this.narrative,
    required this.commits,
  });

  factory GitQueryResult.fromJson(Map<String, dynamic> json) =>
      GitQueryResult(
        question: json['question'] as String? ?? '',
        narrative: json['narrative'] as String? ?? '',
        commits: (json['commits'] as List?)
                ?.map((c) =>
                    CommitSummary.fromJson(c as Map<String, dynamic>))
                .toList() ??
            [],
      );

  Map<String, dynamic> toJson() => {
        'question': question,
        'narrative': narrative,
        'commits': commits.map((c) => c.toJson()).toList(),
      };

  @override
  String toString() =>
      'GitQueryResult(question: $question, commits: ${commits.length})';
}
