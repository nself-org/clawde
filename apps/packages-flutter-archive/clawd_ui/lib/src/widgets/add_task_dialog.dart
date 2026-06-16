import 'package:flutter/material.dart';
import 'package:clawd_proto/clawd_proto.dart';

/// Dialog for adding a new task to the queue.
class AddTaskDialog extends StatefulWidget {
  const AddTaskDialog({super.key, this.repoPath});

  final String? repoPath;

  static Future<Map<String, dynamic>?> show(
    BuildContext context, {
    String? repoPath,
  }) =>
      showDialog<Map<String, dynamic>>(
        context: context,
        builder: (_) => AddTaskDialog(repoPath: repoPath),
      );

  @override
  State<AddTaskDialog> createState() => _AddTaskDialogState();
}

class _AddTaskDialogState extends State<AddTaskDialog> {
  final _title = TextEditingController();
  TaskType _type = TaskType.task;
  TaskSeverity _severity = TaskSeverity.medium;
  final _phase = TextEditingController();
  final _tags = TextEditingController();

  @override
  void initState() {
    super.initState();
    _title.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _title.dispose();
    _phase.dispose();
    _tags.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Add Task'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TextField(
              controller: _title,
              decoration: const InputDecoration(
                labelText: 'Title *',
                hintText: 'What needs to be done?',
              ),
              textCapitalization: TextCapitalization.sentences,
              autofocus: true,
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: DropdownButtonFormField<TaskType>(
                    initialValue: _type,
                    decoration: const InputDecoration(labelText: 'Type'),
                    items: TaskType.values
                        .map((t) => DropdownMenuItem(
                              value: t,
                              child: Text(t.toJsonStr()),
                            ))
                        .toList(),
                    onChanged: (v) => setState(() => _type = v!),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: DropdownButtonFormField<TaskSeverity>(
                    initialValue: _severity,
                    decoration: const InputDecoration(labelText: 'Severity'),
                    items: TaskSeverity.values
                        .map((s) => DropdownMenuItem(
                              value: s,
                              child: Text(s.toJsonStr()),
                            ))
                        .toList(),
                    onChanged: (v) => setState(() => _severity = v!),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _phase,
              decoration: const InputDecoration(
                labelText: 'Phase (optional)',
                hintText: 'e.g. 41-QA',
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _tags,
              decoration: const InputDecoration(
                labelText: 'Tags (comma-separated)',
                hintText: 'e.g. dart, ui, bug',
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _title.text.trim().isEmpty ? null : _submit,
          child: const Text('Add'),
        ),
      ],
    );
  }

  void _submit() {
    if (_title.text.trim().isEmpty) return;
    final tags = _tags.text
        .split(',')
        .map((t) => t.trim())
        .where((t) => t.isNotEmpty)
        .toList();
    Navigator.pop(context, {
      'title': _title.text.trim(),
      'task_type': _type.toJsonStr(),
      'severity': _severity.toJsonStr(),
      if (_phase.text.isNotEmpty) 'phase': _phase.text.trim(),
      if (tags.isNotEmpty) 'tags': tags,
      if (widget.repoPath != null) 'repo_path': widget.repoPath,
    });
  }
}
