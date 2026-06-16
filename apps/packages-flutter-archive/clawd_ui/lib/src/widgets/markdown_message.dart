import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:highlight/highlight.dart' show highlight, Node;
import 'package:markdown/markdown.dart' as md;
import '../theme/clawd_theme.dart';

/// Renders markdown content — used inside [ChatBubble] for assistant messages.
class MarkdownMessage extends StatelessWidget {
  const MarkdownMessage({super.key, required this.content});

  final String content;

  @override
  Widget build(BuildContext context) {
    return MarkdownBody(
      data: content,
      selectable: true,
      extensionSet: md.ExtensionSet.gitHubFlavored,
      styleSheet: MarkdownStyleSheet(
        p: const TextStyle(fontSize: 14, height: 1.5, color: Colors.white),
        strong: const TextStyle(
          fontSize: 14,
          height: 1.5,
          color: Colors.white,
          fontWeight: FontWeight.bold,
        ),
        em: const TextStyle(
          fontSize: 14,
          height: 1.5,
          color: Colors.white,
          fontStyle: FontStyle.italic,
        ),
        code: TextStyle(
          fontFamily: 'monospace',
          fontSize: 13,
          color: ClawdTheme.clawLight,
          backgroundColor: ClawdTheme.surface,
        ),
        codeblockDecoration: BoxDecoration(
          color: ClawdTheme.surface,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: ClawdTheme.surfaceBorder),
        ),
        blockquoteDecoration: BoxDecoration(
          color: ClawdTheme.surfaceElevated,
          border: Border(
            left: BorderSide(color: ClawdTheme.clawLight, width: 3),
          ),
        ),
        h1: const TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.bold,
          color: Colors.white,
        ),
        h2: const TextStyle(
          fontSize: 18,
          fontWeight: FontWeight.bold,
          color: Colors.white,
        ),
        h3: const TextStyle(
          fontSize: 16,
          fontWeight: FontWeight.bold,
          color: Colors.white,
        ),
        listBullet: const TextStyle(fontSize: 14, color: Colors.white),
        tableBody: const TextStyle(fontSize: 13, color: Colors.white),
        tableHead: const TextStyle(
          fontSize: 13,
          fontWeight: FontWeight.bold,
          color: Colors.white,
        ),
      ),
      builders: {'code': _SyntaxHighlightBuilder()},
    );
  }
}

// ── V02.T15 — isolate highlight cache ────────────────────────────────────────

/// Number of newlines above which we defer highlight to a future (isolate).
const _kIsolateThreshold = 50;

/// Flat token — sendable across isolates.
typedef _Token = ({String text, String? cls});

/// In-memory cache: code-hash → computed TextSpan. Avoids re-highlighting.
final _spanCache = <int, TextSpan>{};

/// Compute highlighted tokens from [code]+[language] in a separate isolate.
/// Returns a flat list of tokens suitable for reconstructing TextSpans.
List<_Token> _highlightInIsolate(List<String> args) {
  final code = args[0];
  final language = args[1];
  final result = highlight.parse(code, language: language, autoDetection: false);
  if (result.nodes == null) return [_mkToken(code, null)];
  final tokens = <_Token>[];
  _walkNodes(result.nodes!, tokens);
  return tokens;
}

void _walkNodes(List<Node> nodes, List<_Token> out) {
  for (final node in nodes) {
    if (node.value != null) {
      out.add(_mkToken(node.value!, node.className));
    } else if (node.children != null) {
      // Push className context to children
      final childOut = <_Token>[];
      _walkNodes(node.children!, childOut);
      if (node.className != null) {
        for (final t in childOut) {
          out.add(_mkToken(t.text, t.cls ?? node.className));
        }
      } else {
        out.addAll(childOut);
      }
    }
  }
}

_Token _mkToken(String text, String? cls) => (text: text, cls: cls);

TextSpan _tokensToSpan(List<_Token> tokens) {
  final children = <TextSpan>[];
  for (final t in tokens) {
    final style = t.cls != null
        ? TextStyle(
            color: atomOneDarkTheme['hljs-${t.cls}']?.color ??
                atomOneDarkTheme['hljs']?.color ??
                Colors.white,
          )
        : const TextStyle(color: Colors.white);
    children.add(TextSpan(text: t.text, style: style));
  }
  return TextSpan(
    style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
    children: children,
  );
}

// ── Markdown builder ──────────────────────────────────────────────────────────

class _SyntaxHighlightBuilder extends MarkdownElementBuilder {
  @override
  Widget? visitElementAfterWithContext(
    BuildContext context,
    md.Element element,
    TextStyle? preferredStyle,
    TextStyle? parentStyle,
  ) {
    final String code = element.textContent;
    final String? langClass = element.attributes['class'];
    final String language = langClass != null && langClass.startsWith('language-')
        ? langClass.substring('language-'.length)
        : 'plaintext';

    final lineCount = '\n'.allMatches(code).length + 1;

    // Small blocks: highlight synchronously on main thread.
    if (lineCount < _kIsolateThreshold) {
      return _wrapBlock(
        HighlightView(
          code.trimRight(),
          language: language,
          theme: atomOneDarkTheme,
          padding: const EdgeInsets.all(12),
          textStyle: const TextStyle(fontFamily: 'monospace', fontSize: 13),
        ),
      );
    }

    // Large blocks (V02.T15): highlight in isolate, cache result.
    final cacheKey = Object.hashAll([code, language]);
    if (_spanCache.containsKey(cacheKey)) {
      return _wrapBlock(_HighlightedText(span: _spanCache[cacheKey]!));
    }

    return _wrapBlock(
      _IsolateHighlightView(
        code: code.trimRight(),
        language: language,
        cacheKey: cacheKey,
      ),
    );
  }

  static Widget _wrapBlock(Widget child) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: RepaintBoundary(child: child),
      );
}

/// Shows a placeholder while isolate highlighting runs, then swaps to the
/// highlighted result and caches it.
class _IsolateHighlightView extends StatefulWidget {
  const _IsolateHighlightView({
    required this.code,
    required this.language,
    required this.cacheKey,
  });
  final String code;
  final String language;
  final int cacheKey;

  @override
  State<_IsolateHighlightView> createState() => _IsolateHighlightViewState();
}

class _IsolateHighlightViewState extends State<_IsolateHighlightView> {
  TextSpan? _span;

  @override
  void initState() {
    super.initState();
    _compute();
  }

  Future<void> _compute() async {
    try {
      final tokens = await compute(
        _highlightInIsolate,
        [widget.code, widget.language],
      );
      final span = _tokensToSpan(tokens);
      _spanCache[widget.cacheKey] = span;
      if (mounted) setState(() => _span = span);
    } catch (_) {
      // On failure fall through to plain text display.
      if (mounted) setState(() => _span = TextSpan(text: widget.code));
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_span == null) {
      // While computing: plain monospace text placeholder.
      return Container(
        padding: const EdgeInsets.all(12),
        color: ClawdTheme.surface,
        child: Text(
          widget.code,
          style: const TextStyle(
            fontFamily: 'monospace',
            fontSize: 13,
            color: Colors.white70,
          ),
        ),
      );
    }
    return _HighlightedText(span: _span!);
  }
}

class _HighlightedText extends StatelessWidget {
  const _HighlightedText({required this.span});
  final TextSpan span;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: ClawdTheme.surface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: ClawdTheme.surfaceBorder),
      ),
      child: SelectableText.rich(span),
    );
  }
}
