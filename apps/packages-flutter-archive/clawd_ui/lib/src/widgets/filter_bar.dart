import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

/// Compact horizontal filter bar for the agent dashboard.
class FilterBar extends StatelessWidget {
  const FilterBar({
    super.key,
    required this.agents,
    this.selectedAgent,
    this.selectedType,
    this.selectedSeverity,
    this.selectedStatus,
    this.selectedPhase,
    this.onAgentChanged,
    this.onTypeChanged,
    this.onSeverityChanged,
    this.onStatusChanged,
    this.onPhaseChanged,
    this.onClear,
  });

  final List<AgentView> agents;
  final String? selectedAgent;
  final String? selectedType;
  final String? selectedSeverity;
  final String? selectedStatus;
  final String? selectedPhase;

  final ValueChanged<String?>? onAgentChanged;
  final ValueChanged<String?>? onTypeChanged;
  final ValueChanged<String?>? onSeverityChanged;
  final ValueChanged<String?>? onStatusChanged;
  final ValueChanged<String?>? onPhaseChanged;
  final VoidCallback? onClear;

  bool get _hasAny =>
      selectedAgent != null ||
      selectedType != null ||
      selectedSeverity != null ||
      selectedStatus != null ||
      selectedPhase != null;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Row(
        children: [
          _Dropdown(
            label: 'Agent',
            value: selectedAgent,
            items: {
              null: 'All agents',
              for (final a in agents) a.agentId: a.displayName,
            },
            onChanged: onAgentChanged,
          ),
          const SizedBox(width: 8),
          _Dropdown(
            label: 'Type',
            value: selectedType,
            items: {
              null: 'All types',
              for (final t in TaskType.values) t.toJsonStr(): t.toJsonStr(),
            },
            onChanged: onTypeChanged,
          ),
          const SizedBox(width: 8),
          _Dropdown(
            label: 'Severity',
            value: selectedSeverity,
            items: const {
              null: 'All',
              'critical': 'Critical',
              'high': 'High',
              'medium': 'Medium',
              'low': 'Low',
            },
            onChanged: onSeverityChanged,
          ),
          const SizedBox(width: 8),
          _Dropdown(
            label: 'Status',
            value: selectedStatus,
            items: {
              null: 'All statuses',
              for (final s in TaskStatus.values) s.toJsonStr(): s.displayName,
            },
            onChanged: onStatusChanged,
          ),
          if (_hasAny) ...[
            const SizedBox(width: 8),
            TextButton.icon(
              onPressed: onClear,
              icon: const Icon(Icons.clear, size: 16),
              label: const Text('Clear'),
            ),
          ],
        ],
      ),
    );
  }
}

class _Dropdown extends StatelessWidget {
  const _Dropdown({
    required this.label,
    required this.value,
    required this.items,
    required this.onChanged,
  });

  final String label;
  final String? value;
  final Map<String?, String> items;
  final ValueChanged<String?>? onChanged;

  @override
  Widget build(BuildContext context) {
    return DropdownButton<String?>(
      hint: Text(label),
      value: value,
      isDense: true,
      items: items.entries
          .map((e) => DropdownMenuItem(value: e.key, child: Text(e.value)))
          .toList(),
      onChanged: onChanged,
    );
  }
}
