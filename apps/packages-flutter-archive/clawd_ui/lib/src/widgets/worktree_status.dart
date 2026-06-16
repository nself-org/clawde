import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'package:clawd_core/clawd_core.dart';
import '../theme/clawd_theme.dart';

// Provider that fetches WorktreeStatus for a given taskId.
final _worktreeStatusProvider =
    FutureProvider.family<WorktreeStatus?, String>((ref, taskId) async {
  final client = ref.read(daemonProvider.notifier).client;
  try {
    final result = await client.call<Map<String, dynamic>>('worktrees.list');
    final list = result['worktrees'] as List<dynamic>? ?? [];
    final entry = list.cast<Map<String, dynamic>>().where(
          (w) =>
              (w['task_id'] as String?) == taskId ||
              (w['taskId'] as String?) == taskId,
        ).firstOrNull;
    if (entry == null) return null;
    return WorktreeStatus.fromJson(entry);
  } catch (_) {
    return null;
  }
});

/// Shows a summary of the git worktree associated with a task:
/// branch name, changed file count, merge status,
/// and quick [Merge] / [Abandon] action buttons.
class WorktreeStatusWidget extends ConsumerWidget {
  final String taskId;
  const WorktreeStatusWidget({required this.taskId, super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final statusAsync = ref.watch(_worktreeStatusProvider(taskId));

    return statusAsync.when(
      loading: () => const _WorktreeCard(child: _LoadingRow()),
      error: (e, _) => _WorktreeCard(
        child: Row(
          children: [
            const Icon(Icons.error_outline, size: 14, color: Colors.red),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                'Worktree unavailable: $e',
                style: const TextStyle(fontSize: 11, color: Colors.red),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
      data: (status) {
        if (status == null) {
          return const _WorktreeCard(
            child: Row(
              children: [
                Icon(Icons.folder_off_outlined, size: 14, color: Colors.white38),
                SizedBox(width: 6),
                Text(
                  'No worktree for this task',
                  style: TextStyle(fontSize: 11, color: Colors.white38),
                ),
              ],
            ),
          );
        }
        return _WorktreeCard(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Branch + stale badge
              Row(
                children: [
                  const Icon(Icons.call_split, size: 14, color: ClawdTheme.clawLight),
                  const SizedBox(width: 6),
                  Text(
                    status.branch,
                    style: const TextStyle(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      color: Colors.white,
                    ),
                  ),
                  if (status.isStale) ...[
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 5, vertical: 1),
                      decoration: BoxDecoration(
                        color: Colors.amber.withValues(alpha: 0.2),
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: const Text(
                        'stale',
                        style: TextStyle(
                          fontSize: 10,
                          fontWeight: FontWeight.w600,
                          color: Colors.amber,
                        ),
                      ),
                    ),
                  ],
                  const Spacer(),
                  // Change count chip
                  _ChangeCountBadge(count: status.changeCount),
                ],
              ),
              const SizedBox(height: 4),

              // Path
              Text(
                status.path,
                style:
                    const TextStyle(fontSize: 10, color: Colors.white38),
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: 10),

              // Action buttons — wired to worktrees.accept / worktrees.reject RPCs.
              Row(
                children: [
                  if (status.pendingMerge) ...[
                    _ActionButton(
                      label: 'Merge',
                      icon: Icons.merge,
                      color: Colors.green,
                      onTap: () {
                        ref
                            .read(daemonProvider.notifier)
                            .client
                            .acceptWorktree(taskId)
                            .then((_) =>
                                ref.invalidate(_worktreeStatusProvider(taskId)))
                            .ignore();
                      },
                    ),
                    const SizedBox(width: 8),
                  ],
                  _ActionButton(
                    label: 'Abandon',
                    icon: Icons.delete_outline,
                    color: Colors.red,
                    onTap: () {
                      ref
                          .read(daemonProvider.notifier)
                          .client
                          .rejectWorktree(taskId)
                          .then((_) =>
                              ref.invalidate(_worktreeStatusProvider(taskId)))
                          .ignore();
                    },
                  ),
                ],
              ),
            ],
          ),
        );
      },
    );
  }
}

// ── Sub-widgets ────────────────────────────────────────────────────────────────

class _WorktreeCard extends StatelessWidget {
  const _WorktreeCard({required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: ClawdTheme.surfaceBorder),
      ),
      child: child,
    );
  }
}

class _LoadingRow extends StatelessWidget {
  const _LoadingRow();

  @override
  Widget build(BuildContext context) {
    return const Row(
      children: [
        SizedBox(
          width: 14,
          height: 14,
          child: CircularProgressIndicator(strokeWidth: 2, color: ClawdTheme.claw),
        ),
        SizedBox(width: 8),
        Text(
          'Loading worktree...',
          style: TextStyle(fontSize: 11, color: Colors.white38),
        ),
      ],
    );
  }
}

class _ChangeCountBadge extends StatelessWidget {
  const _ChangeCountBadge({required this.count});
  final int count;

  @override
  Widget build(BuildContext context) {
    final color = count == 0 ? Colors.white38 : Colors.orangeAccent;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        '$count ${count == 1 ? 'file' : 'files'} changed',
        style: TextStyle(
          fontSize: 10,
          fontWeight: FontWeight.w600,
          color: color,
        ),
      ),
    );
  }
}

class _ActionButton extends StatelessWidget {
  const _ActionButton({
    required this.label,
    required this.icon,
    required this.color,
    required this.onTap,
  });
  final String label;
  final IconData icon;
  final Color color;
  // M10: onTap is nullable — null disables the button.
  // Disabled until worktrees.merge/abandon RPCs are implemented.
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final isDisabled = onTap == null;
    final effectiveColor = isDisabled ? color.withValues(alpha: 0.3) : color;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
        decoration: BoxDecoration(
          color: effectiveColor.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: effectiveColor.withValues(alpha: 0.3)),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 12, color: effectiveColor),
            const SizedBox(width: 4),
            Text(
              label,
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: effectiveColor,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
