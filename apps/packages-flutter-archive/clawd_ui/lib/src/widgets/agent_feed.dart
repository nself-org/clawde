import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'package:clawd_core/clawd_core.dart';
import '../theme/clawd_theme.dart';

/// A real-time feed of agent actions: tool calls, file changes,
/// test results, and approval requests. Filterable by agent type.
class AgentFeed extends ConsumerStatefulWidget {
  const AgentFeed({super.key});

  @override
  ConsumerState<AgentFeed> createState() => _AgentFeedState();
}

class _AgentFeedState extends ConsumerState<AgentFeed> {
  String? _typeFilter;

  @override
  Widget build(BuildContext context) {
    final agentsAsync = ref.watch(agentsProvider);
    final approvalsAsync = ref.watch(approvalQueueProvider);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // ── Type filter chips ──────────────────────────────────────────────
        agentsAsync.when(
          data: (agents) {
            final types = agents.map((a) => a.agentType).toSet().toList()..sort();
            return _TypeFilterBar(
              types: types,
              selected: _typeFilter,
              onChanged: (t) => setState(() => _typeFilter = t),
            );
          },
          loading: () => const SizedBox(height: 40),
          error: (_, __) => const SizedBox(height: 40),
        ),

        // ── Feed ──────────────────────────────────────────────────────────
        Expanded(
          child: agentsAsync.when(
            loading: () => const Center(
              child: CircularProgressIndicator(color: ClawdTheme.claw),
            ),
            error: (e, _) => Center(
              child: Text(
                'Failed to load agents: $e',
                style: const TextStyle(color: Colors.red, fontSize: 12),
              ),
            ),
            data: (agents) {
              final filtered = _typeFilter == null
                  ? agents
                  : agents.where((a) => a.agentType == _typeFilter).toList();

              if (filtered.isEmpty) {
                return Center(
                  child: Text(
                    _typeFilter == null
                        ? 'No active agents'
                        : 'No $_typeFilter agents',
                    style: TextStyle(
                      fontSize: 13,
                      color: Colors.white.withValues(alpha: 0.35),
                    ),
                  ),
                );
              }

              final pendingApprovals = approvalsAsync.valueOrNull ?? [];

              return ListView.builder(
                padding: const EdgeInsets.symmetric(vertical: 8),
                itemCount: filtered.length,
                itemBuilder: (context, i) {
                  final agent = filtered[i];
                  final agentApprovals = pendingApprovals
                      .where((a) => a.agentId == agent.agentId)
                      .toList();
                  return _AgentFeedItem(
                    agent: agent,
                    pendingApprovals: agentApprovals,
                  );
                },
              );
            },
          ),
        ),
      ],
    );
  }
}

// ── Type filter bar ─────────────────────────────────────────────────────────────

class _TypeFilterBar extends StatelessWidget {
  const _TypeFilterBar({
    required this.types,
    required this.selected,
    required this.onChanged,
  });
  final List<String> types;
  final String? selected;
  final ValueChanged<String?> onChanged;

  @override
  Widget build(BuildContext context) {
    final options = [null, ...types];
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: Row(
        children: options.map((type) {
          final label = type ?? 'All';
          final isSelected = selected == type;
          return Padding(
            padding: const EdgeInsets.only(right: 6),
            child: FilterChip(
              label: Text(label),
              selected: isSelected,
              onSelected: (_) => onChanged(type),
              selectedColor: ClawdTheme.claw.withValues(alpha: 0.25),
              checkmarkColor: ClawdTheme.clawLight,
              labelStyle: TextStyle(
                fontSize: 11,
                color: isSelected ? ClawdTheme.clawLight : Colors.white60,
              ),
              backgroundColor: ClawdTheme.surfaceElevated,
              side: BorderSide(
                color: isSelected ? ClawdTheme.claw : ClawdTheme.surfaceBorder,
              ),
            ),
          );
        }).toList(),
      ),
    );
  }
}

// ── Single agent feed item ─────────────────────────────────────────────────────

class _AgentFeedItem extends StatelessWidget {
  const _AgentFeedItem({
    required this.agent,
    required this.pendingApprovals,
  });
  final AgentView agent;
  final List<ApprovalRequest> pendingApprovals;

  Color _colorForStatus(AgentViewStatus status) => switch (status) {
        AgentViewStatus.active => Colors.green,
        AgentViewStatus.idle => Colors.amber,
        AgentViewStatus.offline => Colors.white38,
      };

  String _timeSince(int? unixSec) {
    if (unixSec == null) return '—';
    final dt = DateTime.fromMillisecondsSinceEpoch(unixSec * 1000);
    final diff = DateTime.now().difference(dt);
    if (diff.inSeconds < 60) return '${diff.inSeconds}s ago';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    return '${diff.inHours}h ago';
  }

  @override
  Widget build(BuildContext context) {
    final statusColor = _colorForStatus(agent.status);

    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 3),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: pendingApprovals.isNotEmpty
              ? Colors.amber.withValues(alpha: 0.5)
              : ClawdTheme.surfaceBorder,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header row
          Row(
            children: [
              const Icon(Icons.smart_toy_outlined,
                  size: 14, color: ClawdTheme.clawLight),
              const SizedBox(width: 6),
              Text(
                agent.agentType,
                style: const TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: Colors.white,
                ),
              ),
              const Spacer(),
              // Status badge
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: statusColor.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Container(
                      width: 5,
                      height: 5,
                      decoration: BoxDecoration(
                        color: statusColor,
                        shape: BoxShape.circle,
                      ),
                    ),
                    const SizedBox(width: 4),
                    Text(
                      agent.status.name,
                      style: TextStyle(
                        fontSize: 10,
                        fontWeight: FontWeight.w600,
                        color: statusColor,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),

          // Task ID + last seen
          Row(
            children: [
              const Icon(Icons.task_alt, size: 11, color: Colors.white38),
              const SizedBox(width: 4),
              Text(
                'Task ${agent.currentTaskId ?? '—'}',
                style: const TextStyle(fontSize: 11, color: Colors.white54),
              ),
              const SizedBox(width: 12),
              const Icon(Icons.favorite_border,
                  size: 11, color: Colors.white38),
              const SizedBox(width: 4),
              Text(
                _timeSince(agent.lastSeen),
                style: const TextStyle(fontSize: 11, color: Colors.white38),
              ),
            ],
          ),

          // Pending approval warning
          if (pendingApprovals.isNotEmpty) ...[
            const SizedBox(height: 8),
            Row(
              children: [
                const Icon(Icons.warning_amber_rounded,
                    size: 13, color: Colors.amber),
                const SizedBox(width: 6),
                Text(
                  '${pendingApprovals.length} pending approval${pendingApprovals.length == 1 ? '' : 's'}',
                  style: const TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: Colors.amber,
                  ),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}
