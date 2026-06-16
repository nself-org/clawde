/// Session sharing types for the clawd daemon.
///
/// Sprint EE CS.9 — session.share/join_shared RPC types.
/// Cloud tier only — share a live session with collaborators.

// ─── Session Share ────────────────────────────────────────────────────────────

class SessionShare {
  final String shareToken;
  final String sessionId;
  final String? teamId;
  final bool allowSend;
  final String expiresAt;
  final int shareholderCount;

  const SessionShare({
    required this.shareToken,
    required this.sessionId,
    this.teamId,
    required this.allowSend,
    required this.expiresAt,
    required this.shareholderCount,
  });

  factory SessionShare.fromJson(Map<String, dynamic> json) => SessionShare(
        shareToken: json['shareToken'] as String? ?? '',
        sessionId: json['sessionId'] as String? ?? '',
        teamId: json['teamId'] as String?,
        allowSend: json['allowSend'] as bool? ?? false,
        expiresAt: json['expiresAt'] as String? ?? '',
        shareholderCount: json['shareholderCount'] as int? ?? 0,
      );

  Map<String, dynamic> toJson() => {
        'shareToken': shareToken,
        'sessionId': sessionId,
        if (teamId != null) 'teamId': teamId,
        'allowSend': allowSend,
        'expiresAt': expiresAt,
        'shareholderCount': shareholderCount,
      };

  @override
  String toString() =>
      'SessionShare(token: $shareToken, session: $sessionId, shareholders: $shareholderCount)';
}

// ─── Shareholder ──────────────────────────────────────────────────────────────

class ShareholderId {
  final String id;
  final String? displayName;
  final bool canSend;
  final String joinedAt;

  const ShareholderId({
    required this.id,
    this.displayName,
    required this.canSend,
    required this.joinedAt,
  });

  factory ShareholderId.fromJson(Map<String, dynamic> json) => ShareholderId(
        id: json['id'] as String? ?? '',
        displayName: json['displayName'] as String?,
        canSend: json['canSend'] as bool? ?? false,
        joinedAt: json['joinedAt'] as String? ?? '',
      );

  @override
  String toString() => 'ShareholderId(id: $id, canSend: $canSend)';
}

// ─── Share List Result ────────────────────────────────────────────────────────

class ShareListResult {
  final String sessionId;
  final List<SessionShare> shares;

  const ShareListResult({
    required this.sessionId,
    required this.shares,
  });

  factory ShareListResult.fromJson(Map<String, dynamic> json) =>
      ShareListResult(
        sessionId: json['sessionId'] as String? ?? '',
        shares: (json['shares'] as List?)
                ?.map((s) =>
                    SessionShare.fromJson(s as Map<String, dynamic>))
                .toList() ??
            [],
      );

  bool get hasActiveShares => shares.isNotEmpty;
}
