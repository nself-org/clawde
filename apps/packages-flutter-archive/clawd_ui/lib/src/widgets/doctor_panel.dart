import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';
import 'package:clawd_proto/clawd_proto.dart';

import '../theme/clawd_theme.dart';

/// Full doctor findings panel shown in a bottom sheet (D64.T24).
///
/// Displays the list of findings grouped by severity, with per-finding
/// "Fix" buttons for fixable items and a top-level "Scan Again" button.
class DoctorPanel extends ConsumerWidget {
  const DoctorPanel({super.key, required this.projectPath});

  final String projectPath;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scanState = ref.watch(doctorProvider(projectPath));

    return Container(
      decoration: const BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: DraggableScrollableSheet(
        initialChildSize: 0.6,
        minChildSize: 0.35,
        maxChildSize: 0.92,
        expand: false,
        builder: (_, scrollController) => CustomScrollView(
          controller: scrollController,
          slivers: [
            SliverToBoxAdapter(child: _buildHeader(context, ref, scanState)),
            scanState.when(
              loading: () => const SliverFillRemaining(
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (e, _) => SliverFillRemaining(
                child: Center(
                  child: Text(
                    'Scan failed: $e',
                    style: const TextStyle(color: Colors.red, fontSize: 13),
                  ),
                ),
              ),
              data: (result) {
                if (result == null) {
                  return const SliverFillRemaining(
                    child: Center(
                      child: Text(
                        'Tap "Scan" to run doctor checks.',
                        style: TextStyle(color: Colors.white38, fontSize: 13),
                      ),
                    ),
                  );
                }
                return _buildFindings(context, ref, result);
              },
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildHeader(
    BuildContext context,
    WidgetRef ref,
    AsyncValue<DoctorScanResult?> state,
  ) {
    final score = state.valueOrNull?.score;
    final scoreColor = score == null
        ? Colors.white38
        : score >= 90
            ? Colors.green
            : score >= 70
                ? Colors.orange
                : Colors.red;

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        const SizedBox(height: 10),
        Container(
          width: 36,
          height: 4,
          decoration: BoxDecoration(
            color: Colors.white24,
            borderRadius: BorderRadius.circular(2),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 16, 12, 8),
          child: Row(
            children: [
              Icon(Icons.health_and_safety, size: 18, color: scoreColor),
              const SizedBox(width: 8),
              const Expanded(
                child: Text(
                  'Project Health',
                  style: TextStyle(
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                    color: Colors.white,
                  ),
                ),
              ),
              if (score != null)
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
                  decoration: BoxDecoration(
                    color: scoreColor.withValues(alpha: 0.15),
                    borderRadius: BorderRadius.circular(12),
                    border:
                        Border.all(color: scoreColor.withValues(alpha: 0.4)),
                  ),
                  child: Text(
                    '$score / 100',
                    style: TextStyle(
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                      color: scoreColor,
                    ),
                  ),
                ),
              const SizedBox(width: 8),
              TextButton.icon(
                onPressed: state.isLoading
                    ? null
                    : () => ref
                        .read(doctorProvider(projectPath).notifier)
                        .scan(),
                icon: const Icon(Icons.refresh, size: 14),
                label: const Text('Scan'),
                style: TextButton.styleFrom(
                  foregroundColor: Colors.white70,
                  padding:
                      const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                ),
              ),
            ],
          ),
        ),
        const Divider(height: 1),
      ],
    );
  }

  SliverList _buildFindings(
    BuildContext context,
    WidgetRef ref,
    DoctorScanResult result,
  ) {
    final fixable =
        result.findings.where((f) => f.fixable).map((f) => f.code).toList();

    if (result.findings.isEmpty) {
      return SliverList(
        delegate: SliverChildListDelegate([
          const Padding(
            padding: EdgeInsets.all(32),
            child: Column(
              children: [
                Icon(Icons.check_circle_outline,
                    size: 40, color: Colors.green),
                SizedBox(height: 12),
                Text(
                  'All checks passed.',
                  style: TextStyle(color: Colors.white70, fontSize: 14),
                ),
              ],
            ),
          ),
        ]),
      );
    }

    final items = <Widget>[
      if (fixable.isNotEmpty)
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 12, 20, 4),
          child: FilledButton.icon(
            onPressed: () =>
                ref.read(doctorProvider(projectPath).notifier).fix(),
            icon: const Icon(Icons.auto_fix_high, size: 16),
            label: Text('Fix all (${fixable.length})'),
            style: FilledButton.styleFrom(
              backgroundColor: Colors.green.withValues(alpha: 0.2),
              foregroundColor: Colors.green,
            ),
          ),
        ),
      const Divider(height: 1),
      ...result.findings.map((f) => _FindingTile(
            finding: f,
            onFix: f.fixable
                ? () => ref
                    .read(doctorProvider(projectPath).notifier)
                    .fix(codes: [f.code])
                : null,
          )),
      const SizedBox(height: 32),
    ];

    return SliverList(
      delegate: SliverChildListDelegate(items),
    );
  }
}

// ─── Finding tile ──────────────────────────────────────────────────────────────

class _FindingTile extends StatelessWidget {
  const _FindingTile({required this.finding, this.onFix});

  final DoctorFinding finding;
  final VoidCallback? onFix;

  @override
  Widget build(BuildContext context) {
    final (color, icon) = _severityStyle(finding.severity);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Icon(icon, size: 14, color: color),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  finding.message,
                  style: const TextStyle(fontSize: 13, color: Colors.white70),
                ),
                const SizedBox(height: 2),
                Row(
                  children: [
                    _SeverityChip(finding.severity),
                    const SizedBox(width: 6),
                    Text(
                      finding.code,
                      style: const TextStyle(
                          fontSize: 10,
                          color: Colors.white38,
                          fontFamily: 'monospace'),
                    ),
                  ],
                ),
                if (finding.path != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Text(
                      finding.path!,
                      style: const TextStyle(
                          fontSize: 10,
                          color: Colors.white30,
                          fontFamily: 'monospace'),
                    ),
                  ),
              ],
            ),
          ),
          if (onFix != null) ...[
            const SizedBox(width: 8),
            TextButton(
              onPressed: onFix,
              style: TextButton.styleFrom(
                foregroundColor: Colors.green,
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              child: const Text('Fix', style: TextStyle(fontSize: 11)),
            ),
          ],
        ],
      ),
    );
  }

  (Color, IconData) _severityStyle(DoctorSeverity severity) => switch (severity) {
        DoctorSeverity.critical => (Colors.red, Icons.error),
        DoctorSeverity.high => (Colors.deepOrange, Icons.warning_rounded),
        DoctorSeverity.medium => (Colors.orange, Icons.warning_amber_rounded),
        DoctorSeverity.low => (Colors.yellow, Icons.info_outline),
        DoctorSeverity.info => (Colors.white38, Icons.info_outline),
      };
}

class _SeverityChip extends StatelessWidget {
  const _SeverityChip(this.severity);
  final DoctorSeverity severity;

  @override
  Widget build(BuildContext context) {
    final (color, label) = switch (severity) {
      DoctorSeverity.critical => (Colors.red, 'critical'),
      DoctorSeverity.high => (Colors.deepOrange, 'high'),
      DoctorSeverity.medium => (Colors.orange, 'medium'),
      DoctorSeverity.low => (Colors.yellow, 'low'),
      DoctorSeverity.info => (Colors.white38, 'info'),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.3)),
      ),
      child: Text(
        label,
        style: TextStyle(fontSize: 9, color: color, fontWeight: FontWeight.w600),
      ),
    );
  }
}
