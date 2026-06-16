import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Small session-header or session-tile chip showing the pinned model.
///
/// Hidden when [modelOverride] is null (auto-routing active).
/// Tapping invokes [onTap] — used to open the model picker.
class ModelChip extends StatelessWidget {
  const ModelChip({
    super.key,
    required this.modelOverride,
    this.onTap,
  });

  /// The raw model ID string, e.g. `"claude-sonnet-4-6"`.
  /// Null hides the chip entirely.
  final String? modelOverride;

  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final model = modelOverride;
    if (model == null) return const SizedBox.shrink();

    final label = _shortLabel(model);

    return Tooltip(
      message: model,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
          decoration: BoxDecoration(
            color: const Color(0xFF5C3B00).withValues(alpha: 0.7),
            borderRadius: BorderRadius.circular(10),
            border: Border.all(color: Colors.amber.withValues(alpha: 0.5)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.auto_awesome, size: 10, color: Colors.amber),
              const SizedBox(width: 4),
              Text(
                label,
                style: const TextStyle(
                  fontSize: 10,
                  color: Colors.amber,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.3,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  /// Convert a full model ID to a compact label.
  ///
  /// Examples:
  ///   `claude-opus-4-6`          → `opus`
  ///   `claude-sonnet-4-6`        → `sonnet`
  ///   `claude-haiku-4-5-20251001`→ `haiku`
  ///   `gpt-4o`                   → `gpt-4o`
  static String _shortLabel(String modelId) {
    for (final name in ['opus', 'sonnet', 'haiku']) {
      if (modelId.toLowerCase().contains(name)) return name;
    }
    // Fallback: use model ID up to 10 chars.
    return modelId.length > 10 ? '${modelId.substring(0, 10)}…' : modelId;
  }
}

/// Stateless visual-only chip used in the session list tile trailing row.
/// No tap handler — the tile itself handles selection.
class ModelIndicator extends StatelessWidget {
  const ModelIndicator({super.key, required this.modelOverride});

  final String? modelOverride;

  @override
  Widget build(BuildContext context) {
    final model = modelOverride;
    if (model == null) return const SizedBox.shrink();
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
      decoration: BoxDecoration(
        color: Colors.amber.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: Colors.amber.withValues(alpha: 0.4)),
      ),
      child: Text(
        ModelChip._shortLabel(model),
        style: const TextStyle(
          fontSize: 9,
          color: Colors.amber,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

/// Full model selector bottom sheet.
///
/// Shows haiku / sonnet / opus + "Auto (route by task)" option.
/// [current] is the active model ID, or null for auto-routing.
/// [onSelect] is called with the new model ID, or null to restore auto-routing.
class ModelPicker extends StatelessWidget {
  const ModelPicker({
    super.key,
    required this.current,
    required this.onSelect,
  });

  final String? current;
  final void Function(String? model) onSelect;

  static const _models = [
    _ModelOption(
      id: 'claude-opus-4-6',
      label: 'Opus',
      subtitle: 'Most powerful — architecture, complex reasoning',
      icon: Icons.workspace_premium,
    ),
    _ModelOption(
      id: 'claude-sonnet-4-6',
      label: 'Sonnet',
      subtitle: 'Balanced — code, review, general tasks',
      icon: Icons.auto_awesome,
    ),
    _ModelOption(
      id: 'claude-haiku-4-5-20251001',
      label: 'Haiku',
      subtitle: 'Fast & cheap — quick lookups, simple edits',
      icon: Icons.bolt,
    ),
  ];

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
                Icon(Icons.auto_awesome, size: 16, color: Colors.amber),
                SizedBox(width: 8),
                Text(
                  'Select Model',
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
              'Pin a model for this session, or let the daemon route by task type.',
              style: TextStyle(fontSize: 12, color: Colors.white38),
            ),
          ),
          const Divider(height: 1),
          // Auto-routing option
          _OptionTile(
            icon: Icons.route,
            label: 'Auto (route by task)',
            subtitle: 'Daemon selects the best model per message',
            selected: current == null,
            onTap: () {
              Navigator.pop(context);
              onSelect(null);
            },
          ),
          const Divider(height: 1, indent: 20),
          for (final opt in _models) ...[
            _OptionTile(
              icon: opt.icon,
              label: opt.label,
              subtitle: opt.subtitle,
              selected: current == opt.id,
              onTap: () {
                Navigator.pop(context);
                onSelect(opt.id);
              },
            ),
            if (opt != _models.last)
              const Divider(height: 1, indent: 20),
          ],
          const SizedBox(height: 24),
        ],
      ),
    );
  }
}

class _ModelOption {
  const _ModelOption({
    required this.id,
    required this.label,
    required this.subtitle,
    required this.icon,
  });
  final String id;
  final String label;
  final String subtitle;
  final IconData icon;

  bool operator ==(Object other) =>
      other is _ModelOption && other.id == id;

  @override
  int get hashCode => id.hashCode;
}

class _OptionTile extends StatelessWidget {
  const _OptionTile({
    required this.icon,
    required this.label,
    required this.subtitle,
    required this.selected,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final String subtitle;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: Icon(
        icon,
        size: 18,
        color: selected ? Colors.amber : Colors.white54,
      ),
      title: Text(
        label,
        style: TextStyle(
          fontSize: 13,
          fontWeight: FontWeight.w600,
          color: selected ? Colors.amber : Colors.white,
        ),
      ),
      subtitle: Text(
        subtitle,
        style: const TextStyle(fontSize: 11, color: Colors.white38),
      ),
      trailing: selected
          ? const Icon(Icons.check, size: 16, color: Colors.amber)
          : null,
      onTap: onTap,
    );
  }
}
