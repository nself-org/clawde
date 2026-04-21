// SPDX-License-Identifier: MIT
// File tree sidebar widget (Sprint HH, ED.12).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';

/// A single node in the file tree.
class FileNode {
  FileNode({required this.name, required this.path, this.isDirectory = false, List<FileNode>? children})
      : children = children ?? [];

  final String name;
  final String path;
  final bool isDirectory;
  final List<FileNode> children;
  bool isExpanded = false;
}

/// Left-sidebar file browser for the desktop editor.
///
/// Uses `fs.list` RPC to fetch directory contents.  Single-click opens a file
/// in the editor; right-click shows a context menu.
class FileTreeWidget extends ConsumerStatefulWidget {
  const FileTreeWidget({
    super.key,
    required this.rootPath,
    this.onFileOpen,
    this.onFileDelete,
    this.onFileRename,
    this.onNewFile,
  });

  final String rootPath;
  final ValueChanged<String>? onFileOpen;
  final ValueChanged<String>? onFileDelete;
  final void Function(String oldPath, String newPath)? onFileRename;
  final ValueChanged<String>? onNewFile;

  @override
  ConsumerState<FileTreeWidget> createState() => _FileTreeWidgetState();
}

class _FileTreeWidgetState extends ConsumerState<FileTreeWidget> {
  List<FileNode> _nodes = [];
  bool _loading = true;
  String? _selected;

  @override
  void initState() {
    super.initState();
    _loadDirectory(widget.rootPath, null);
  }

  Future<void> _loadDirectory(String path, FileNode? parent) async {
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final result = await client.call<Map<String, dynamic>>('fs.list', {'path': path});
      final entries = result['entries'] as List<dynamic>? ?? [];
      final nodes = entries.map((e) {
        final m = e as Map<String, dynamic>;
        return FileNode(
          name: m['name'] as String? ?? '',
          path: m['path'] as String? ?? '',
          isDirectory: m['isDirectory'] as bool? ?? false,
        );
      }).toList()
        ..sort((a, b) {
          if (a.isDirectory != b.isDirectory) return a.isDirectory ? -1 : 1;
          return a.name.compareTo(b.name);
        });

      if (mounted) {
        setState(() {
          if (parent == null) {
            _nodes = nodes;
          } else {
            parent.children
              ..clear()
              ..addAll(nodes);
          }
          _loading = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  void _onTap(FileNode node) {
    if (node.isDirectory) {
      setState(() {
        node.isExpanded = !node.isExpanded;
        if (node.isExpanded && node.children.isEmpty) {
          _loadDirectory(node.path, node);
        }
      });
    } else {
      setState(() => _selected = node.path);
      widget.onFileOpen?.call(node.path);
    }
  }

  void _showContextMenu(BuildContext context, FileNode node, Offset position) {
    showMenu<String>(
      context: context,
      position: RelativeRect.fromLTRB(position.dx, position.dy, position.dx + 1, position.dy + 1),
      items: [
        if (!node.isDirectory) const PopupMenuItem(value: 'open', child: Text('Open')),
        const PopupMenuItem(value: 'rename', child: Text('Rename')),
        const PopupMenuItem(value: 'delete', child: Text('Delete')),
        if (node.isDirectory) const PopupMenuItem(value: 'new_file', child: Text('New File')),
      ],
    ).then((action) {
      if (action == 'open') widget.onFileOpen?.call(node.path);
      if (action == 'delete') widget.onFileDelete?.call(node.path);
      if (action == 'new_file') widget.onNewFile?.call(node.path);
    });
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_nodes.isEmpty) {
      return const Center(
        child: Text('Empty directory', style: TextStyle(color: Color(0xFF6b7280), fontSize: 12)),
      );
    }
    return ListView.builder(
      itemCount: _nodes.length,
      itemBuilder: (ctx, i) => _FileNodeTile(
        node: _nodes[i],
        depth: 0,
        selected: _selected,
        onTap: _onTap,
        onContextMenu: (node, pos) => _showContextMenu(context, node, pos),
      ),
    );
  }
}

class _FileNodeTile extends StatelessWidget {
  const _FileNodeTile({
    required this.node,
    required this.depth,
    required this.selected,
    required this.onTap,
    required this.onContextMenu,
  });

  final FileNode node;
  final int depth;
  final String? selected;
  final ValueChanged<FileNode> onTap;
  final void Function(FileNode, Offset) onContextMenu;

  @override
  Widget build(BuildContext context) {
    final isSelected = selected == node.path;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Semantics(
          label: node.isDirectory
              ? '${node.name} folder${node.isExpanded ? ", expanded" : ", collapsed"}'
              : node.name,
          selected: isSelected,
          button: true,
          hint: node.isDirectory
              ? (node.isExpanded ? 'Tap to collapse' : 'Tap to expand')
              : 'Tap to open file',
          child: GestureDetector(
            onTap: () => onTap(node),
            onSecondaryTapDown: (d) => onContextMenu(node, d.globalPosition),
            child: Container(
              color: isSelected ? const Color(0xFF1f2937) : Colors.transparent,
              padding: EdgeInsets.only(left: 8.0 + depth * 12.0, right: 8, top: 4, bottom: 4),
              child: Row(
                children: [
                  ExcludeSemantics(
                    child: Icon(
                      node.isDirectory
                          ? (node.isExpanded ? Icons.folder_open : Icons.folder)
                          : _fileIcon(node.name),
                      size: 14,
                      color: node.isDirectory ? const Color(0xFFfbbf24) : const Color(0xFF6b7280),
                    ),
                  ),
                  const SizedBox(width: 6),
                  Flexible(
                    child: Text(
                      node.name,
                      style: TextStyle(
                        fontSize: 12,
                        color: isSelected ? Colors.white : const Color(0xFFd1d5db),
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
        if (node.isDirectory && node.isExpanded)
          ...node.children.map(
            (child) => _FileNodeTile(
              node: child,
              depth: depth + 1,
              selected: selected,
              onTap: onTap,
              onContextMenu: onContextMenu,
            ),
          ),
      ],
    );
  }

  IconData _fileIcon(String name) {
    final ext = name.contains('.') ? name.split('.').last.toLowerCase() : '';
    return switch (ext) {
      'rs' => Icons.memory,
      'dart' => Icons.flutter_dash,
      'ts' || 'tsx' || 'js' || 'jsx' => Icons.javascript,
      'html' => Icons.html,
      'css' || 'scss' => Icons.style,
      'json' => Icons.data_object,
      'md' => Icons.article_outlined,
      'yaml' || 'yml' => Icons.settings_outlined,
      'toml' => Icons.tune,
      'png' || 'jpg' || 'svg' => Icons.image_outlined,
      _ => Icons.insert_drive_file_outlined,
    };
  }
}
