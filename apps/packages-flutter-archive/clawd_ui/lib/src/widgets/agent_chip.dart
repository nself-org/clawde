import 'package:flutter/material.dart';

/// Small chip displaying an agent identifier.
/// Truncates long IDs to keep the UI compact.
class AgentChip extends StatelessWidget {
  const AgentChip({super.key, required this.agentId, this.isActive = false});

  final String agentId;
  final bool isActive;

  @override
  Widget build(BuildContext context) {
    final label = _shorten(agentId);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: isActive
            ? const Color(0xFF42A5F5).withValues(alpha: 0.15)
            : Colors.white.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isActive
              ? const Color(0xFF42A5F5).withValues(alpha: 0.5)
              : Colors.white.withValues(alpha: 0.15),
        ),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 6,
            height: 6,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: isActive
                  ? const Color(0xFF42A5F5)
                  : Colors.white.withValues(alpha: 0.3),
            ),
          ),
          const SizedBox(width: 4),
          Text(
            label,
            style: const TextStyle(fontSize: 11, color: Colors.white70),
          ),
        ],
      ),
    );
  }

  static String _shorten(String id) {
    // "agent:claude:sess-abc123" → "claude:abc123"
    final parts = id.split(':');
    if (parts.length >= 3) {
      final sessId = parts.last;
      final short = sessId.length > 8 ? sessId.substring(0, 8) : sessId;
      return '${parts[1]}:$short';
    }
    return id.length > 16 ? '${id.substring(0, 14)}\u2026' : id;
  }
}
