import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Centered error state widget — icon + title + optional description + optional retry.
class ErrorState extends StatelessWidget {
  const ErrorState({
    super.key,
    required this.icon,
    required this.title,
    this.description,
    this.onRetry,
  });

  final IconData icon;
  final String title;
  final String? description;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 48, color: ClawdTheme.error),
            const SizedBox(height: 16),
            Text(
              title,
              style: const TextStyle(
                fontSize: 16,
                fontWeight: FontWeight.w600,
                color: Colors.white,
              ),
              textAlign: TextAlign.center,
            ),
            if (description != null) ...[
              const SizedBox(height: 8),
              Text(
                description!,
                style: TextStyle(
                  fontSize: 13,
                  color: Colors.white.withValues(alpha: 0.6),
                ),
                textAlign: TextAlign.center,
              ),
            ],
            if (onRetry != null) ...[
              const SizedBox(height: 20),
              OutlinedButton.icon(
                onPressed: onRetry,
                icon: const Icon(Icons.refresh, size: 16),
                label: const Text('Retry'),
                style: OutlinedButton.styleFrom(
                  foregroundColor: ClawdTheme.clawLight,
                  side: const BorderSide(color: ClawdTheme.clawLight),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
