import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'agent_chip.dart';

/// A single row in the activity feed.
class ActivityFeedItem extends StatelessWidget {
  const ActivityFeedItem({
    super.key,
    required this.entry,
    this.muted = false,
  });

  final ActivityLogEntry entry;

  /// When true, renders the row at reduced opacity (e.g. pre-resume history).
  final bool muted;

  @override
  Widget build(BuildContext context) {
    final icon = _icon(entry.entryType);
    final iconColor = _iconColor(entry.entryType);

    Widget row = Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 14, color: iconColor),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    AgentChip(agentId: entry.agent),
                    const SizedBox(width: 6),
                    Text(
                      entry.action,
                      style: const TextStyle(
                        fontSize: 12,
                        fontWeight: FontWeight.w500,
                        color: Colors.white,
                      ),
                    ),
                    const Spacer(),
                    Text(
                      _formatTs(entry.ts),
                      style: TextStyle(
                        fontSize: 11,
                        color: Colors.white.withValues(alpha: 0.35),
                      ),
                    ),
                  ],
                ),
                if (entry.detail != null) ...[
                  const SizedBox(height: 2),
                  Text(
                    entry.detail!,
                    style: TextStyle(
                      fontSize: 12,
                      color: Colors.white.withValues(alpha: 0.55),
                    ),
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );

    if (muted) {
      row = Opacity(opacity: 0.45, child: row);
    }

    return row;
  }

  static IconData _icon(ActivityEntryType t) => switch (t) {
        ActivityEntryType.note => Icons.sticky_note_2_outlined,
        ActivityEntryType.system => Icons.settings_outlined,
        ActivityEntryType.auto => Icons.bolt_outlined,
      };

  static Color _iconColor(ActivityEntryType t) => switch (t) {
        ActivityEntryType.note => const Color(0xFFAB47BC),
        ActivityEntryType.system => const Color(0xFF9E9E9E),
        ActivityEntryType.auto => const Color(0xFF42A5F5),
      };

  static String _formatTs(int ts) {
    final dt = DateTime.fromMillisecondsSinceEpoch(ts * 1000);
    final now = DateTime.now();
    final diff = now.difference(dt);
    if (diff.inSeconds < 60) return '${diff.inSeconds}s ago';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    return '${dt.month}/${dt.day}';
  }
}
