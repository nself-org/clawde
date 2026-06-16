import 'dart:async' show unawaited;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';

/// Progress bar showing task completion % and ETA for a project (V02.T18).
///
/// Calls `tasks.progressEstimate` and displays:
///   - a linear progress bar filled to [pct_complete] %
///   - "N / total done" count
///   - ETA when available ("~Xm remaining")
///
/// Auto-refreshes every 30 seconds when mounted.
class TaskProgressBar extends ConsumerStatefulWidget {
  const TaskProgressBar({
    super.key,
    this.repoPath,
    this.compact = false,
  });

  /// Repo path to scope the estimate. Null = all projects.
  final String? repoPath;

  /// When true renders a single line. When false shows bar + stats row.
  final bool compact;

  @override
  ConsumerState<TaskProgressBar> createState() => _TaskProgressBarState();
}

class _TaskProgressBarState extends ConsumerState<TaskProgressBar> {
  _ProgressData? _data;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _fetch();
    // Poll every 30 s (intentionally fire-and-forget — widget lifecycle
    // terminates the loop via the `mounted` guard).
    unawaited(Future.doWhile(() async {
      await Future<void>.delayed(const Duration(seconds: 30));
      if (!mounted) return false;
      await _fetch();
      return mounted;
    }));
  }

  Future<void> _fetch() async {
    if (!mounted || _loading) return;
    setState(() => _loading = true);
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final estimateParams = widget.repoPath != null
          ? {'repo_path': widget.repoPath}
          : <String, dynamic>{};
      final raw = await client.call<Map<String, dynamic>>(
        'tasks.progressEstimate',
        estimateParams,
      );
      if (mounted) {
        setState(() {
          _data = _ProgressData.fromJson(raw);
          _loading = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_data == null) {
      return widget.compact
          ? const SizedBox.shrink()
          : const LinearProgressIndicator(value: 0);
    }
    final d = _data!;
    if (d.total == 0) return const SizedBox.shrink();

    final pct = (d.pctComplete / 100).clamp(0.0, 1.0);

    if (widget.compact) {
      return _CompactBar(pct: pct, data: d);
    }

    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(2),
          child: LinearProgressIndicator(
            value: pct,
            backgroundColor: Colors.white12,
            valueColor:
                AlwaysStoppedAnimation<Color>(_barColor(d.pctComplete)),
            minHeight: 4,
          ),
        ),
        const SizedBox(height: 4),
        Row(
          children: [
            Text(
              '${d.done} / ${d.total} done',
              style: const TextStyle(fontSize: 11, color: Colors.white54),
            ),
            const Spacer(),
            if (d.etaMinutes != null)
              Text(
                '~${_formatEta(d.etaMinutes!)} remaining',
                style: const TextStyle(fontSize: 11, color: Colors.white38),
              ),
          ],
        ),
      ],
    );
  }

  Color _barColor(double pct) {
    if (pct >= 90) return Colors.green;
    if (pct >= 60) return Colors.orange;
    return Colors.red;
  }

  String _formatEta(double minutes) {
    if (minutes < 60) return '${minutes.round()}m';
    final h = (minutes / 60).floor();
    final m = (minutes % 60).round();
    return m > 0 ? '${h}h ${m}m' : '${h}h';
  }
}

class _CompactBar extends StatelessWidget {
  const _CompactBar({required this.pct, required this.data});
  final double pct;
  final _ProgressData data;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        SizedBox(
          width: 60,
          child: ClipRRect(
            borderRadius: BorderRadius.circular(2),
            child: LinearProgressIndicator(
              value: pct,
              backgroundColor: Colors.white12,
              valueColor: const AlwaysStoppedAnimation<Color>(Colors.green),
              minHeight: 3,
            ),
          ),
        ),
        const SizedBox(width: 6),
        Text(
          '${data.pctComplete.round()}%',
          style: const TextStyle(fontSize: 10, color: Colors.white38),
        ),
      ],
    );
  }
}

// ─── Data model ───────────────────────────────────────────────────────────────

class _ProgressData {
  final int done;
  final int total;
  final double pctComplete;
  final double? etaMinutes;

  const _ProgressData({
    required this.done,
    required this.total,
    required this.pctComplete,
    this.etaMinutes,
  });

  factory _ProgressData.fromJson(Map<String, dynamic> json) => _ProgressData(
        done: (json['done'] as num?)?.toInt() ?? 0,
        total: (json['total'] as num?)?.toInt() ?? 0,
        pctComplete: (json['pct_complete'] as num?)?.toDouble() ?? 0.0,
        etaMinutes: (json['eta_minutes'] as num?)?.toDouble(),
      );
}
