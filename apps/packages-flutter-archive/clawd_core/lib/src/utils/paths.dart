import 'dart:io' show Platform;

/// Platform-appropriate path for the daemon's auth token file.
///
/// Must match the path computed by `clawd/src/config/mod.rs`.
/// Shared between DaemonManager (desktop) and DaemonNotifier (all platforms).
///
/// Returns null on platforms where the daemon does not run locally
/// (Android, iOS) or when the required environment variable is unset.
String? clawdTokenFilePath() {
  if (Platform.isMacOS) {
    final home = Platform.environment['HOME'];
    return home != null
        ? '$home/Library/Application Support/clawd/auth_token'
        : null;
  }
  if (Platform.isLinux) {
    final xdg = Platform.environment['XDG_DATA_HOME'];
    if (xdg != null) return '$xdg/clawd/auth_token';
    final home = Platform.environment['HOME'];
    return home != null ? '$home/.local/share/clawd/auth_token' : null;
  }
  if (Platform.isWindows) {
    final appdata = Platform.environment['APPDATA'];
    return appdata != null ? '$appdata\\clawd\\auth_token' : null;
  }
  // Android / iOS — no local daemon, token comes from host pairing.
  return null;
}
