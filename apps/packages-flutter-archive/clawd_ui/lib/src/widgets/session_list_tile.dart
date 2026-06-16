import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';
import '../theme/clawd_theme.dart';
import 'model_chip.dart';
import 'provider_badge.dart';

/// A list tile representing a single [Session].
/// Tapping calls [onTap]. Provides visual distinction for active/running sessions.
class SessionListTile extends StatelessWidget {
  const SessionListTile({
    super.key,
    required this.session,
    this.isSelected = false,
    this.onTap,
  });

  final Session session;
  final bool isSelected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: ListTile(
        selected: isSelected,
        selectedTileColor: ClawdTheme.claw.withValues(alpha: 0.12),
        onTap: onTap,
        leading: _DoubleDot(status: session.status, tier: session.tier),
        title: Text(
          session.repoPath.split('/').last,
          style: const TextStyle(fontWeight: FontWeight.w500),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        subtitle: Text(
          session.repoPath,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(fontSize: 11),
        ),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            // MI.T13 — model indicator chip (hidden when auto-routing)
            if (session.modelOverride != null) ...[
              ModelIndicator(modelOverride: session.modelOverride),
              const SizedBox(width: 4),
            ],
            ProviderBadge(provider: session.provider),
          ],
        ),
      ),
    );
  }
}

/// V02.T09 — two dots side by side: status dot (left) + tier dot (right).
///
/// Status dot: running=green glow, paused=blue, error=red, idle=amber, done=grey
/// Tier dot: active=green, warm=yellow, cold=grey (no glow on tier dot)
class _DoubleDot extends StatelessWidget {
  const _DoubleDot({required this.status, required this.tier});
  final SessionStatus status;
  final SessionTier tier;

  Color get _statusColor => switch (status) {
        SessionStatus.running => ClawdTheme.success,
        SessionStatus.idle => ClawdTheme.warning,
        SessionStatus.paused => ClawdTheme.info,
        SessionStatus.error => ClawdTheme.error,
        _ => Colors.grey,
      };

  Color get _tierColor => switch (tier) {
        SessionTier.active => ClawdTheme.success,
        SessionTier.warm => ClawdTheme.warning,
        SessionTier.cold => Colors.white24,
      };

  String get _tierTooltip => switch (tier) {
        SessionTier.active => 'Active — runner loaded',
        SessionTier.warm => 'Warm — runner cached',
        SessionTier.cold => 'Cold — needs warm-up',
      };

  @override
  Widget build(BuildContext context) {
    final sc = _statusColor;
    return Tooltip(
      message: _tierTooltip,
      child: SizedBox(
        width: 20,
        height: 20,
        child: Stack(
          children: [
            // Status dot (top-left)
            Positioned(
              top: 0,
              left: 0,
              child: _Dot(
                color: sc,
                glow: status == SessionStatus.running,
              ),
            ),
            // Tier dot (bottom-right) — V02.T09
            Positioned(
              bottom: 0,
              right: 0,
              child: _Dot(color: _tierColor, size: 5),
            ),
          ],
        ),
      ),
    );
  }
}

class _Dot extends StatelessWidget {
  const _Dot({required this.color, this.size = 8, this.glow = false});
  final Color color;
  final double size;
  final bool glow;

  @override
  Widget build(BuildContext context) {
    return AnimatedContainer(
      duration: const Duration(milliseconds: 300),
      width: size,
      height: size,
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        color: color,
        boxShadow: glow
            ? [BoxShadow(color: color.withValues(alpha: 0.5), blurRadius: 4)]
            : null,
      ),
    );
  }
}
