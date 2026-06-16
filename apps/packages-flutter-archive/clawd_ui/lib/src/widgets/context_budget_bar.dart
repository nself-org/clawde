import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Displays a horizontal bar showing how much of the context budget
/// (token limit) is currently used.
///
/// The bar changes color based on usage percentage:
/// - Green (0-60%): plenty of room
/// - Yellow (60-80%): getting close
/// - Red (80-100%): nearly full
///
/// Usage:
/// ```dart
/// ContextBudgetBar(
///   currentTokens: 42000,
///   maxTokens: 100000,
/// )
/// ```
class ContextBudgetBar extends StatelessWidget {
  const ContextBudgetBar({
    super.key,
    required this.currentTokens,
    required this.maxTokens,
    this.height = 6.0,
    this.showLabel = true,
  });

  /// Estimated token count for the current context.
  final int currentTokens;

  /// Maximum token budget for the model/session.
  final int maxTokens;

  /// Height of the progress bar.
  final double height;

  /// Whether to show the "current / max tokens" text label.
  final bool showLabel;

  double get _fraction =>
      maxTokens > 0 ? (currentTokens / maxTokens).clamp(0.0, 1.0) : 0.0;

  Color get _barColor {
    final pct = _fraction * 100;
    if (pct >= 80) return ClawdTheme.error;
    if (pct >= 60) return ClawdTheme.warning;
    return ClawdTheme.success;
  }

  String get _label {
    final current = _formatTokenCount(currentTokens);
    final max = _formatTokenCount(maxTokens);
    return '$current / $max tokens';
  }

  /// Formats a token count for display, e.g. `42.0k` or `128`.
  static String _formatTokenCount(int tokens) {
    if (tokens >= 1000) {
      final k = tokens / 1000;
      // Show one decimal place for counts under 100k.
      if (k < 100) return '${k.toStringAsFixed(1)}k';
      return '${k.round()}k';
    }
    return tokens.toString();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (showLabel)
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  'Context',
                  style: TextStyle(
                    fontSize: 11,
                    color: Colors.white.withValues(alpha: 0.5),
                  ),
                ),
                Text(
                  _label,
                  style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w500,
                    color: _barColor,
                  ),
                ),
              ],
            ),
          ),
        ClipRRect(
          borderRadius: BorderRadius.circular(height / 2),
          child: SizedBox(
            height: height,
            child: Stack(
              children: [
                // Background track
                Container(
                  decoration: BoxDecoration(
                    color: ClawdTheme.surfaceBorder,
                    borderRadius: BorderRadius.circular(height / 2),
                  ),
                ),
                // Filled portion
                FractionallySizedBox(
                  widthFactor: _fraction,
                  child: Container(
                    decoration: BoxDecoration(
                      color: _barColor,
                      borderRadius: BorderRadius.circular(height / 2),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}
