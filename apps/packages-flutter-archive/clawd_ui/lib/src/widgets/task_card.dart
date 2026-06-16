import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'package:clawd_core/clawd_core.dart';

import 'task_status_badge.dart';
import 'agent_chip.dart';

/// Compact task card for Kanban column display.
class TaskCard extends ConsumerWidget {
  const TaskCard({
    super.key,
    required this.task,
    this.onTap,
    this.onDiffTap,
    this.selected = false,
  });

  final AgentTask task;
  final VoidCallback? onTap;

  /// Called when the ∆ diff badge is tapped. If null, no badge is shown
  /// even when a worktree exists.
  final VoidCallback? onDiffTap;
  final bool selected;

  @override
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final sevColor = _severityColor(task.severity);

    // Watch worktree status to show diff badge (null = no worktree).
    final worktreeAsync =
        onDiffTap != null ? ref.watch(worktreeProvider(task.id)) : null;
    final hasActiveWorktree = worktreeAsync?.valueOrNull?.status == 'active';

    return GestureDetector(
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 120),
        margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: selected
              ? Colors.white.withValues(alpha: 0.08)
              : Colors.white.withValues(alpha: 0.04),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: selected
                ? const Color(0xFF42A5F5).withValues(alpha: 0.5)
                : Colors.white.withValues(alpha: 0.1),
          ),
        ),
        child: Padding(
          padding: const EdgeInsets.all(10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    width: 3,
                    height: 28,
                    decoration: BoxDecoration(
                      color: sevColor,
                      borderRadius: BorderRadius.circular(2),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      task.title,
                      style: const TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                        color: Colors.white,
                      ),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  // ∆ diff badge — shown when an active worktree exists.
                  if (hasActiveWorktree)
                    GestureDetector(
                      onTap: onDiffTap,
                      child: Container(
                        margin: const EdgeInsets.only(left: 6),
                        padding: const EdgeInsets.symmetric(
                            horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: Colors.orangeAccent.withValues(alpha: 0.15),
                          borderRadius: BorderRadius.circular(4),
                          border: Border.all(
                              color:
                                  Colors.orangeAccent.withValues(alpha: 0.4)),
                        ),
                        child: const Text(
                          '∆',
                          style: TextStyle(
                            fontSize: 11,
                            fontWeight: FontWeight.w700,
                            color: Colors.orangeAccent,
                          ),
                        ),
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 6),
              Row(
                children: [
                  if (task.phase != null) ...[
                    Text(
                      task.phase!,
                      style: TextStyle(
                        fontSize: 11,
                        color: Colors.white.withValues(alpha: 0.4),
                      ),
                    ),
                    const SizedBox(width: 6),
                  ],
                  TaskStatusBadge(status: task.status, compact: true),
                  const Spacer(),
                  if (task.claimedBy != null)
                    AgentChip(agentId: task.claimedBy!, isActive: true),
                ],
              ),
              if (task.blockReason != null) ...[
                const SizedBox(height: 4),
                Text(
                  task.blockReason!,
                  style: const TextStyle(
                    fontSize: 11,
                    color: Color(0xFFEF5350),
                    fontStyle: FontStyle.italic,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  static Color _severityColor(TaskSeverity s) => switch (s) {
        TaskSeverity.critical => const Color(0xFFEF5350),
        TaskSeverity.high => const Color(0xFFFF7043),
        TaskSeverity.medium => const Color(0xFFFFCA28),
        TaskSeverity.low => const Color(0xFF66BB6A),
      };
}
