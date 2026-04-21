# ClawDE Mobile — Code Signing & Distribution

**Scope:** `apps/mobile/` (iOS + Android Flutter app)
**Parent standard:** `~/Sites/nself/.claude/docs/mobile-platform/MOBILE-PLATFORM-STANDARD.md`

This is the ClawDE-mobile-specific instantiation of the nSelf Mobile
Platform Standard (sections 6.1 and 6.2). It never contains secrets.
Secrets live in `~/.claude/vault.env` and GitHub Actions secrets.

---

## 1. Bundle IDs

| Platform | Bundle ID | Notes |
|---|---|---|
| iOS (main app) | `com.nself.clawde.mobile` | Must match AASA template (`ios/apple-app-site-association-template.json`) |
| iOS (notification extension) | `com.nself.clawde.mobile.notifications` | Reserved; enable when rich push is added |
| Android (application ID) | `com.nself.clawde.mobile` | Must match `assetlinks-template.json` |

---

## 2. Vault variables

All secrets pulled from `~/.claude/vault.env` and mirrored into GitHub
Actions secrets with identical names.

### iOS / Apple

| Vault var | Purpose |
|---|---|
| `APPLE_TEAM_ID` | Shared across nSelf apps; used in AASA + entitlements |
| `APP_STORE_CONNECT_API_KEY_ID` | Key ID for App Store Connect API |
| `APP_STORE_CONNECT_API_ISSUER_ID` | Issuer ID |
| `APP_STORE_CONNECT_API_KEY_CONTENT` | Base64 `.p8` |
| `MATCH_PASSWORD` | Decrypt password for `match_clawde_certs` |
| `MATCH_GIT_URL` | Private git URL for match certs |
| `MATCH_GIT_BASIC_AUTHORIZATION` | Base64 `user:pat` for match repo |
| `APNS_KEY_ID_CLAWDE` | APNs auth key ID (scoped per app per platform standard §2.3) |
| `APNS_KEY_P8_CLAWDE` | Base64 APNs `.p8` |

### Android / Google

| Vault var | Purpose |
|---|---|
| `ANDROID_KEYSTORE_BASE64_CLAWDE` | Base64-encoded release keystore |
| `ANDROID_KEYSTORE_PASSWORD_CLAWDE` | Keystore password |
| `ANDROID_KEY_ALIAS_CLAWDE` | Key alias |
| `ANDROID_KEY_PASSWORD_CLAWDE` | Key password |
| `PLAY_SERVICE_ACCOUNT_JSON` | Play Console service account (shared across nSelf apps) |
| `FCM_SERVICE_ACCOUNT_JSON_CLAWDE` | Firebase Admin SDK service account |

---

## 3. iOS setup workflow

1. Enrol `com.nself.clawde.mobile` in Apple Developer portal; enable
   Push Notifications and Associated Domains capabilities.
2. Create a private repo under `nself-org` named `match_clawde_certs`.
3. On a dev machine with access to Xcode:
   ```bash
   cd apps/mobile/ios
   fastlane match init
   fastlane match appstore
   fastlane match development
   ```
4. Sync vault → GitHub Actions:
   ```bash
   gh secret set APPLE_TEAM_ID --body "$APPLE_TEAM_ID"
   gh secret set MATCH_PASSWORD --body "$MATCH_PASSWORD"
   # ... (repeat for every vault var in section 2)
   ```
5. Verify a CI release build produces a TestFlight submission; see
   `.github/workflows/mobile-release.yml` (to be added in the release
   sprint following S37).

### Rotation

- Apple cert expiry reminder fires 30 days out via the release runbook.
- `fastlane match nuke distribution` then `fastlane match appstore`
  regenerates. Update vault + GitHub Actions. Next CI run picks up the
  new material.

---

## 4. Android setup workflow

1. Generate a release keystore once per app:
   ```bash
   keytool -genkey -v \
     -keystore clawde-mobile-release.keystore \
     -alias clawde-mobile-release \
     -keyalg RSA -keysize 4096 -validity 10000
   ```
2. Base64 the keystore:
   ```bash
   base64 -i clawde-mobile-release.keystore | pbcopy
   ```
   Paste into vault as `ANDROID_KEYSTORE_BASE64_CLAWDE`.
3. In Play Console:
   - Enable Play App Signing.
   - Upload the public cert extracted from the keystore.
   - Under API access, link the Play service account.
4. Pull SHA-256 fingerprint (for assetlinks.json) from Play Console
   → App integrity → App signing → SHA-256 certificate fingerprint.
   Paste into `assetlinks-template.json` via CI substitution.
5. First CI release uploads to the internal testing track.

### Rotation

- Upload key rotation uses Play Console key replacement flow; Google
  re-signs with the app signing key automatically.
- App signing key is Google-managed and does not rotate unless
  compromised.

---

## 5. AASA + assetlinks hosting

- Rendered from `ios/apple-app-site-association-template.json` and
  `android/assetlinks-template.json` by the web repo's release job.
- Hosted by `web/clawde/` (or equivalent):
  - `https://clawde.io/.well-known/apple-app-site-association`
  - `https://clawde.io/.well-known/assetlinks.json`
- Both files are refreshed whenever a new cert or new bundle ID ships.

---

## 6. Never-commit policy

- `ios/Runner.xcodeproj/project.pbxproj` must not contain Team IDs or
  provisioning profile UUIDs. Use `DEVELOPMENT_TEAM = $(APPLE_TEAM_ID)`
  and `CODE_SIGN_STYLE = Manual` + fastlane match.
- `android/key.properties` is gitignored. Only `key.properties.template`
  may be committed.
- `google-services.json` is public metadata and safe to commit.
- `GoogleService-Info.plist` is public metadata and safe to commit.
- Nothing else may live in the app source tree.

---

**Last updated:** 2026-04-20 (S52-T02: renamed Android applicationId from io.nself.clawde.mobile to com.nself.clawde.mobile; both platforms now use com.nself.clawde.mobile)
