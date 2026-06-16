import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'kanban_column.dart';

/// Horizontally scrollable Kanban board showing all task statuses.
class KanbanBoard extends StatelessWidget {
  const KanbanBoard({
    super.key,
    required this.tasksByStatus,
    this.selectedTaskId,
    this.onTaskTap,
    this.onDiffTap,
    this.columnWidth = 260,
  });

  final Map<String, List<AgentTask>> tasksByStatus;
  final String? selectedTaskId;
  final void Function(AgentTask)? onTaskTap;
  final void Function(AgentTask)? onDiffTap;
  final double columnWidth;

  static const List<TaskStatus> _columnOrder = [
    TaskStatus.active,
    TaskStatus.inProgress,
    TaskStatus.pending,
    TaskStatus.planned,
    TaskStatus.claimed,
    TaskStatus.needsApproval,
    TaskStatus.codeReview,
    TaskStatus.blocked,
    TaskStatus.inQa,
    TaskStatus.interrupted,
    TaskStatus.deferred,
    TaskStatus.done,
    TaskStatus.canceled,
    TaskStatus.failed,
  ];

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.all(12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: _columnOrder.map((status) {
          final key = status.toJsonStr();
          return KanbanColumn(
            status: status,
            tasks: tasksByStatus[key] ?? [],
            selectedTaskId: selectedTaskId,
            onTaskTap: onTaskTap,
            onDiffTap: onDiffTap,
            maxWidth: columnWidth,
          );
        }).toList(),
      ),
    );
  }
}
