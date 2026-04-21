// S52-T10: Widget tests for HelpWidget in ClawDE mobile.
//
// Tests:
//   - HelpWidget renders in a ListTile with correct title
//   - Tap fires the correct action without exception

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:clawde_mobile/widgets/help_widget.dart';

void main() {
  group('HelpWidget — clawde_mobile', () {
    testWidgets('renders ListTile with Help & Feedback title', (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: HelpWidget(),
          ),
        ),
      );

      expect(find.text('Help & Feedback'), findsOneWidget);
      expect(find.text('Contact support or join Discord'), findsOneWidget);
      expect(find.byIcon(Icons.help_outline), findsOneWidget);
    });

    testWidgets('tap on HelpWidget does not throw', (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: HelpWidget(),
          ),
        ),
      );

      await tester.tap(find.byType(HelpWidget));
      await tester.pumpAndSettle();

      expect(tester.takeException(), isNull);
    });
  });
}
