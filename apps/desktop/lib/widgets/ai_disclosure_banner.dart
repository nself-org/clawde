import 'package:flutter/material.dart';

/// AIDisclosureBanner — EU AI Act disclosure for ClawDE (S50-T17)
///
/// Persistent banner indicating that code generation and suggestions are
/// AI-generated. Required by the EU AI Act (Regulation (EU) 2024/1689)
/// for AI systems interacting with users.
///
/// Shown persistently in the ClawDE interface alongside the AI assistant.
/// Does NOT block interaction.
///
/// Usage:
///   AIDisclosureBanner(
///     onLearnMore: () => launchUrl(Uri.parse('https://nself.org/legal/ai-aup')),
///   )
class AIDisclosureBanner extends StatelessWidget {
  const AIDisclosureBanner({
    super.key,
    this.onLearnMore,
  });

  /// Called when the user clicks the "AI policy" link.
  final VoidCallback? onLearnMore;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;

    return Container(
      height: 28,
      decoration: BoxDecoration(
        color: colorScheme.primaryContainer.withValues(alpha: 0.08),
        border: Border(
          bottom: BorderSide(
            color: colorScheme.outlineVariant.withValues(alpha: 0.2),
          ),
        ),
      ),
      child: Row(
        children: [
          const SizedBox(width: 12),
          Icon(
            Icons.auto_awesome_rounded,
            size: 12,
            color: colorScheme.primary.withValues(alpha: 0.8),
          ),
          const SizedBox(width: 6),
          Text(
            'Powered by AI — suggestions may be inaccurate. Review all generated code before use.',
            style: theme.textTheme.labelSmall?.copyWith(
              color: colorScheme.onSurfaceVariant.withValues(alpha: 0.8),
              fontSize: 11,
            ),
          ),
          if (onLearnMore != null) ...[
            const SizedBox(width: 6),
            GestureDetector(
              onTap: onLearnMore,
              child: Text(
                'AI policy',
                style: theme.textTheme.labelSmall?.copyWith(
                  color: colorScheme.primary,
                  decoration: TextDecoration.underline,
                  decorationColor: colorScheme.primary,
                  fontSize: 11,
                ),
              ),
            ),
          ],
          const Spacer(),
        ],
      ),
    );
  }
}
