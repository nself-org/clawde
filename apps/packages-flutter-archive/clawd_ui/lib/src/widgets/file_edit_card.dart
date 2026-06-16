import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Displays a file edit operation as a card with optional unified-diff preview.
///
/// Used in the chat view to represent tool calls that modify files (write_file,
/// edit_file, patch, etc.). Shows the filename, change summary, and optionally
/// a collapsible diff.
class FileEditCard extends StatefulWidget {
  const FileEditCard({
    super.key,
    required this.filePath,
    this.operation = 'edit',
    this.linesAdded = 0,
    this.linesRemoved = 0,
    this.diffContent,
    this.isExpanded = false,
    this.onOpenFullDiff,
  });

  /// The file path being edited (relative to repo root).
  final String filePath;

  /// The type of operation: 'create', 'edit', 'delete', 'rename'.
  final String operation;

  /// Number of lines added (shown in green).
  final int linesAdded;

  /// Number of lines removed (shown in red).
  final int linesRemoved;

  /// Raw unified diff content to show when expanded. If null or empty, diff
  /// preview is hidden and a placeholder is shown instead.
  final String? diffContent;

  /// Whether the diff is expanded by default.
  final bool isExpanded;

  /// Optional callback invoked when the user taps "Open Full Diff".
  /// If null, the button is not shown. Typically provided by the desktop app
  /// to open [DiffReviewDialog].
  final VoidCallback? onOpenFullDiff;

  @override
  State<FileEditCard> createState() => _FileEditCardState();
}

class _FileEditCardState extends State<FileEditCard> {
  late bool _expanded;

  @override
  void initState() {
    super.initState();
    _expanded = widget.isExpanded;
  }

  Color get _operationColor {
    return switch (widget.operation) {
      'create' => ClawdTheme.success,
      'delete' => ClawdTheme.error,
      'rename' => ClawdTheme.info,
      _ => ClawdTheme.warning,
    };
  }

  IconData get _operationIcon {
    return switch (widget.operation) {
      'create' => Icons.add_circle_outline,
      'delete' => Icons.delete_outline,
      'rename' => Icons.drive_file_rename_outline,
      _ => Icons.edit_outlined,
    };
  }

  String get _filename {
    final parts = widget.filePath.replaceAll('\\', '/').split('/');
    return parts.last;
  }

  String get _directory {
    final parts = widget.filePath.replaceAll('\\', '/').split('/');
    if (parts.length <= 1) return '';
    return parts.sublist(0, parts.length - 1).join('/');
  }

  // M16: A diff is considered "present" only when the content is non-null
  // and non-empty. Empty diff strings produce a misleading blank preview.
  bool get _hasDiff =>
      widget.diffContent != null && widget.diffContent!.isNotEmpty;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 3),
      decoration: BoxDecoration(
        color: ClawdTheme.surfaceElevated,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: ClawdTheme.surfaceBorder),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // ── Header row ────────────────────────────────────────────────────
          InkWell(
            onTap: _hasDiff
                ? () => setState(() => _expanded = !_expanded)
                : null,
            borderRadius: BorderRadius.circular(8),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
              child: Row(
                children: [
                  Icon(_operationIcon, size: 14, color: _operationColor),
                  const SizedBox(width: 8),
                  Expanded(
                    child: RichText(
                      overflow: TextOverflow.ellipsis,
                      text: TextSpan(
                        children: [
                          if (_directory.isNotEmpty)
                            TextSpan(
                              text: '$_directory/',
                              style: const TextStyle(
                                fontSize: 12,
                                color: Colors.white38,
                              ),
                            ),
                          TextSpan(
                            text: _filename,
                            style: const TextStyle(
                              fontSize: 12,
                              fontWeight: FontWeight.w600,
                              color: Colors.white70,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  // Diff stats
                  if (widget.linesAdded > 0)
                    Text(
                      '+${widget.linesAdded}',
                      style: const TextStyle(
                        fontSize: 11,
                        color: ClawdTheme.success,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  if (widget.linesAdded > 0 && widget.linesRemoved > 0)
                    const SizedBox(width: 4),
                  if (widget.linesRemoved > 0)
                    Text(
                      '-${widget.linesRemoved}',
                      style: const TextStyle(
                        fontSize: 11,
                        color: ClawdTheme.error,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  if (widget.onOpenFullDiff != null && _hasDiff) ...[
                    const SizedBox(width: 4),
                    InkWell(
                      onTap: widget.onOpenFullDiff,
                      borderRadius: BorderRadius.circular(4),
                      child: const Padding(
                        padding: EdgeInsets.all(2),
                        child: Tooltip(
                          message: 'Open Full Diff (per-hunk review)',
                          child: Icon(
                            Icons.open_in_full,
                            size: 13,
                            color: Colors.white38,
                          ),
                        ),
                      ),
                    ),
                  ],
                  if (_hasDiff) ...[
                    const SizedBox(width: 4),
                    Icon(
                      _expanded
                          ? Icons.expand_less
                          : Icons.expand_more,
                      size: 16,
                      color: Colors.white38,
                    ),
                  ],
                ],
              ),
            ),
          ),
          // ── Diff preview ──────────────────────────────────────────────────
          if (_expanded && _hasDiff)
            Container(
              decoration: const BoxDecoration(
                border: Border(
                  top: BorderSide(color: ClawdTheme.surfaceBorder),
                ),
              ),
              child: _DiffView(diff: widget.diffContent!),
            ),
        ],
      ),
    );
  }
}

// ─── Diff view ────────────────────────────────────────────────────────────────

class _DiffView extends StatelessWidget {
  const _DiffView({required this.diff});
  final String diff;

  @override
  Widget build(BuildContext context) {
    // M16: Guard against empty diff content — show a placeholder rather than
    // rendering a blank area. Callers should use _hasDiff before constructing
    // this widget, but this provides a safe fallback.
    if (diff.isEmpty) {
      return const Padding(
        padding: EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Text(
          'No diff content available.',
          style: TextStyle(fontSize: 11, color: Colors.white38),
        ),
      );
    }

    final lines = diff.split('\n');
    return Container(
      constraints: const BoxConstraints(maxHeight: 300),
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: lines.map((line) => _DiffLine(line: line)).toList(),
        ),
      ),
    );
  }
}

class _DiffLine extends StatelessWidget {
  const _DiffLine({required this.line});
  final String line;

  Color get _bgColor {
    if (line.startsWith('+') && !line.startsWith('+++')) {
      return ClawdTheme.success.withValues(alpha: 0.08);
    }
    if (line.startsWith('-') && !line.startsWith('---')) {
      return ClawdTheme.error.withValues(alpha: 0.08);
    }
    if (line.startsWith('@@')) {
      return ClawdTheme.info.withValues(alpha: 0.06);
    }
    return Colors.transparent;
  }

  Color get _textColor {
    if (line.startsWith('+') && !line.startsWith('+++')) {
      return ClawdTheme.success;
    }
    if (line.startsWith('-') && !line.startsWith('---')) {
      return ClawdTheme.error;
    }
    if (line.startsWith('@@')) {
      return ClawdTheme.info;
    }
    return Colors.white54;
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      color: _bgColor,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 1),
      child: Text(
        line,
        style: TextStyle(
          fontSize: 11,
          fontFamily: 'monospace',
          color: _textColor,
          height: 1.6,
        ),
      ),
    );
  }
}
