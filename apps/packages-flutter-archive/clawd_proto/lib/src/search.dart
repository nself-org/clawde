// SPDX-License-Identifier: MIT
// Dart types for the session search RPC (Sprint GG, SS.6).

/// Optional filters for `session.search`.
class SearchFilters {
  const SearchFilters({
    this.sessionId,
    this.dateFrom,
    this.dateTo,
    this.role,
  });

  /// Restrict to a specific session.
  final String? sessionId;

  /// ISO-8601 lower bound for message creation time.
  final String? dateFrom;

  /// ISO-8601 upper bound for message creation time.
  final String? dateTo;

  /// Restrict to "user" or "assistant" messages.
  final String? role;

  Map<String, dynamic> toJson() => {
        if (sessionId != null) 'sessionId': sessionId,
        if (dateFrom != null) 'dateFrom': dateFrom,
        if (dateTo != null) 'dateTo': dateTo,
        if (role != null) 'role': role,
      };
}

/// Parameters for `session.search`.
class SearchQuery {
  const SearchQuery({
    required this.query,
    this.limit = 20,
    this.filterBy = const SearchFilters(),
  });

  final String query;
  final int limit;
  final SearchFilters filterBy;

  Map<String, dynamic> toJson() => {
        'query': query,
        'limit': limit,
        'filterBy': filterBy.toJson(),
      };
}

/// A single search result entry returned by `session.search`.
class SearchResult {
  const SearchResult({
    required this.sessionId,
    required this.messageId,
    required this.snippet,
    required this.role,
    required this.createdAt,
    required this.rank,
  });

  final String sessionId;
  final String messageId;

  /// Short snippet of matching text (may contain `<b>…</b>` HTML highlights).
  final String snippet;

  final String role;
  final String createdAt;

  /// BM25 rank (lower = more relevant; typically negative).
  final double rank;

  factory SearchResult.fromJson(Map<String, dynamic> json) => SearchResult(
        sessionId: json['sessionId'] as String? ?? '',
        messageId: json['messageId'] as String? ?? '',
        snippet: json['snippet'] as String? ?? '',
        role: json['role'] as String? ?? '',
        createdAt: json['createdAt'] as String? ?? '',
        rank: (json['rank'] as num?)?.toDouble() ?? 0.0,
      );

  Map<String, dynamic> toJson() => {
        'sessionId': sessionId,
        'messageId': messageId,
        'snippet': snippet,
        'role': role,
        'createdAt': createdAt,
        'rank': rank,
      };
}

/// Response from `session.search`.
class SearchResponse {
  const SearchResponse({required this.results, required this.totalHits});

  final List<SearchResult> results;
  final int totalHits;

  factory SearchResponse.fromJson(Map<String, dynamic> json) => SearchResponse(
        results: (json['results'] as List<dynamic>?)
                ?.map((e) => SearchResult.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
        totalHits: json['totalHits'] as int? ?? 0,
      );
}
