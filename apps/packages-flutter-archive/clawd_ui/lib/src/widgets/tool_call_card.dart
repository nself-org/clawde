import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';
import '../theme/clawd_theme.dart';

/// Displays a pending [ToolCall] with approve/reject actions.
/// Used in both the desktop tool-approval panel and the mobile modal sheet.
class ToolCallCard extends StatelessWidget {
  const ToolCallCard({
    super.key,
    required this.toolCall,
    this.onApprove,
    this.onReject,
  });

  final ToolCall toolCall;
  final VoidCallback? onApprove;
  final VoidCallback? onReject;

  bool get _isPending => toolCall.status == ToolCallStatus.pending;

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      color: ClawdTheme.surfaceElevated,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: BorderSide(
          color: _isPending ? ClawdTheme.warning : ClawdTheme.surfaceBorder,
        ),
      ),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  Icons.terminal,
                  size: 14,
                  color: _isPending ? ClawdTheme.warning : Colors.grey,
                ),
                const SizedBox(width: 6),
                Text(
                  toolCall.toolName,
                  style: const TextStyle(
                    fontWeight: FontWeight.w600,
                    fontSize: 13,
                    fontFamily: 'monospace',
                  ),
                ),
                const Spacer(),
                _StatusChip(status: toolCall.status),
              ],
            ),
            if (toolCall.input.isNotEmpty) ...[
              const SizedBox(height: 8),
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: ClawdTheme.surface,
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  _formatInput(toolCall.input),
                  style: const TextStyle(
                    fontSize: 11,
                    fontFamily: 'monospace',
                    height: 1.4,
                  ),
                  maxLines: 6,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
            if (_isPending && (onApprove != null || onReject != null)) ...[
              const SizedBox(height: 10),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  if (onReject != null)
                    TextButton(
                      onPressed: onReject,
                      child: const Text(
                        'Reject',
                        style: TextStyle(color: ClawdTheme.error),
                      ),
                    ),
                  const SizedBox(width: 8),
                  if (onApprove != null)
                    FilledButton(
                      onPressed: onApprove,
                      style: FilledButton.styleFrom(
                        backgroundColor: ClawdTheme.success,
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(
                          horizontal: 16,
                          vertical: 8,
                        ),
                      ),
                      child: const Text('Approve'),
                    ),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _formatInput(Map<String, dynamic> input) {
    return input.entries
        .map((e) => '${e.key}: ${e.value}')
        .join('\n');
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.status});
  final ToolCallStatus status;

  (String, Color) get _label => switch (status) {
        ToolCallStatus.pending => ('Pending', ClawdTheme.warning),
        ToolCallStatus.running => ('Running', ClawdTheme.info),
        ToolCallStatus.completed => ('Done', ClawdTheme.success),
        ToolCallStatus.error => ('Error', ClawdTheme.error),
      };

  @override
  Widget build(BuildContext context) {
    final (label, color) = _label;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha:0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha:0.4)),
      ),
      child: Text(
        label,
        style: TextStyle(fontSize: 10, color: color, fontWeight: FontWeight.w600),
      ),
    );
  }
}
