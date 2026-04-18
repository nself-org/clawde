# ClawDE Mobile

Mobile companion for ClawDE: review sessions, approve or deny remote agent plans, and stay in sync from iOS or Android.

## Description

This is the Flutter mobile client for ClawDE. It pairs with a `clawd` daemon (running on your laptop, home lab, or ClawDE Cloud) and lets you monitor sessions, reply to agents, and approve worktree changes from your phone. Cloud sync requires the **ClawDE+ Bundle** (per F06 in `~/Sites/nself/.claude/docs/sport/F06-BUNDLE-INVENTORY.md`); local LAN pairing works free.

For the full ClawDE README and architecture overview, see [../../README.md](../../README.md). For ecosystem context, see the PRI at `~/Sites/nself/clawde/.claude/CLAUDE.md`.

## Prerequisites

- Flutter 3.x (`flutter doctor` should be clean)
- Dart 3.x
- iOS toolchain: Xcode + CocoaPods, valid Apple Developer account for device builds
- Android toolchain: Android Studio + JDK 17
- A `clawd` daemon paired to this device (LAN via mDNS, or remote via the ClawDE relay)

## Run (dev)

```bash
# From the repo root, one-time
cd apps && melos bootstrap

# iOS simulator
cd apps/mobile
flutter run -d ios

# Android emulator
flutter run -d android
```

The app discovers daemons on the local network via mDNS. To pair:

```bash
# On the daemon host
clawd pair --show-qr
```

Scan the QR in the mobile app to trust the device.

## Build (release)

```bash
cd apps/mobile

# iOS (requires Apple Developer cert)
flutter build ios --release
# Then archive in Xcode for App Store / TestFlight

# Android
flutter build apk --release
# Output: build/app/outputs/flutter-apk/app-release.apk
flutter build appbundle --release    # for Play Console upload
```

### Code signing

Credentials live in `~/.claude/vault.env`. Reference vault variables only. Never commit certificates, keystores, or provisioning profiles to the repo.

| Platform | Vault var | Purpose |
|----------|-----------|---------|
| iOS | `APPLE_DEV_TEAM_ID` | Team ID for `xcodebuild` |
| Android | `CLAWDE_ANDROID_KEYSTORE_PATH` | Path to release keystore |
| Android | `CLAWDE_ANDROID_KEYSTORE_PASSWORD` | Keystore password |

## Distribution

| Channel | Where | Cadence |
|---------|-------|---------|
| TestFlight (iOS) | App Store Connect | Per release |
| Play Console internal track | Google Play | Per release |
| App Store (iOS) | App Store Connect | Tagged releases |
| Play Store (Android) | Google Play | Tagged releases |

Never upload a build without an approved Release Plan in `.claude/planning/release-{version}.md` (per GCI Version & Release Lock).

## Test

```bash
cd apps/mobile
flutter test
```

## ClawDE+ Bundle

Cloud sync (review sessions from any device, server-side history, push notifications when an agent finishes) requires the **ClawDE+ Bundle** ($1.99/mo / $19.99/yr per F07-PRICING-TIERS). The free tier supports LAN-paired daemons only. License activation happens through `clawd license set ...` on the daemon host; the mobile app inherits that license over the paired connection.

## License

MIT. See [../../LICENSE](../../LICENSE).
