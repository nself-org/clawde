// SPDX-License-Identifier: MIT
import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Session health indicator chip for the session header (Sprint G, SI.T15).
///
/// Hidden when [healthScore] is null (daemon not yet connected or RPC
/// not available), and when score >= 80 and [needsRefresh] is false
/// (session is healthy — no badge needed).
///
/// Color coding:
///   score >= 80  — green    (healthy, not shown)
///   score 40–79  — orange/warning (degraded)
///   score < 40   — red/error (needs refresh)
class HealthChip extends StatelessWidget {
  const HealthChip({
    super.key,
    required this.healthScore,
    this.needsRefresh = false,
    this.totalTurns = 0,
    this.shortResponseCount = 0,
    this.toolErrorCount = 0,
    this.truncationCount = 0,
  });

  final int? healthScore;
  final bool needsRefresh;
  final int totalTurns;
  final int shortResponseCount;
  final int toolErrorCount;
  final int truncationCount;

  @override
  Widget build(BuildContext context) {
    final score = healthScore;
    // Hidden when no data, or when session is healthy (score >= 80 and no refresh needed).
    if (score == null || (score >= 80 && !needsRefresh)) {
      return const SizedBox.shrink();
    }

    final (color, bgColor, icon, tooltip) = _colorInfo(score, needsRefresh);

    return Tooltip(
      message: tooltip,
      child: InkWell(
        onTap: () => _showSheet(context, score),
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
          decoration: BoxDecoration(
            color: bgColor.withValues(alpha: 0.18),
            borderRadius: BorderRadius.circular(10),
            border: Border.all(color: bgColor.withValues(alpha: 0.4)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 11, color: color),
              const SizedBox(width: 4),
              Text(
                '$score',
                style: TextStyle(
                  fontSize: 10,
                  color: color,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  (Color, Color, IconData, String) _colorInfo(
      int score, bool refresh) {
    if (refresh || score < 40) {
      return (
        ClawdTheme.error,
        ClawdTheme.error,
        Icons.favorite_border,
        'Session health: $score/100 — refresh recommended',
      );
    }
    return (
      ClawdTheme.warning,
      ClawdTheme.warning,
      Icons.favorite,
      'Session health: $score/100 — slightly degraded',
    );
  }

  void _showSheet(BuildContext context, int score) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      builder: (_) => _HealthSheet(
        healthScore: score,
        needsRefresh: needsRefresh,
        totalTurns: totalTurns,
        shortResponseCount: shortResponseCount,
        toolErrorCount: toolErrorCount,
        truncationCount: truncationCount,
      ),
    );
  }
}

class _HealthSheet extends StatelessWidget {
  const _HealthSheet({
    required this.healthScore,
    required this.needsRefresh,
    required this.totalTurns,
    required this.shortResponseCount,
    required this.toolErrorCount,
    required this.truncationCount,
  });

  final int healthScore;
  final bool needsRefresh;
  final int totalTurns;
  final int shortResponseCount;
  final int toolErrorCount;
  final int truncationCount;

  @override
  Widget build(BuildContext context) {
    final scoreColor = healthScore < 40
        ? ClawdTheme.error
        : healthScore < 80
            ? ClawdTheme.warning
            : ClawdTheme.success;

    return Container(
      decoration: const BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(height: 8),
          Container(
            width: 36,
            height: 4,
            decoration: BoxDecoration(
              color: Colors.white24,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 16, 20, 4),
            child: Row(
              children: [
                Icon(Icons.favorite, size: 16, color: scoreColor),
                const SizedBox(width: 8),
                const Text(
                  'Session Health',
                  style: TextStyle(
                    fontSize: 15,
                    fontWeight: FontWeight.w700,
                    color: Colors.white,
                  ),
                ),
                const Spacer(),
                Text(
                  '$healthScore / 100',
                  style: TextStyle(
                    fontSize: 20,
                    fontWeight: FontWeight.w700,
                    color: scoreColor,
                  ),
                ),
              ],
            ),
          ),
          if (needsRefresh)
            const Padding(
              padding: EdgeInsets.fromLTRB(20, 0, 20, 8),
              child: Row(
                children: [
                  Icon(Icons.warning_amber, size: 13, color: ClawdTheme.warning),
                  SizedBox(width: 6),
                  Text(
                    'Starting a new session is recommended.',
                    style: TextStyle(fontSize: 12, color: ClawdTheme.warning),
                  ),
                ],
              ),
            ),
          const Padding(
            padding: EdgeInsets.fromLTRB(20, 0, 20, 12),
            child: Text(
              'Health degrades when responses are short, tools fail, or context is truncated.',
              style: TextStyle(fontSize: 12, color: Colors.white38),
            ),
          ),
          const Divider(height: 1),
          _StatRow(label: 'Turns', value: '$totalTurns'),
          _StatRow(label: 'Short responses', value: '$shortResponseCount'),
          _StatRow(label: 'Tool errors', value: '$toolErrorCount'),
          _StatRow(label: 'Truncations', value: '$truncationCount'),
          const SizedBox(height: 24),
        ],
      ),
    );
  }
}

class _StatRow extends StatelessWidget {
  const _StatRow({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 9),
      child: Row(
        children: [
          Text(
            label,
            style: const TextStyle(fontSize: 13, color: Colors.white54),
          ),
          const Spacer(),
          Text(
            value,
            style: const TextStyle(
              fontSize: 13,
              color: Colors.white,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }
}
