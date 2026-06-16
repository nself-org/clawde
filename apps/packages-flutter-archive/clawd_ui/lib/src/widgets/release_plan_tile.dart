import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';
import 'package:clawd_proto/clawd_proto.dart';

import '../theme/clawd_theme.dart';

/// Settings panel item showing active release plans with approval status (D64.T25).
///
/// Calls `doctor.scan` with scope `"release"` to retrieve release findings,
/// then renders each plan with a status chip and "Approve" button.
///
/// Usage — add inside a settings ListView:
/// ```dart
/// ReleasePlanTile(projectPath: currentProject)
/// ```
class ReleasePlanTile extends ConsumerWidget {
  const ReleasePlanTile({super.key, required this.projectPath});

  final String projectPath;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scanState = ref.watch(doctorProvider(projectPath));
    final releaseFindings = scanState.valueOrNull?.findings
            .where((f) => f.code.startsWith('release.'))
            .toList() ??
        [];

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 8),
          child: Row(
            children: [
              const Text(
                'Release Plans',
                style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                  color: Colors.white70,
                  letterSpacing: 0.5,
                ),
              ),
              const Spacer(),
              if (scanState.isLoading)
                const SizedBox(
                  width: 12,
                  height: 12,
                  child: CircularProgressIndicator(strokeWidth: 1.5),
                )
              else
                InkWell(
                  onTap: () => ref
                      .read(doctorProvider(projectPath).notifier)
                      .scan(scope: 'release'),
                  borderRadius: BorderRadius.circular(4),
                  child: const Padding(
                    padding: EdgeInsets.all(4),
                    child: Icon(Icons.refresh, size: 14, color: Colors.white38),
                  ),
                ),
            ],
          ),
        ),
        if (scanState.hasError)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 8),
            child: Text(
              'Scan failed: ${scanState.error}',
              style: const TextStyle(fontSize: 12, color: Colors.red),
            ),
          )
        else if (releaseFindings.isEmpty && !scanState.isLoading)
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 20, vertical: 12),
            child: Text(
              'No release plan issues found.',
              style: TextStyle(fontSize: 13, color: Colors.white38),
            ),
          )
        else
          ...releaseFindings.map(
            (f) => _ReleasePlanItem(
              finding: f,
              projectPath: projectPath,
            ),
          ),
        const Divider(height: 1),
      ],
    );
  }
}

class _ReleasePlanItem extends ConsumerWidget {
  const _ReleasePlanItem({
    required this.finding,
    required this.projectPath,
  });

  final DoctorFinding finding;
  final String projectPath;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Extract version from finding path (e.g. ".claude/planning/release-v0.2.0.md")
    final version = _extractVersion(finding.path);
    final isApproved = finding.code == 'release.approved';

    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: ClawdTheme.surface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isApproved
              ? Colors.green.withValues(alpha: 0.3)
              : Colors.orange.withValues(alpha: 0.25),
        ),
      ),
      child: Row(
        children: [
          Icon(
            isApproved ? Icons.check_circle : Icons.pending_outlined,
            size: 16,
            color: isApproved ? Colors.green : Colors.orange,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  version ?? 'Release Plan',
                  style: const TextStyle(
                    fontSize: 13,
                    color: Colors.white70,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                Text(
                  finding.message,
                  style: const TextStyle(fontSize: 11, color: Colors.white38),
                ),
              ],
            ),
          ),
          if (!isApproved && version != null)
            TextButton(
              onPressed: () => ref
                  .read(doctorProvider(projectPath).notifier)
                  .approveRelease(version),
              style: TextButton.styleFrom(
                foregroundColor: Colors.green,
                padding:
                    const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              child: const Text('Approve', style: TextStyle(fontSize: 11)),
            ),
        ],
      ),
    );
  }

  /// Extract version string from a release plan file path.
  /// e.g. `.claude/planning/release-v0.2.0.md` → `v0.2.0`
  String? _extractVersion(String? path) {
    if (path == null) return null;
    final match = RegExp(r'release-([^/]+)\.md$').firstMatch(path);
    return match?.group(1);
  }
}
