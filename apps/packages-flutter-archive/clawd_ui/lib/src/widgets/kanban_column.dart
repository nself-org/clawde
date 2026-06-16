import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'task_card.dart';
import 'task_status_badge.dart';

/// A single Kanban column for one [TaskStatus].
class KanbanColumn extends StatelessWidget {
  const KanbanColumn({
    super.key,
    required this.status,
    required this.tasks,
    this.selectedTaskId,
    this.onTaskTap,
    this.onDiffTap,
    this.maxWidth = 260,
  });

  final TaskStatus status;
  final List<AgentTask> tasks;
  final String? selectedTaskId;
  final void Function(AgentTask)? onTaskTap;
  final void Function(AgentTask)? onDiffTap;
  final double maxWidth;

  @override
  Widget build(BuildContext context) {
    final headerColor = TaskStatusBadge.colorFor(status);
    return Container(
      width: maxWidth,
      margin: const EdgeInsets.only(right: 8),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.03),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: Colors.white.withValues(alpha: 0.07)),
      ),
      child: Column(
        children: [
          // Column header
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            child: Row(
              children: [
                Container(
                  width: 10,
                  height: 10,
                  decoration: BoxDecoration(
                    color: headerColor,
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  status.displayName,
                  style: const TextStyle(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: Colors.white,
                  ),
                ),
                const Spacer(),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                  decoration: BoxDecoration(
                    color: Colors.white.withValues(alpha: 0.08),
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: Text(
                    '${tasks.length}',
                    style: TextStyle(
                      fontSize: 11,
                      color: Colors.white.withValues(alpha: 0.6),
                    ),
                  ),
                ),
              ],
            ),
          ),
          const Divider(height: 1, color: Color(0x1AFFFFFF)),
          // Task list
          Expanded(
            child: tasks.isEmpty
                ? Center(
                    child: Text(
                      'No tasks',
                      style: TextStyle(
                        fontSize: 12,
                        color: Colors.white.withValues(alpha: 0.25),
                      ),
                    ),
                  )
                : ListView.builder(
                    padding: const EdgeInsets.symmetric(vertical: 6),
                    itemCount: tasks.length,
                    itemBuilder: (ctx, i) => TaskCard(
                      task: tasks[i],
                      selected: tasks[i].id == selectedTaskId,
                      onTap: onTaskTap != null ? () => onTaskTap!(tasks[i]) : null,
                      onDiffTap:
                          onDiffTap != null ? () => onDiffTap!(tasks[i]) : null,
                    ),
                  ),
          ),
        ],
      ),
    );
  }
}
