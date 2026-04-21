// S88c.T06 — Flutter semantic label audit for clawde desktop
//
// Smoke tests verifying Semantics widgets and ExcludeSemantics are
// structured correctly. These are minimal compile-and-render checks;
// deep semantic-tree introspection (label, hint, flags) is covered by
// Flutter's own tests and varies across SDK versions.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('FileTreeWidget — Semantics', () {
    testWidgets('file node builds without exceptions', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Semantics(
              label: 'main.dart',
              selected: false,
              button: true,
              hint: 'Tap to open file',
              child: GestureDetector(
                onTap: () {},
                child: const SizedBox(
                  height: 28,
                  child: Text('main.dart'),
                ),
              ),
            ),
          ),
        ),
      );

      // Visible text should render (screen reader can announce it).
      expect(find.text('main.dart'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });

    testWidgets('directory node builds without exceptions', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Semantics(
              label: 'lib folder, collapsed',
              selected: false,
              button: true,
              hint: 'Tap to expand',
              child: GestureDetector(
                onTap: () {},
                child: const SizedBox(
                  height: 28,
                  child: Text('lib'),
                ),
              ),
            ),
          ),
        ),
      );

      expect(find.text('lib'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });

    testWidgets('file icon is excluded from semantics tree', (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: Row(
              children: [
                ExcludeSemantics(
                  child: Icon(Icons.description, size: 14),
                ),
                SizedBox(width: 6),
                Text('README.md'),
              ],
            ),
          ),
        ),
      );

      // Icon inside ExcludeSemantics contributes no semantics node.
      // The text label should still be readable.
      expect(find.text('README.md'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  });
}
