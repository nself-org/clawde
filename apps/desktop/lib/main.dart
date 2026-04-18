import 'dart:async' show unawaited;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:window_manager/window_manager.dart';
import 'package:clawde/app.dart';
import 'package:clawde/services/daemon_manager.dart';
import 'package:clawde/services/hotkey_service.dart';
import 'package:clawde/services/snackbar_service.dart';
import 'package:clawde/services/tray_service.dart';
import 'package:clawde/services/updater_service.dart';
import 'package:clawd_core/clawd_core.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await windowManager.ensureInitialized();
  await UpdaterService.instance.init();
  await DaemonManager.instance.ensureRunning();

  // Initialise tray BEFORE the window is shown so the icon is ready.
  await TrayService.instance.init(
    onQuit: () async {
      await DaemonManager.instance.shutdown();
      await windowManager.destroy();
    },
  );

  // Show tray error state if the daemon failed to start.
  if (DaemonManager.instance.startFailed) {
    await TrayService.instance.setState(TrayIconState.error);
  }

  const WindowOptions windowOptions = WindowOptions(
    minimumSize: Size(900, 600),
    size: Size(1280, 800),
    center: true,
    title: 'ClawDE',
    titleBarStyle: TitleBarStyle.normal,
  );
  await windowManager.waitUntilReadyToShow(windowOptions, () async {
    await windowManager.show();
    await windowManager.focus();
  });

  await HotkeyService.instance.init(onActivated: () {
    windowManager.show();
    windowManager.focus();
  });

  // Intercept window close — minimize to tray rather than quit.
  windowManager.addListener(_AppWindowListener());
  await windowManager.setPreventClose(true);

  // Check for updates 5 s after startup (intentionally fire-and-forget).
  unawaited(Future.delayed(const Duration(seconds: 5),
      () => UpdaterService.instance.checkInBackground()));

  runApp(ProviderScope(
    overrides: [
      // Inject the token obtained by DaemonManager so DaemonNotifier does not
      // need to race against the token file appearing on disk.
      bootstrapTokenProvider.overrideWithValue(
        DaemonManager.instance.tokenOverride,
      ),
    ],
    child: const ClawDEApp(),
  ));

  // FA-H1: Show error banner if the bundled daemon failed to start.
  if (DaemonManager.instance.startFailed) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      SnackbarService.instance.showError(
        'clawd daemon failed to start. Sessions will not be available.',
      );
    });
  }
}

class _AppWindowListener extends WindowListener {
  @override
  Future<void> onWindowClose() async {
    // Minimize to tray instead of quitting.
    // The user quits via "Quit ClawDE" in the tray menu.
    await windowManager.hide();
  }
}
