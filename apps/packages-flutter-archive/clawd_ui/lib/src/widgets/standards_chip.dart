import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Small session-header chip showing the count of active coding standards.
///
/// Hidden when [count] is 0 (no standards loaded yet or RPC not available).
/// Tapping opens [_StandardsSheet] with the list.
class StandardsChip extends StatelessWidget {
  const StandardsChip({
    super.key,
    required this.count,
    this.standards = const [],
  });

  final int count;

  /// Full list of standard rule descriptions shown in the bottom sheet.
  final List<String> standards;

  @override
  Widget build(BuildContext context) {
    if (count == 0) return const SizedBox.shrink();
    return Tooltip(
      message: '$count coding standard${count == 1 ? '' : 's'} active',
      child: InkWell(
        onTap: () => _showSheet(context),
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
          decoration: BoxDecoration(
            color: const Color(0xFF1a472a).withValues(alpha: 0.6),
            borderRadius: BorderRadius.circular(10),
            border:
                Border.all(color: Colors.green.withValues(alpha: 0.35)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.rule, size: 11, color: Colors.green),
              const SizedBox(width: 4),
              Text(
                '$count std',
                style: const TextStyle(
                  fontSize: 10,
                  color: Colors.green,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _showSheet(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      builder: (_) => _StandardsSheet(standards: standards),
    );
  }
}

class _StandardsSheet extends StatelessWidget {
  const _StandardsSheet({required this.standards});
  final List<String> standards;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(height: 8),
          Container(
            width: 36,
            height: 4,
            decoration: BoxDecoration(
              color: Colors.white24,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          const Padding(
            padding: EdgeInsets.fromLTRB(20, 16, 20, 4),
            child: Row(
              children: [
                Icon(Icons.rule, size: 16, color: Colors.green),
                SizedBox(width: 8),
                Text(
                  'Active Standards',
                  style: TextStyle(
                    fontSize: 15,
                    fontWeight: FontWeight.w700,
                    color: Colors.white,
                  ),
                ),
              ],
            ),
          ),
          const Padding(
            padding: EdgeInsets.fromLTRB(20, 0, 20, 12),
            child: Text(
              'Injected into the system prompt for this session.',
              style: TextStyle(fontSize: 12, color: Colors.white38),
            ),
          ),
          const Divider(height: 1),
          if (standards.isEmpty)
            const Padding(
              padding: EdgeInsets.all(24),
              child: Text(
                'No standards loaded.',
                style: TextStyle(fontSize: 13, color: Colors.white38),
              ),
            )
          else
            ListView.separated(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              itemCount: standards.length,
              separatorBuilder: (_, __) =>
                  const Divider(height: 1, indent: 20),
              itemBuilder: (_, i) => Padding(
                padding: const EdgeInsets.symmetric(
                    horizontal: 20, vertical: 10),
                child: Text(
                  standards[i],
                  style: const TextStyle(
                      fontSize: 12, color: Colors.white70),
                ),
              ),
            ),
          const SizedBox(height: 24),
        ],
      ),
    );
  }
}
