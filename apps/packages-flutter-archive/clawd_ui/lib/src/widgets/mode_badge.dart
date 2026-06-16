import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';
import '../theme/clawd_theme.dart';

/// Reusable mode badge for any session context.
///
/// Shows the current GCI mode with a colored pill. When [onTap] is provided,
/// the badge is tappable and a chevron is shown — used in the session header
/// as a mode switcher.
class ModeBadge extends StatelessWidget {
  const ModeBadge({
    super.key,
    required this.mode,
    this.onTap,
    this.compact = false,
  });

  final SessionMode mode;

  /// If non-null, the badge is tappable and opens a mode-picker.
  final VoidCallback? onTap;

  /// Compact mode: smaller font, no chevron even when [onTap] is set.
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final (label, color) = _modeInfo(mode);
    final fontSize = compact ? 9.0 : 10.0;
    final child = Container(
      padding: EdgeInsets.symmetric(
        horizontal: compact ? 5 : 7,
        vertical: compact ? 2 : 3,
      ),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: color.withValues(alpha: 0.45)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            label,
            style: TextStyle(
              fontSize: fontSize,
              fontWeight: FontWeight.w700,
              color: color,
              letterSpacing: 0.4,
            ),
          ),
          if (onTap != null && !compact) ...{
            const SizedBox(width: 3),
            Icon(Icons.keyboard_arrow_down, size: 12, color: color),
          },
        ],
      ),
    );
    if (onTap == null) return child;
    return GestureDetector(onTap: onTap, child: child);
  }

  static (String, Color) _modeInfo(SessionMode m) => switch (m) {
        SessionMode.normal => ('NORMAL', Colors.white38),
        SessionMode.learn => ('LEARN', const Color(0xFF42A5F5)),
        SessionMode.storm => ('STORM', const Color(0xFF7E57C2)),
        SessionMode.forge => ('FORGE', const Color(0xFF26A69A)),
        SessionMode.crunch => (
          'CRUNCH',
          ClawdTheme.claw,
        ),
      };
}

/// Full-page mode picker sheet — shown when user taps the ModeBadge.
class ModePicker extends StatelessWidget {
  const ModePicker({super.key, required this.current, required this.onSelect});

  final SessionMode current;
  final void Function(SessionMode) onSelect;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 16),
      decoration: const BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            'Session Mode',
            style: TextStyle(
              fontSize: 13,
              fontWeight: FontWeight.w700,
              color: Colors.white,
            ),
          ),
          const SizedBox(height: 12),
          for (final mode in SessionMode.values) _ModeOption(
            mode: mode,
            selected: mode == current,
            onTap: () {
              Navigator.of(context).pop();
              onSelect(mode);
            },
          ),
          const SizedBox(height: 8),
        ],
      ),
    );
  }
}

class _ModeOption extends StatelessWidget {
  const _ModeOption({
    required this.mode,
    required this.selected,
    required this.onTap,
  });
  final SessionMode mode;
  final bool selected;
  final VoidCallback onTap;

  static const _descriptions = {
    SessionMode.normal: 'Default — single requests, conversation',
    SessionMode.learn: 'Deep dialogue — questions, listening, requirements capture',
    SessionMode.storm: 'Free-form brainstorm — "yes and", all ideas captured',
    SessionMode.forge: 'Exhaustive planning — phase docs, version locking',
    SessionMode.crunch: 'Code execution — never stops, weight-class CR/QA',
  };

  @override
  Widget build(BuildContext context) {
    final (label, color) = ModeBadge._modeInfo(mode);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
        child: Row(
          children: [
            ModeBadge(mode: mode, compact: true),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                _descriptions[mode] ?? label,
                style: TextStyle(
                  fontSize: 12,
                  color: Colors.white.withValues(alpha: 0.7),
                ),
              ),
            ),
            if (selected)
              Icon(Icons.check, size: 14, color: color),
          ],
        ),
      ),
    );
  }
}
