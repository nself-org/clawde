import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'activity_feed_item.dart';
import '../theme/clawd_theme.dart';

/// Scrollable activity feed for a repo or task.
class ActivityFeed extends StatelessWidget {
  const ActivityFeed({
    super.key,
    required this.entries,
    this.isLoading = false,
  });

  final List<ActivityLogEntry> entries;
  final bool isLoading;

  @override
  Widget build(BuildContext context) {
    if (isLoading) {
      return const Center(
        child: CircularProgressIndicator(color: ClawdTheme.claw),
      );
    }

    if (entries.isEmpty) {
      return Center(
        child: Text(
          'No activity yet',
          style: TextStyle(
            fontSize: 13,
            color: Colors.white.withValues(alpha: 0.35),
          ),
        ),
      );
    }

    return ListView.separated(
      reverse: true,
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: entries.length,
      separatorBuilder: (_, __) =>
          Divider(height: 1, color: Colors.white.withValues(alpha: 0.05)),
      itemBuilder: (ctx, i) => ActivityFeedItem(entry: entries[i]),
    );
  }
}
