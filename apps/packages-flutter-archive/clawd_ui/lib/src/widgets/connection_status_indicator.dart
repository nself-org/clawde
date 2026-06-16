import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';
import '../theme/clawd_theme.dart';

/// A compact pill showing the daemon connection status.
/// - Connected: green dot
/// - Connecting/Reconnecting: amber dot + attempt count (SH-02)
/// - Error/Disconnected: red dot + tap to retry
class ConnectionStatusIndicator extends ConsumerWidget {
  const ConnectionStatusIndicator({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final daemon = ref.watch(daemonProvider);

    final String label;
    final Color color;
    final IconData icon;

    switch (daemon.status) {
      case DaemonStatus.connected:
        label = 'Connected';
        color = ClawdTheme.success;
        icon = Icons.circle;
      case DaemonStatus.connecting:
        final attempt = daemon.reconnectAttempt;
        label = attempt > 0 ? 'Retry #$attempt…' : 'Connecting…';
        color = ClawdTheme.warning;
        icon = Icons.sync;
      case DaemonStatus.error:
        label = 'Error – tap';
        color = ClawdTheme.error;
        icon = Icons.error_outline;
      case DaemonStatus.disconnected:
        label = 'Offline – tap';
        color = Colors.grey;
        icon = Icons.circle_outlined;
    }

    final canRetry = daemon.status == DaemonStatus.disconnected ||
        daemon.status == DaemonStatus.error;

    return GestureDetector(
      onTap: canRetry
          ? () => ref.read(daemonProvider.notifier).reconnect()
          : null,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: color.withValues(alpha: 0.4)),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 8, color: color),
            const SizedBox(width: 5),
            Text(
              label,
              style: TextStyle(
                fontSize: 11,
                color: color,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
