import 'package:flutter/material.dart';
import '../theme/clawd_theme.dart';

/// Expandable token-usage panel shown in the chat footer (MI.T14).
///
/// Displays input/output tokens, estimated cost, and context fill %.
/// Auto-expands when [budgetWarning] is true.
///
/// Data is supplied as plain values — the caller owns the RPC calls.
class TokenUsagePanel extends StatefulWidget {
  const TokenUsagePanel({
    super.key,
    this.inputTokens = 0,
    this.outputTokens = 0,
    this.estimatedCostUsd = 0.0,
    this.contextPercent = 0.0,
    this.monthlySpendUsd = 0.0,
    this.monthlyCap,
    this.budgetWarning = false,
    this.budgetExceeded = false,
  });

  final int inputTokens;
  final int outputTokens;
  final double estimatedCostUsd;

  /// Context window fill, 0.0–100.0.
  final double contextPercent;

  final double monthlySpendUsd;

  /// Monthly cap in USD, or null when no cap is configured.
  final double? monthlyCap;

  /// When true the panel auto-expands and shows a warning indicator.
  final bool budgetWarning;
  final bool budgetExceeded;

  @override
  State<TokenUsagePanel> createState() => _TokenUsagePanelState();
}

class _TokenUsagePanelState extends State<TokenUsagePanel> {
  late bool _expanded;

  @override
  void initState() {
    super.initState();
    _expanded = widget.budgetWarning || widget.budgetExceeded;
  }

  @override
  void didUpdateWidget(TokenUsagePanel old) {
    super.didUpdateWidget(old);
    // Auto-expand on budget warning transition.
    if (!old.budgetWarning && widget.budgetWarning) _expanded = true;
    if (!old.budgetExceeded && widget.budgetExceeded) _expanded = true;
  }

  @override
  Widget build(BuildContext context) {
    final warnColor = widget.budgetExceeded
        ? ClawdTheme.error
        : widget.budgetWarning
            ? ClawdTheme.warning
            : Colors.white24;

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        // Collapsed header — always visible
        GestureDetector(
          onTap: () => setState(() => _expanded = !_expanded),
          behavior: HitTestBehavior.opaque,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
            decoration: BoxDecoration(
              color: ClawdTheme.surfaceElevated,
              border: Border(
                top: BorderSide(color: ClawdTheme.surfaceBorder),
                bottom: _expanded
                    ? BorderSide(color: ClawdTheme.surfaceBorder)
                    : BorderSide.none,
              ),
            ),
            child: Row(
              children: [
                Icon(
                  widget.budgetExceeded
                      ? Icons.money_off
                      : Icons.receipt_long,
                  size: 13,
                  color: warnColor,
                ),
                const SizedBox(width: 6),
                Text(
                  '\$${widget.estimatedCostUsd.toStringAsFixed(4)}',
                  style: TextStyle(
                    fontSize: 11,
                    color: warnColor,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(width: 12),
                _StatLabel(
                  label: 'in',
                  value: _formatTokens(widget.inputTokens),
                ),
                const SizedBox(width: 8),
                _StatLabel(
                  label: 'out',
                  value: _formatTokens(widget.outputTokens),
                ),
                const SizedBox(width: 12),
                _ContextBar(percent: widget.contextPercent),
                const Spacer(),
                if (widget.budgetWarning && !widget.budgetExceeded)
                  const Padding(
                    padding: EdgeInsets.only(right: 6),
                    child: Text(
                      'Budget warning',
                      style: TextStyle(
                        fontSize: 10,
                        color: ClawdTheme.warning,
                      ),
                    ),
                  ),
                if (widget.budgetExceeded)
                  const Padding(
                    padding: EdgeInsets.only(right: 6),
                    child: Text(
                      'Budget exceeded',
                      style: TextStyle(
                        fontSize: 10,
                        color: ClawdTheme.error,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                Icon(
                  _expanded ? Icons.expand_more : Icons.chevron_right,
                  size: 14,
                  color: Colors.white38,
                ),
              ],
            ),
          ),
        ),
        // Expanded detail view
        if (_expanded)
          Container(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
            color: ClawdTheme.surfaceElevated,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _DetailRow('Input tokens', _formatTokens(widget.inputTokens)),
                _DetailRow('Output tokens', _formatTokens(widget.outputTokens)),
                _DetailRow(
                    'Session cost',
                    '\$${widget.estimatedCostUsd.toStringAsFixed(6)}'),
                _DetailRow(
                    'Context fill',
                    '${widget.contextPercent.toStringAsFixed(1)}%'),
                if (widget.monthlyCap != null) ...[
                  const Divider(height: 16),
                  _DetailRow(
                    'Monthly spend',
                    '\$${widget.monthlySpendUsd.toStringAsFixed(4)}',
                  ),
                  _DetailRow(
                    'Monthly cap',
                    '\$${widget.monthlyCap!.toStringAsFixed(2)}',
                  ),
                  _BudgetProgressBar(
                    spend: widget.monthlySpendUsd,
                    cap: widget.monthlyCap!,
                  ),
                ],
              ],
            ),
          ),
      ],
    );
  }

  static String _formatTokens(int n) {
    if (n >= 1000) {
      final k = n / 1000;
      return k < 100 ? '${k.toStringAsFixed(1)}k' : '${k.round()}k';
    }
    return n.toString();
  }
}

class _StatLabel extends StatelessWidget {
  const _StatLabel({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          '$label ',
          style: const TextStyle(fontSize: 10, color: Colors.white38),
        ),
        Text(
          value,
          style: const TextStyle(
            fontSize: 11,
            color: Colors.white60,
            fontWeight: FontWeight.w500,
          ),
        ),
      ],
    );
  }
}

/// Mini horizontal context fill bar (0–100%).
class _ContextBar extends StatelessWidget {
  const _ContextBar({required this.percent});
  final double percent;

  @override
  Widget build(BuildContext context) {
    final frac = (percent / 100).clamp(0.0, 1.0);
    final color = percent >= 80
        ? ClawdTheme.error
        : percent >= 60
            ? ClawdTheme.warning
            : ClawdTheme.success;
    return Tooltip(
      message: 'Context: ${percent.toStringAsFixed(1)}%',
      child: SizedBox(
        width: 60,
        height: 4,
        child: ClipRRect(
          borderRadius: BorderRadius.circular(2),
          child: Stack(
            children: [
              Container(color: Colors.white12),
              FractionallySizedBox(
                widthFactor: frac,
                child: Container(color: color),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _DetailRow extends StatelessWidget {
  const _DetailRow(this.label, this.value);
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          Text(
            label,
            style: const TextStyle(fontSize: 11, color: Colors.white38),
          ),
          const Spacer(),
          Text(
            value,
            style: const TextStyle(
              fontSize: 11,
              color: Colors.white70,
              fontWeight: FontWeight.w500,
            ),
          ),
        ],
      ),
    );
  }
}

class _BudgetProgressBar extends StatelessWidget {
  const _BudgetProgressBar({required this.spend, required this.cap});
  final double spend;
  final double cap;

  @override
  Widget build(BuildContext context) {
    final frac = cap > 0 ? (spend / cap).clamp(0.0, 1.0) : 0.0;
    final pct = frac * 100;
    final color = pct >= 100
        ? ClawdTheme.error
        : pct >= 80
            ? ClawdTheme.warning
            : ClawdTheme.success;
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(3),
        child: SizedBox(
          height: 6,
          child: Stack(
            children: [
              Container(color: Colors.white12),
              FractionallySizedBox(
                widthFactor: frac,
                child: Container(color: color),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
