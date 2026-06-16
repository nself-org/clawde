import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'agent_chip.dart';

/// A single row in the agent swimlane panel showing one agent's status.
class AgentSwimlaneRow extends StatelessWidget {
  const AgentSwimlaneRow({
    super.key,
    required this.agent,
    this.currentTask,
    this.onTap,
  });

  final AgentView agent;
  final AgentTask? currentTask;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Row(
          children: [
            AgentChip(
              agentId: agent.agentId,
              isActive: agent.status == AgentViewStatus.active,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: currentTask != null
                  ? Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(
                          currentTask!.title,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodySmall,
                        ),
                        const SizedBox(height: 2),
                        LinearProgressIndicator(
                          value: null, // indeterminate while active
                          minHeight: 2,
                          backgroundColor:
                              theme.colorScheme.surfaceContainerHighest,
                        ),
                      ],
                    )
                  : Text(
                      agent.status == AgentViewStatus.idle ? 'Idle' : 'Offline',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
            ),
            const SizedBox(width: 8),
            if (agent.lastSeen != null)
              Text(
                _timeAgo(agent.lastSeen!),
                style: theme.textTheme.labelSmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
          ],
        ),
      ),
    );
  }

  String _timeAgo(int ts) {
    final diff = DateTime.now()
        .difference(DateTime.fromMillisecondsSinceEpoch(ts * 1000));
    if (diff.inSeconds < 60) return '${diff.inSeconds}s ago';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    return '${diff.inHours}h ago';
  }
}
