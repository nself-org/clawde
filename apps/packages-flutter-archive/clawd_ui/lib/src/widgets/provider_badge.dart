import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';
import '../theme/clawd_theme.dart';

/// A compact badge showing which AI provider a session uses.
class ProviderBadge extends StatelessWidget {
  const ProviderBadge({super.key, required this.provider});

  final ProviderType provider;

  (String, Color) get _label => switch (provider) {
        ProviderType.claude => ('Claude', ClawdTheme.claudeColor),
        ProviderType.codex => ('Codex', ClawdTheme.codexColor),
        ProviderType.cursor => ('Cursor', ClawdTheme.cursorColor),
      };

  @override
  Widget build(BuildContext context) {
    final (label, color) = _label;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha:0.12),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha:0.35)),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 10,
          color: color,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.2,
        ),
      ),
    );
  }
}
