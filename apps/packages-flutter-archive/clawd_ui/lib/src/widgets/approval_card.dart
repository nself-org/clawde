import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';
import '../theme/clawd_theme.dart';

/// Displays a single approval request with Approve / Approve For Task /
/// Deny / Clarify action buttons.
class ApprovalCard extends StatelessWidget {
  final ApprovalRequest request;

  /// Called when the user taps "Approve Once".
  final VoidCallback onApprove;

  /// Called when the user taps "Approve For Task".
  final VoidCallback? onApproveForTask;

  /// Called when the user taps "Deny".
  final VoidCallback onDeny;

  /// Called when the user taps "Clarify" (optional — shown only when provided).
  final VoidCallback? onClarify;

  const ApprovalCard({
    required this.request,
    required this.onApprove,
    required this.onDeny,
    this.onApproveForTask,
    this.onClarify,
    super.key,
  });

  Color get _riskColor => switch (request.risk) {
        'low' => Colors.green,
        'medium' => Colors.amber,
        'high' => Colors.orange,
        'critical' => Colors.red,
        _ => Colors.amber,
      };

  IconData get _riskIcon => switch (request.risk) {
        'low' => Icons.check_circle_outline,
        'medium' => Icons.warning_amber_outlined,
        'high' => Icons.error_outline,
        'critical' => Icons.dangerous_outlined,
        _ => Icons.warning_amber_outlined,
      };

  String _relativeTime(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inSeconds < 60) return '${diff.inSeconds}s ago';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    return '${diff.inHours}h ago';
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color: _riskColor.withValues(alpha: 0.4),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // ── Header ──────────────────────────────────────────────────────
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
            decoration: BoxDecoration(
              color: _riskColor.withValues(alpha: 0.08),
              borderRadius: const BorderRadius.vertical(top: Radius.circular(9)),
            ),
            child: Row(
              children: [
                Icon(_riskIcon, size: 15, color: _riskColor),
                const SizedBox(width: 8),
                _RiskBadge(risk: request.risk, color: _riskColor),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(
                    request.tool,
                    style: const TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w700,
                      color: Colors.white,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                Text(
                  _relativeTime(request.requestedAt),
                  style: const TextStyle(fontSize: 10, color: Colors.white38),
                ),
              ],
            ),
          ),

          // ── Body ────────────────────────────────────────────────────────
          Padding(
            padding: const EdgeInsets.fromLTRB(14, 10, 14, 0),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                // Task + Agent IDs
                _MetaRow(
                  icon: Icons.task_alt,
                  label: 'Task',
                  value: request.taskId,
                ),
                const SizedBox(height: 4),
                _MetaRow(
                  icon: Icons.smart_toy_outlined,
                  label: 'Agent',
                  value: request.agentId,
                ),
                const SizedBox(height: 10),

                // Args summary
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(10),
                  decoration: BoxDecoration(
                    color: ClawdTheme.surface,
                    borderRadius: BorderRadius.circular(6),
                    border: Border.all(color: ClawdTheme.surfaceBorder),
                  ),
                  child: Text(
                    request.argsSummary,
                    style: const TextStyle(
                      fontSize: 11,
                      fontFamily: 'monospace',
                      color: Colors.white70,
                      height: 1.5,
                    ),
                  ),
                ),
              ],
            ),
          ),

          // ── Actions ─────────────────────────────────────────────────────
          Padding(
            padding: const EdgeInsets.all(12),
            child: Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                _ApproveButton(
                  label: 'Approve Once',
                  icon: Icons.check,
                  color: Colors.green,
                  onTap: onApprove,
                ),
                if (onApproveForTask != null)
                  _ApproveButton(
                    label: 'Approve For Task',
                    icon: Icons.playlist_add_check,
                    color: Colors.teal,
                    onTap: onApproveForTask!,
                  ),
                _ApproveButton(
                  label: 'Deny',
                  icon: Icons.close,
                  color: Colors.red,
                  onTap: onDeny,
                ),
                if (onClarify != null)
                  _ApproveButton(
                    label: 'Clarify',
                    icon: Icons.chat_bubble_outline,
                    color: Colors.white54,
                    onTap: onClarify!,
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

// ── Sub-widgets ────────────────────────────────────────────────────────────────

class _RiskBadge extends StatelessWidget {
  const _RiskBadge({required this.risk, required this.color});
  final String risk;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        risk.toUpperCase(),
        style: TextStyle(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
          letterSpacing: 0.5,
        ),
      ),
    );
  }
}

class _MetaRow extends StatelessWidget {
  const _MetaRow({
    required this.icon,
    required this.label,
    required this.value,
  });
  final IconData icon;
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(icon, size: 12, color: Colors.white38),
        const SizedBox(width: 6),
        Text(
          '$label: ',
          style: const TextStyle(fontSize: 11, color: Colors.white38),
        ),
        Expanded(
          child: Text(
            value,
            style: const TextStyle(fontSize: 11, color: Colors.white70),
            overflow: TextOverflow.ellipsis,
          ),
        ),
      ],
    );
  }
}

class _ApproveButton extends StatelessWidget {
  const _ApproveButton({
    required this.label,
    required this.icon,
    required this.color,
    required this.onTap,
  });
  final String label;
  final IconData icon;
  final Color color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: color.withValues(alpha: 0.3)),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 12, color: color),
            const SizedBox(width: 5),
            Text(
              label,
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: color,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
