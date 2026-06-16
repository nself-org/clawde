import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'activity_feed_item.dart';

/// Aggregated view of all activity log entries for a single phase.
/// Phase-level notes (taskId == null) are shown at the top with distinct styling.
class PhaseLogView extends StatelessWidget {
  const PhaseLogView({
    super.key,
    required this.phase,
    required this.entries,
  });

  final String phase;
  final List<ActivityLogEntry> entries;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final phaseNotes = entries
        .where((e) =>
            e.taskId == null && e.entryType == ActivityEntryType.note)
        .toList();
    final taskEntries = entries
        .where((e) =>
            e.taskId != null || e.entryType != ActivityEntryType.note)
        .toList();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: Text(
            'Phase: $phase',
            style: theme.textTheme.titleMedium
                ?.copyWith(fontWeight: FontWeight.w600),
          ),
        ),
        if (phaseNotes.isNotEmpty) ...[
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Text(
              'Phase Notes',
              style: theme.textTheme.labelMedium?.copyWith(
                color: theme.colorScheme.primary,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          for (final e in phaseNotes)
            Container(
              margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: theme.colorScheme.primaryContainer
                    .withValues(alpha: 0.3),
                borderRadius: BorderRadius.circular(8),
                border: Border.all(
                    color: theme.colorScheme.primaryContainer),
              ),
              child: Text(
                e.detail ?? e.action,
                style: theme.textTheme.bodySmall,
              ),
            ),
          const Divider(height: 24),
        ],
        if (taskEntries.isEmpty)
          const Padding(
            padding: EdgeInsets.all(24),
            child:
                Center(child: Text('No task activity for this phase.')),
          )
        else
          for (final e in taskEntries) ActivityFeedItem(entry: e),
      ],
    );
  }
}
