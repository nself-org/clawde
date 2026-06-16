import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'doctor_panel.dart';

/// Health status chip shown in the project selector header (D64.T23).
///
/// Displays 🟢/🟡/🔴 based on the doctor scan score:
///   - 🟢 green  : score ≥ 90 (or no critical/high findings)
///   - 🟡 yellow : score 70–89
///   - 🔴 red    : score < 70 or any critical finding
///
/// Tapping triggers a scan (if not yet run) then opens [DoctorPanel].
class DoctorBadge extends ConsumerWidget {
  const DoctorBadge({
    super.key,
    required this.projectPath,
  });

  final String projectPath;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scanState = ref.watch(doctorProvider(projectPath));

    return scanState.when(
      loading: () => const _HealthChip(
        label: '…',
        color: Colors.white38,
        icon: Icons.health_and_safety_outlined,
      ),
      error: (_, __) => const _HealthChip(
        label: 'err',
        color: Colors.red,
        icon: Icons.error_outline,
      ),
      data: (result) {
        if (result == null) {
          // Not yet scanned — show a neutral chip that triggers a scan on tap.
          return _HealthChip(
            label: 'scan',
            color: Colors.white38,
            icon: Icons.health_and_safety_outlined,
            onTap: () => _runScan(ref),
          );
        }
        final (color, icon) = _resolveStyle(result);
        return _HealthChip(
          label: '${result.score}',
          color: color,
          icon: icon,
          onTap: () => _openPanel(context, ref),
        );
      },
    );
  }

  (Color, IconData) _resolveStyle(DoctorScanResult result) {
    final hasCritical = result.findings
        .any((f) => f.severity == DoctorSeverity.critical);
    if (hasCritical || result.score < 70) {
      return (Colors.red, Icons.health_and_safety);
    }
    if (result.score < 90) {
      return (Colors.orange, Icons.health_and_safety_outlined);
    }
    return (Colors.green, Icons.health_and_safety);
  }

  Future<void> _runScan(WidgetRef ref) async {
    await ref.read(doctorProvider(projectPath).notifier).scan();
  }

  void _openPanel(BuildContext context, WidgetRef ref) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (_) => DoctorPanel(projectPath: projectPath),
    );
  }
}

class _HealthChip extends StatelessWidget {
  const _HealthChip({
    required this.label,
    required this.color,
    required this.icon,
    this.onTap,
  });

  final String label;
  final Color color;
  final IconData icon;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: 'Project health score',
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
          decoration: BoxDecoration(
            color: color.withValues(alpha: 0.12),
            borderRadius: BorderRadius.circular(10),
            border: Border.all(color: color.withValues(alpha: 0.35)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 11, color: color),
              const SizedBox(width: 4),
              Text(
                label,
                style: TextStyle(
                  fontSize: 10,
                  color: color,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
