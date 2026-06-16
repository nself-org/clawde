import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Small session-header chip showing the count of detected provider contexts.
///
/// Hidden when [count] is 0. Tapping opens [_ProviderKnowledgeSheet].
class ProviderKnowledgeChip extends StatelessWidget {
  const ProviderKnowledgeChip({
    super.key,
    required this.count,
    this.providers = const [],
  });

  final int count;

  /// Provider names detected (e.g. ["Hetzner", "Stripe", "Cloudflare"]).
  final List<String> providers;

  @override
  Widget build(BuildContext context) {
    if (count == 0) return const SizedBox.shrink();
    return Tooltip(
      message: '$count provider context${count == 1 ? '' : 's'} active',
      child: InkWell(
        onTap: () => _showSheet(context),
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
          decoration: BoxDecoration(
            color: const Color(0xFF1a2a47).withValues(alpha: 0.6),
            borderRadius: BorderRadius.circular(10),
            border:
                Border.all(color: Colors.blue.withValues(alpha: 0.35)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.cloud_outlined, size: 11, color: Colors.blue),
              const SizedBox(width: 4),
              Text(
                '$count prov',
                style: const TextStyle(
                  fontSize: 10,
                  color: Colors.blue,
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
      builder: (_) => _ProviderKnowledgeSheet(providers: providers),
    );
  }
}

class _ProviderKnowledgeSheet extends StatelessWidget {
  const _ProviderKnowledgeSheet({required this.providers});
  final List<String> providers;

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
                Icon(Icons.cloud_outlined, size: 16, color: Colors.blue),
                SizedBox(width: 8),
                Text(
                  'Provider Knowledge',
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
              'Provider-specific context injected into the system prompt.',
              style: TextStyle(fontSize: 12, color: Colors.white38),
            ),
          ),
          const Divider(height: 1),
          if (providers.isEmpty)
            const Padding(
              padding: EdgeInsets.all(24),
              child: Text(
                'No provider contexts detected.',
                style: TextStyle(fontSize: 13, color: Colors.white38),
              ),
            )
          else
            ListView.separated(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              itemCount: providers.length,
              separatorBuilder: (_, __) =>
                  const Divider(height: 1, indent: 20),
              itemBuilder: (_, i) => Padding(
                padding: const EdgeInsets.symmetric(
                    horizontal: 20, vertical: 10),
                child: Row(
                  children: [
                    const Icon(Icons.circle, size: 6, color: Colors.blue),
                    const SizedBox(width: 10),
                    Text(
                      providers[i],
                      style: const TextStyle(
                          fontSize: 12, color: Colors.white70),
                    ),
                  ],
                ),
              ),
            ),
          const SizedBox(height: 24),
        ],
      ),
    );
  }
}
