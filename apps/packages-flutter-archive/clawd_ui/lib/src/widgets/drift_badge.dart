import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Non-blocking drift notification badge for the session sidebar.
///
/// Shows a small orange count pill when [count] > 0. Hidden when count is 0.
/// Tapping opens [_DriftSheet] with the item list.
class DriftBadge extends StatelessWidget {
  const DriftBadge({
    super.key,
    required this.count,
    this.items = const [],
  });

  final int count;

  /// Drift item descriptions returned by `drift.list`.
  final List<String> items;

  @override
  Widget build(BuildContext context) {
    if (count == 0) return const SizedBox.shrink();
    return Tooltip(
      message: '$count drift item${count == 1 ? '' : 's'} detected',
      child: InkWell(
        onTap: () => _showSheet(context),
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
          decoration: BoxDecoration(
            color: Colors.orange.withValues(alpha: 0.15),
            borderRadius: BorderRadius.circular(10),
            border: Border.all(color: Colors.orange.withValues(alpha: 0.4)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.warning_amber_rounded,
                  size: 11, color: Colors.orange),
              const SizedBox(width: 4),
              Text(
                '$count',
                style: const TextStyle(
                  fontSize: 10,
                  color: Colors.orange,
                  fontWeight: FontWeight.w700,
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
      builder: (_) => _DriftSheet(items: items),
    );
  }
}

class _DriftSheet extends StatelessWidget {
  const _DriftSheet({required this.items});
  final List<String> items;

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
                Icon(Icons.warning_amber_rounded,
                    size: 16, color: Colors.orange),
                SizedBox(width: 8),
                Text(
                  'Drift Detected',
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
              'Implementation has drifted from spec. Non-blocking — your workflow continues.',
              style: TextStyle(fontSize: 12, color: Colors.white38),
            ),
          ),
          const Divider(height: 1),
          if (items.isEmpty)
            const Padding(
              padding: EdgeInsets.all(24),
              child: Text(
                'No drift items.',
                style: TextStyle(fontSize: 13, color: Colors.white38),
              ),
            )
          else
            ConstrainedBox(
              constraints: const BoxConstraints(maxHeight: 300),
              child: ListView.separated(
                shrinkWrap: true,
                itemCount: items.length,
                separatorBuilder: (_, __) =>
                    const Divider(height: 1, indent: 20),
                itemBuilder: (_, i) => Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 20, vertical: 10),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Padding(
                        padding: EdgeInsets.only(top: 3),
                        child: Icon(Icons.circle,
                            size: 5, color: Colors.orange),
                      ),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Text(
                          items[i],
                          style: const TextStyle(
                              fontSize: 12, color: Colors.white70),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          const SizedBox(height: 24),
        ],
      ),
    );
  }
}
