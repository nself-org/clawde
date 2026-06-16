// SPDX-License-Identifier: MIT
// Editor state types and Riverpod provider (Sprint HH, ED.15).

import 'package:flutter_riverpod/flutter_riverpod.dart';

// ─── Value types ──────────────────────────────────────────────────────────────

/// Represents a file open in the editor.
class EditorFile {
  const EditorFile({
    required this.path,
    required this.content,
    required this.language,
    this.isDirty = false,
  });

  final String path;
  final String content;
  final String language;
  final bool isDirty;

  String get fileName {
    final parts = path.replaceAll('\\', '/').split('/');
    return parts.last;
  }

  EditorFile copyWith({String? content, bool? isDirty}) => EditorFile(
        path: path,
        content: content ?? this.content,
        language: language,
        isDirty: isDirty ?? this.isDirty,
      );
}

/// A tab in the multi-tab editor.
class EditorTab {
  const EditorTab({required this.file, this.isActive = false});

  final EditorFile file;
  final bool isActive;

  EditorTab copyWith({EditorFile? file, bool? isActive}) =>
      EditorTab(file: file ?? this.file, isActive: isActive ?? this.isActive);
}

/// Emitted by the CodeMirror editor via the JS bridge.
class EditorStateEvent {
  const EditorStateEvent({required this.type, this.content, this.path, this.cursorLine, this.cursorCol});

  /// `"change"` | `"save"` | `"cursorMove"` | `"ready"`
  final String type;
  final String? content;
  final String? path;
  final int? cursorLine;
  final int? cursorCol;
}

// ─── Editor state ─────────────────────────────────────────────────────────────

/// State for the multi-tab code editor.
class EditorState {
  const EditorState({this.tabs = const [], this.activeIndex = -1});

  final List<EditorTab> tabs;
  final int activeIndex;

  EditorTab? get activeTab => activeIndex >= 0 && activeIndex < tabs.length ? tabs[activeIndex] : null;

  EditorState openFile(EditorFile file) {
    final existing = tabs.indexWhere((t) => t.file.path == file.path);
    if (existing >= 0) {
      return EditorState(tabs: tabs, activeIndex: existing);
    }
    final newTabs = [...tabs, EditorTab(file: file)];
    return EditorState(tabs: newTabs, activeIndex: newTabs.length - 1);
  }

  EditorState closeTab(int index) {
    if (index < 0 || index >= tabs.length) return this;
    final newTabs = [...tabs]..removeAt(index);
    final newIndex = newTabs.isEmpty ? -1 : (activeIndex >= newTabs.length ? newTabs.length - 1 : activeIndex);
    return EditorState(tabs: newTabs, activeIndex: newIndex);
  }

  EditorState setActive(int index) => EditorState(tabs: tabs, activeIndex: index);

  EditorState markDirty(int index) {
    if (index < 0 || index >= tabs.length) return this;
    final newTabs = [...tabs];
    newTabs[index] = newTabs[index].copyWith(file: newTabs[index].file.copyWith(isDirty: true));
    return EditorState(tabs: newTabs, activeIndex: activeIndex);
  }
}

// ─── Provider ─────────────────────────────────────────────────────────────────

class EditorNotifier extends Notifier<EditorState> {
  @override
  EditorState build() => const EditorState();

  void openFile(EditorFile file) => state = state.openFile(file);
  void closeTab(int index) => state = state.closeTab(index);
  void setActive(int index) => state = state.setActive(index);
  void markDirty(int index) => state = state.markDirty(index);
}

final editorProvider = NotifierProvider<EditorNotifier, EditorState>(EditorNotifier.new);
