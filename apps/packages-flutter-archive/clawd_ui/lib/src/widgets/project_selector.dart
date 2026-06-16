import 'package:flutter/material.dart';

/// Dropdown that lets the user switch between registered projects or "All".
class ProjectSelector extends StatelessWidget {
  const ProjectSelector({
    super.key,
    required this.projects,
    this.selected,
    this.onChanged,
  });

  /// List of registered repo paths.
  final List<String> projects;

  /// Currently selected repo path, or null for "All Projects".
  final String? selected;
  final ValueChanged<String?>? onChanged;

  String _label(String? path) {
    if (path == null) return 'All Projects';
    final parts = path.split('/');
    return parts.length >= 2
        ? '${parts[parts.length - 2]}/${parts.last}'
        : path;
  }

  @override
  Widget build(BuildContext context) {
    final items = <DropdownMenuItem<String?>>[
      const DropdownMenuItem(value: null, child: Text('All Projects')),
      ...projects
          .map((p) => DropdownMenuItem(value: p, child: Text(_label(p)))),
    ];
    return DropdownButton<String?>(
      value: selected,
      isDense: true,
      items: items,
      onChanged: onChanged,
    );
  }
}
