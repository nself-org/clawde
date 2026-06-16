import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../theme/clawd_theme.dart';

/// A slash command definition.
class SlashCommand {
  /// The command name without the leading `/` (e.g. `new`).
  final String name;

  /// Short description shown in the dropdown.
  final String description;

  /// Icon shown next to the command in the dropdown.
  final IconData icon;

  const SlashCommand({
    required this.name,
    required this.description,
    required this.icon,
  });
}

/// Built-in slash commands available in every message input.
const List<SlashCommand> builtInSlashCommands = [
  SlashCommand(
    name: 'new',
    description: 'Start a new session',
    icon: Icons.add_circle_outline,
  ),
  SlashCommand(
    name: 'pause',
    description: 'Pause the current session',
    icon: Icons.pause_circle_outline,
  ),
  SlashCommand(
    name: 'resume',
    description: 'Resume a paused session',
    icon: Icons.play_circle_outline,
  ),
  SlashCommand(
    name: 'cancel',
    description: 'Cancel the current session',
    icon: Icons.cancel_outlined,
  ),
  SlashCommand(
    name: 'export',
    description: 'Export session as markdown',
    icon: Icons.download_outlined,
  ),
];

/// The message input bar. Shared across desktop and mobile.
/// Desktop: Enter sends, Shift+Enter newlines.
/// Mobile: Send button only (soft keyboard handles Enter).
///
/// Supports slash commands: typing `/` at the start of input shows a
/// dropdown of available commands. On selection, [onSlashCommand] fires
/// with the command name (without the `/` prefix).
class MessageInput extends StatefulWidget {
  const MessageInput({
    super.key,
    required this.onSend,
    this.onSlashCommand,
    this.onAttachPressed,
    this.onFilesDropped,
    this.isLoading = false,
    this.enabled = true,
    this.hint = 'Message clawd…',
    this.extraCommands = const [],
    this.attachments = const [],
    this.onRemoveAttachment,
    this.leading,
    this.showAttachButton = false,
  });

  final void Function(String message) onSend;

  /// Called when the user selects a slash command. The argument is the
  /// command name without the leading `/` (e.g. `"new"`, `"pause"`).
  /// If null, slash commands are dispatched through [onSend] as text.
  final void Function(String command)? onSlashCommand;

  /// Called when the user taps the paperclip attachment button.
  /// The consumer is responsible for showing a file picker and calling
  /// back with the selected files.
  final VoidCallback? onAttachPressed;

  /// Called when files are dragged and dropped onto the input area.
  /// Receives a list of file path URIs from the drop event.
  final void Function(List<String> paths)? onFilesDropped;

  final bool isLoading;

  /// When false, the text field and send button are both disabled.
  final bool enabled;
  final String hint;

  /// Additional slash commands beyond the built-in set.
  final List<SlashCommand> extraCommands;

  /// Currently attached file names, shown as removable chips above the input.
  final List<String> attachments;

  /// Called when the user removes an attachment chip by index.
  final void Function(int index)? onRemoveAttachment;

  /// Optional widget placed before the text field (e.g. a paperclip button).
  /// When null and [showAttachButton] is true, a default paperclip button
  /// is rendered that calls [onAttachPressed].
  final Widget? leading;

  /// When true, show a built-in paperclip button to the left of the input.
  /// Requires [onAttachPressed] to be set.
  final bool showAttachButton;

  @override
  State<MessageInput> createState() => _MessageInputState();
}

class _MessageInputState extends State<MessageInput> {
  final _controller = TextEditingController();
  final _focusNode = FocusNode();
  final _layerLink = LayerLink();

  List<SlashCommand> _filteredCommands = [];
  int _selectedCommandIndex = 0;
  OverlayEntry? _overlayEntry;
  bool _isDragOver = false;

  List<SlashCommand> get _allCommands =>
      [...builtInSlashCommands, ...widget.extraCommands];

  @override
  void initState() {
    super.initState();
    _controller.addListener(_onTextChanged);
  }

  @override
  void dispose() {
    _removeOverlay();
    _controller.removeListener(_onTextChanged);
    _controller.dispose();
    _focusNode.dispose();
    super.dispose();
  }

  void _onTextChanged() {
    final text = _controller.text;

    // Only show commands when text starts with `/`.
    if (text.startsWith('/')) {
      final query = text.substring(1).toLowerCase();
      final matches = _allCommands
          .where((c) => c.name.toLowerCase().startsWith(query))
          .toList();

      if (matches.isNotEmpty) {
        setState(() {
          _filteredCommands = matches;
          _selectedCommandIndex = 0;
        });
        _showOverlay();
        return;
      }
    }

    _removeOverlay();
  }

  void _showOverlay() {
    if (!mounted) return;
    _removeOverlay();
    _overlayEntry = OverlayEntry(builder: (_) => _buildCommandOverlay());
    Overlay.of(context).insert(_overlayEntry!);
  }

  void _removeOverlay() {
    _overlayEntry?.remove();
    _overlayEntry = null;
  }

  void _selectCommand(SlashCommand command) {
    _controller.clear();
    _removeOverlay();

    if (widget.onSlashCommand != null) {
      widget.onSlashCommand!(command.name);
    } else {
      widget.onSend('/${command.name}');
    }
  }

  void _send() {
    final text = _controller.text.trim();
    if (text.isEmpty || widget.isLoading || !widget.enabled) return;

    // If the input matches a slash command exactly, dispatch it.
    if (text.startsWith('/')) {
      final cmdName = text.substring(1).toLowerCase();
      final match = _allCommands
          .where((c) => c.name.toLowerCase() == cmdName)
          .firstOrNull;
      if (match != null) {
        _controller.clear();
        _removeOverlay();
        if (widget.onSlashCommand != null) {
          widget.onSlashCommand!(match.name);
        } else {
          widget.onSend(text);
        }
        return;
      }
    }

    _controller.clear();
    _removeOverlay();
    widget.onSend(text);
  }

  KeyEventResult _onKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;

    // When the command overlay is visible, arrow keys and Enter navigate.
    if (_overlayEntry != null && _filteredCommands.isNotEmpty) {
      if (event.logicalKey == LogicalKeyboardKey.arrowDown) {
        setState(() {
          _selectedCommandIndex =
              (_selectedCommandIndex + 1) % _filteredCommands.length;
        });
        _overlayEntry?.markNeedsBuild();
        return KeyEventResult.handled;
      }
      if (event.logicalKey == LogicalKeyboardKey.arrowUp) {
        setState(() {
          _selectedCommandIndex =
              (_selectedCommandIndex - 1 + _filteredCommands.length) %
                  _filteredCommands.length;
        });
        _overlayEntry?.markNeedsBuild();
        return KeyEventResult.handled;
      }
      if (event.logicalKey == LogicalKeyboardKey.tab ||
          (event.logicalKey == LogicalKeyboardKey.enter &&
              !HardwareKeyboard.instance.isShiftPressed)) {
        _selectCommand(_filteredCommands[_selectedCommandIndex]);
        return KeyEventResult.handled;
      }
      if (event.logicalKey == LogicalKeyboardKey.escape) {
        _removeOverlay();
        return KeyEventResult.handled;
      }
    }

    // Normal Enter sends.
    if (event.logicalKey == LogicalKeyboardKey.enter &&
        !HardwareKeyboard.instance.isShiftPressed &&
        _overlayEntry == null) {
      _send();
      return KeyEventResult.handled;
    }

    return KeyEventResult.ignored;
  }

  Widget _buildCommandOverlay() {
    return Positioned(
      width: 280,
      child: CompositedTransformFollower(
        link: _layerLink,
        showWhenUnlinked: false,
        offset: const Offset(0, -8),
        followerAnchor: Alignment.bottomLeft,
        targetAnchor: Alignment.topLeft,
        child: Material(
          elevation: 8,
          color: ClawdTheme.surfaceElevated,
          borderRadius: BorderRadius.circular(8),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 240),
            child: ListView.builder(
              padding: const EdgeInsets.symmetric(vertical: 4),
              shrinkWrap: true,
              itemCount: _filteredCommands.length,
              itemBuilder: (context, index) {
                final cmd = _filteredCommands[index];
                final isSelected = index == _selectedCommandIndex;
                return InkWell(
                  onTap: () => _selectCommand(cmd),
                  child: Container(
                    color: isSelected
                        ? ClawdTheme.claw.withValues(alpha: 0.2)
                        : Colors.transparent,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 8,
                    ),
                    child: Row(
                      children: [
                        Icon(cmd.icon, size: 16, color: ClawdTheme.clawLight),
                        const SizedBox(width: 10),
                        Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text(
                              '/${cmd.name}',
                              style: const TextStyle(
                                fontSize: 13,
                                fontWeight: FontWeight.w600,
                                color: Colors.white,
                              ),
                            ),
                            Text(
                              cmd.description,
                              style: TextStyle(
                                fontSize: 11,
                                color: Colors.white.withValues(alpha: 0.5),
                              ),
                            ),
                          ],
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
          ),
        ),
      ),
    );
  }

  /// Resolves the leading widget: explicit [widget.leading], or a default
  /// paperclip button when [widget.showAttachButton] is true.
  Widget? get _effectiveLeading {
    if (widget.leading != null) return widget.leading;
    if (widget.showAttachButton && widget.onAttachPressed != null) {
      return IconButton(
        onPressed: widget.enabled ? widget.onAttachPressed : null,
        icon: const Icon(Icons.attach_file, size: 20),
        tooltip: 'Attach files',
        style: IconButton.styleFrom(
          foregroundColor: Colors.white54,
        ),
        padding: EdgeInsets.zero,
        constraints: const BoxConstraints(minWidth: 36, minHeight: 36),
      );
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final leading = _effectiveLeading;

    Widget inputArea = CompositedTransformTarget(
      link: _layerLink,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: ClawdTheme.surfaceElevated,
          border: Border(
            top: BorderSide(
              color: _isDragOver
                  ? ClawdTheme.claw
                  : ClawdTheme.surfaceBorder,
              width: _isDragOver ? 2 : 1,
            ),
          ),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // Drop zone hint
            if (_isDragOver)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.symmetric(vertical: 12),
                margin: const EdgeInsets.only(bottom: 8),
                decoration: BoxDecoration(
                  color: ClawdTheme.claw.withValues(alpha: 0.1),
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(
                    color: ClawdTheme.claw.withValues(alpha: 0.4),
                  ),
                ),
                child: const Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Icon(Icons.file_upload_outlined,
                        size: 18, color: ClawdTheme.clawLight),
                    SizedBox(width: 8),
                    Text(
                      'Drop files to attach',
                      style: TextStyle(
                        fontSize: 13,
                        color: ClawdTheme.clawLight,
                      ),
                    ),
                  ],
                ),
              ),
            // Attachment chips
            if (widget.attachments.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Wrap(
                  spacing: 6,
                  runSpacing: 4,
                  children: [
                    for (var i = 0; i < widget.attachments.length; i++)
                      Chip(
                        materialTapTargetSize:
                            MaterialTapTargetSize.shrinkWrap,
                        visualDensity: VisualDensity.compact,
                        avatar: const Icon(Icons.insert_drive_file,
                            size: 14, color: Colors.white54),
                        label: Text(
                          widget.attachments[i],
                          style: const TextStyle(fontSize: 12),
                        ),
                        deleteIcon: const Icon(Icons.close, size: 14),
                        onDeleted: widget.onRemoveAttachment != null
                            ? () => widget.onRemoveAttachment!(i)
                            : null,
                        backgroundColor: ClawdTheme.surface,
                        side: const BorderSide(
                            color: ClawdTheme.surfaceBorder),
                      ),
                  ],
                ),
              ),
            // Input row
            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                if (leading != null) ...[
                  leading,
                  const SizedBox(width: 8),
                ],
                Expanded(
                  child: Focus(
                    onKeyEvent: _onKey,
                    child: TextField(
                      controller: _controller,
                      focusNode: _focusNode,
                      enabled: widget.enabled,
                      minLines: 1,
                      maxLines: 6,
                      decoration: InputDecoration(
                        hintText: widget.hint,
                        filled: true,
                        fillColor: ClawdTheme.surface,
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(10),
                          borderSide: const BorderSide(
                              color: ClawdTheme.surfaceBorder),
                        ),
                        enabledBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(10),
                          borderSide: const BorderSide(
                              color: ClawdTheme.surfaceBorder),
                        ),
                        focusedBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(10),
                          borderSide:
                              const BorderSide(color: ClawdTheme.claw),
                        ),
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 8,
                        ),
                      ),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                IconButton.filled(
                  onPressed:
                      (widget.isLoading || !widget.enabled) ? null : _send,
                  icon: widget.isLoading
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.arrow_upward, size: 18),
                  style: IconButton.styleFrom(
                    backgroundColor: ClawdTheme.claw,
                    foregroundColor: Colors.white,
                    disabledBackgroundColor: ClawdTheme.surfaceBorder,
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );

    // Wrap in a DragTarget when the consumer supports file drops.
    if (widget.onFilesDropped != null) {
      inputArea = DragTarget<Object>(
        onWillAcceptWithDetails: (_) {
          if (!_isDragOver) setState(() => _isDragOver = true);
          return true;
        },
        onLeave: (_) {
          if (_isDragOver) setState(() => _isDragOver = false);
        },
        onAcceptWithDetails: (details) {
          setState(() => _isDragOver = false);
          final data = details.data;
          if (data is Iterable) {
            final paths = data
                .map((e) => e.toString())
                .where((s) => s.isNotEmpty)
                .toList();
            if (paths.isNotEmpty) {
              widget.onFilesDropped!(paths);
            }
          } else {
            final path = data.toString();
            if (path.isNotEmpty) {
              widget.onFilesDropped!([path]);
            }
          }
        },
        builder: (context, candidateData, rejectedData) => inputArea,
      );
    }

    return inputArea;
  }
}
