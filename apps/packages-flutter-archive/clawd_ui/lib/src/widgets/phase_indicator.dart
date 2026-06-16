import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Animated phase indicator chip displayed in the chat session header.
///
/// Shows the current active phase (e.g. "Phase 42") with a pulsing dot
/// when the session is actively running, and a static indicator otherwise.
class PhaseIndicator extends StatefulWidget {
  const PhaseIndicator({
    super.key,
    required this.phase,
    this.isActive = false,
    this.onTap,
  });

  /// The phase label to display (e.g. "Phase 42" or "42-core").
  final String phase;

  /// Whether the session is currently running (triggers pulse animation).
  final bool isActive;

  /// Optional tap callback (e.g. to navigate to phase detail).
  final VoidCallback? onTap;

  @override
  State<PhaseIndicator> createState() => _PhaseIndicatorState();
}

class _PhaseIndicatorState extends State<PhaseIndicator>
    with SingleTickerProviderStateMixin {
  late AnimationController _controller;
  late Animation<double> _pulse;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1200),
    );
    _pulse = Tween<double>(begin: 0.4, end: 1.0).animate(
      CurvedAnimation(parent: _controller, curve: Curves.easeInOut),
    );
    if (widget.isActive) {
      _controller.repeat(reverse: true);
    }
  }

  @override
  void didUpdateWidget(PhaseIndicator oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.isActive != oldWidget.isActive) {
      if (widget.isActive) {
        _controller.repeat(reverse: true);
      } else {
        _controller.stop();
        _controller.value = 1.0;
      }
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: widget.onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        decoration: BoxDecoration(
          color: ClawdTheme.claw.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: ClawdTheme.claw.withValues(alpha: 0.3),
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (widget.isActive)
              FadeTransition(
                opacity: _pulse,
                child: Container(
                  width: 6,
                  height: 6,
                  decoration: const BoxDecoration(
                    color: ClawdTheme.claw,
                    shape: BoxShape.circle,
                  ),
                ),
              )
            else
              Container(
                width: 6,
                height: 6,
                decoration: BoxDecoration(
                  color: ClawdTheme.claw.withValues(alpha: 0.5),
                  shape: BoxShape.circle,
                ),
              ),
            const SizedBox(width: 6),
            Text(
              widget.phase,
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: ClawdTheme.claw.withValues(alpha: 0.9),
                letterSpacing: 0.3,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
