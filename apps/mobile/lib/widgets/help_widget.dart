import 'dart:io';
import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:url_launcher/url_launcher.dart';

// S52-T10: In-app help/feedback widget for ClawDE mobile
// Tap opens email composer with templated subject (version + OS + app name).
// Falls back to Discord link when email is not configured on the device.

const String _kSupportEmail = 'support@nself.org';
const String _kDiscordUrl = 'https://discord.gg/nself';

/// Minimal help/feedback button for settings/profile screens.
///
/// UI states handled:
///   - populated  : email composer opens with templated subject
///   - error      : email not configured → shows Discord fallback
///   - offline    : same as error — Discord link works offline
class HelpWidget extends StatelessWidget {
  const HelpWidget({super.key});

  Future<void> _openSupport(BuildContext context) async {
    PackageInfo info;
    try {
      info = await PackageInfo.fromPlatform();
    } catch (_) {
      info = PackageInfo(
        appName: 'ClawDE',
        packageName: 'com.nself.clawde.mobile',
        version: 'unknown',
        buildNumber: '0',
      );
    }

    final String platform = Platform.isIOS
        ? 'iOS'
        : Platform.isAndroid
            ? 'Android'
            : Platform.isMacOS
                ? 'macOS'
                : 'unknown';

    final String version = info.version;
    final String subject = '[v$version / $platform / ClawDE] Help request';
    final Uri emailUri = Uri(
      scheme: 'mailto',
      path: _kSupportEmail,
      queryParameters: {
        'subject': subject,
        'body': '\n\n---\nApp: ClawDE $version\nPlatform: $platform',
      },
    );

    if (await canLaunchUrl(emailUri)) {
      await launchUrl(emailUri);
    } else {
      // Email not configured — fall back to Discord
      if (context.mounted) {
        _showDiscordFallback(context);
      }
    }
  }

  void _showDiscordFallback(BuildContext context) {
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Get Help'),
        content: const Text(
          'Email is not configured on this device.\n\n'
          'Join the nSelf Discord community to get help from the team.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () async {
              Navigator.of(ctx).pop();
              final uri = Uri.parse(_kDiscordUrl);
              if (await canLaunchUrl(uri)) {
                await launchUrl(uri, mode: LaunchMode.externalApplication);
              }
            },
            child: const Text('Open Discord'),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: const Icon(Icons.help_outline),
      title: const Text('Help & Feedback'),
      subtitle: const Text('Contact support or join Discord'),
      trailing: const Icon(Icons.chevron_right),
      onTap: () => _openSupport(context),
    );
  }
}
