import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'agent_chip.dart';
import 'task_status_badge.dart';
import 'activity_feed_item.dart';

/// Slide-in panel showing full task details + activity timeline.
class TaskDetailPanel extends StatelessWidget {
  const TaskDetailPanel({
    super.key,
    required this.task,
    this.activityLog = const [],
    this.onClose,
    this.onMarkDone,
    this.onMarkBlocked,
    this.onDefer,
  });

  final AgentTask task;
  final List<ActivityLogEntry> activityLog;
  final VoidCallback? onClose;
  final ValueChanged<String>? onMarkDone; // passes completion notes
  final VoidCallback? onMarkBlocked;
  final VoidCallback? onDefer;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final hasResume =
        activityLog.any((e) => e.action == 'session_resume');

    return Material(
      elevation: 4,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Header
          Container(
            padding:
                const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            color: theme.colorScheme.surfaceContainerLow,
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    task.title,
                    style: theme.textTheme.titleMedium
                        ?.copyWith(fontWeight: FontWeight.w600),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.close),
                  onPressed: onClose,
                  tooltip: 'Close',
                ),
              ],
            ),
          ),
          // Meta row
          Padding(
            padding:
                const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                TaskStatusBadge(status: task.status),
                if (task.claimedBy != null)
                  AgentChip(agentId: task.claimedBy!),
                if (task.phase != null)
                  Chip(
                    label: Text(task.phase!),
                    padding: EdgeInsets.zero,
                    visualDensity: VisualDensity.compact,
                  ),
                if (hasResume)
                  Chip(
                    label: const Text('\u21ba Resumed'),
                    backgroundColor: Colors.orange.withValues(alpha: 0.2),
                    side: const BorderSide(color: Colors.orange),
                    padding: EdgeInsets.zero,
                    visualDensity: VisualDensity.compact,
                  ),
              ],
            ),
          ),
          const Divider(height: 1),
          // Action buttons
          Padding(
            padding:
                const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            child: Wrap(
              spacing: 8,
              children: [
                if (onMarkDone != null)
                  FilledButton.tonal(
                    onPressed: () => _showDoneDialog(context),
                    child: const Text('Done'),
                  ),
                if (onMarkBlocked != null)
                  OutlinedButton(
                    onPressed: onMarkBlocked,
                    child: const Text('Block'),
                  ),
                if (onDefer != null)
                  OutlinedButton(
                    onPressed: onDefer,
                    child: const Text('Defer'),
                  ),
              ],
            ),
          ),
          const Divider(height: 1),
          // Activity timeline
          Expanded(
            child: activityLog.isEmpty
                ? const Center(child: Text('No activity yet.'))
                : ListView.builder(
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    itemCount: _itemCount(activityLog, hasResume),
                    itemBuilder: (ctx, i) => _buildTimelineItem(
                        ctx, i, activityLog, hasResume),
                  ),
          ),
        ],
      ),
    );
  }

  int _itemCount(List<ActivityLogEntry> log, bool hasResume) {
    if (!hasResume) return log.length;
    final resumeIdx =
        log.indexWhere((e) => e.action == 'session_resume');
    return log.length + (resumeIdx >= 0 ? 1 : 0);
  }

  Widget _buildTimelineItem(
    BuildContext ctx,
    int i,
    List<ActivityLogEntry> log,
    bool hasResume,
  ) {
    if (!hasResume) return ActivityFeedItem(entry: log[i]);

    final resumeIdx =
        log.indexWhere((e) => e.action == 'session_resume');
    if (resumeIdx < 0) return ActivityFeedItem(entry: log[i]);

    // Insert a divider row immediately after the resume entry.
    if (i == resumeIdx + 1) {
      return const _ResumeDivider();
    }
    // Entries before the resume divider row are shifted by one after the
    // divider is inserted at resumeIdx+1, so map raw index → entry index.
    final entryIdx = i > resumeIdx + 1 ? i - 1 : i;
    return ActivityFeedItem(
      entry: log[entryIdx],
      muted: i < resumeIdx,
    );
  }

  void _showDoneDialog(BuildContext context) {
    final controller = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Mark Done'),
        content: TextField(
          controller: controller,
          decoration: const InputDecoration(
            hintText: 'Completion notes (required)',
          ),
          minLines: 2,
          maxLines: 5,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              Navigator.pop(ctx);
              onMarkDone?.call(controller.text);
            },
            child: const Text('Done'),
          ),
        ],
      ),
    );
  }
}

class _ResumeDivider extends StatelessWidget {
  const _ResumeDivider();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 16),
      child: Row(
        children: [
          const Expanded(child: Divider()),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Text(
              '--- Resumed after interruption ---',
              style: Theme.of(context).textTheme.labelSmall?.copyWith(
                    color: Colors.orange,
                  ),
            ),
          ),
          const Expanded(child: Divider()),
        ],
      ),
    );
  }
}
