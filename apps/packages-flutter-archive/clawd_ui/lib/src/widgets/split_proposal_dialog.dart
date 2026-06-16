// SPDX-License-Identifier: MIT
import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Dialog shown when a prompt is detected as Complex or DeepReasoning (SI.T16).
///
/// Presents the complexity classification and proposed subtasks to the user.
/// Returns [SplitProposalResult] indicating what the user chose:
///   - [SplitProposalResult.sendAsIs]  — user wants to send the prompt unchanged.
///   - [SplitProposalResult.dismissed] — user closed the dialog without action.
///
/// Since full multi-session splitting is a future feature, the "split" path
/// currently sends as-is while making the user aware of the proposal.
///
/// Usage:
/// ```dart
/// final result = await showSplitProposalDialog(
///   context: context,
///   complexity: 'Complex',
///   reason: 'Prompt contains multiple distinct tasks',
///   subtasks: ['Add tests', 'Fix linting', 'Update docs'],
/// );
/// if (result != SplitProposalResult.dismissed) { _sendMessage(text); }
/// ```
enum SplitProposalResult {
  /// User chose to send the message as typed.
  sendAsIs,

  /// User dismissed the dialog without choosing an action.
  dismissed,
}

Future<SplitProposalResult> showSplitProposalDialog({
  required BuildContext context,
  required String complexity,
  required String reason,
  required List<String> subtasks,
}) async {
  final result = await showModalBottomSheet<SplitProposalResult>(
    context: context,
    backgroundColor: Colors.transparent,
    builder: (_) => _SplitProposalSheet(
      complexity: complexity,
      reason: reason,
      subtasks: subtasks,
    ),
  );
  return result ?? SplitProposalResult.dismissed;
}

class _SplitProposalSheet extends StatelessWidget {
  const _SplitProposalSheet({
    required this.complexity,
    required this.reason,
    required this.subtasks,
  });

  final String complexity;
  final String reason;
  final List<String> subtasks;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const SizedBox(height: 8),
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: Colors.white24,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          // Header
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 16, 20, 4),
            child: Row(
              children: [
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                  decoration: BoxDecoration(
                    color: ClawdTheme.warning.withValues(alpha: 0.15),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(
                        color: ClawdTheme.warning.withValues(alpha: 0.4)),
                  ),
                  child: Text(
                    complexity,
                    style: const TextStyle(
                      fontSize: 11,
                      color: ClawdTheme.warning,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
                const SizedBox(width: 10),
                const Text(
                  'Complex Prompt',
                  style: TextStyle(
                    fontSize: 15,
                    fontWeight: FontWeight.w700,
                    color: Colors.white,
                  ),
                ),
              ],
            ),
          ),
          // Reason
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 4, 20, 12),
            child: Text(
              reason,
              style: const TextStyle(fontSize: 12, color: Colors.white54),
            ),
          ),
          // Subtask list
          if (subtasks.isNotEmpty) ...[
            const Divider(height: 1),
            const Padding(
              padding: EdgeInsets.fromLTRB(20, 12, 20, 6),
              child: Text(
                'Suggested breakdown',
                style: TextStyle(
                  fontSize: 11,
                  color: Colors.white38,
                  fontWeight: FontWeight.w600,
                  letterSpacing: 0.5,
                ),
              ),
            ),
            ...subtasks.asMap().entries.map(
                  (e) => Padding(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 20, vertical: 5),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '${e.key + 1}.',
                          style: const TextStyle(
                            fontSize: 13,
                            color: ClawdTheme.claw,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Text(
                            e.value,
                            style: const TextStyle(
                                fontSize: 13, color: Colors.white70),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
          ],
          // Actions
          const SizedBox(height: 16),
          const Divider(height: 1),
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 12, 20, 24),
            child: Row(
              children: [
                Expanded(
                  child: OutlinedButton(
                    onPressed: () =>
                        Navigator.pop(context, SplitProposalResult.dismissed),
                    style: OutlinedButton.styleFrom(
                      side: const BorderSide(color: Colors.white24),
                      foregroundColor: Colors.white54,
                      padding: const EdgeInsets.symmetric(vertical: 12),
                    ),
                    child: const Text('Cancel'),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  flex: 2,
                  child: FilledButton(
                    onPressed: () =>
                        Navigator.pop(context, SplitProposalResult.sendAsIs),
                    style: FilledButton.styleFrom(
                      backgroundColor: ClawdTheme.claw,
                      foregroundColor: Colors.white,
                      padding: const EdgeInsets.symmetric(vertical: 12),
                    ),
                    child: const Text('Send as-is'),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
