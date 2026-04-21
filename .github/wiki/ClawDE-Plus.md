# ClawDE+

> **Status: Coming v1.1.0** — ClawDE+ subscription is not yet available at v1.0.9. The ClawDE desktop app ships as Free Beta. Full ClawDE+ (server-sync, mobile companion, team features) launches at v1.1.0. Bundle ID registration and store submission are also v1.1.0 targets (mobile apps are in alpha; not on App Store / Play Store at v1.0.9).

ClawDE+ ($1.99/mo or $19.99/yr) adds server-sync, mobile companion access, and team features to the free ClawDE desktop app.

ClawDE+ is not part of the nSelf plugin store. It is an in-app subscription for ClawDE specifically. The license is validated by ClawDE's own auth flow against `api.clawde.io/daemon/verify`.

## Activating ClawDE+

1. Subscribe at [nself.org/clawde/plus](https://nself.org/clawde/plus)
2. Open ClawDE and go to **Settings → ClawDE+**
3. Your license is validated automatically on each app launch against `api.clawde.io/daemon/verify`

Your license key is stored in the system keychain. No manual entry is needed after the first activation. If your subscription lapses, ClawDE continues working in free mode: local sessions, LAN access, and local-only storage remain available.

## Features

### Server Sync

Session titles and status sync to ClawDE's servers every 30 seconds while the desktop app is running. The sync is incremental: only changed fields are transmitted. Session content (messages, tool-call outputs) is not synced — only metadata needed to display the session list on mobile.

The sync endpoint is `api.clawde.io/sync/sessions`. The daemon posts a signed payload using your license key as the identity token. No session content leaves your machine.

Sync is controlled via the `sync.setEnabled` RPC method. The desktop app exposes this as a toggle in **Settings → ClawDE+ → Sync**.

To disable sync without cancelling ClawDE+, toggle **Settings → ClawDE+ → Sync** off.

### Mobile Companion (iOS and Android)

The ClawDE mobile app connects to your desktop daemon via the relay at `api.clawde.io`. ClawDE+ is required on the desktop; the mobile app itself is free to download.

The mobile app reads your session list from `GET api.clawde.io/sync/sessions`. This endpoint is available only when ClawDE+ sync is active on the desktop.

What you can do from mobile:

- View all active and recent sessions in real time
- Read the full message history for any session
- Approve or deny pending tool calls
- Send a message to a running session
- Start a new session (the daemon on your desktop executes it)

The mobile app discovers your desktop over LAN automatically when both devices are on the same network. For off-LAN access (e.g. from your phone on cellular), the relay is used instead. The relay connection is end-to-end encrypted: the relay sees only the session ID, not the content.

### Team Features

Team features let multiple people share sessions on a single daemon. This is intended for pair-programming, code review, and async handoff scenarios.

- **Invite a collaborator:** share a one-time join link from **Settings → ClawDE+ → Team → Invite**. The invited user connects with their own ClawDE desktop app.
- **Co-present sessions:** both users see the same message stream in real time.
- **Role control:** the session owner controls tool-call approvals by default. The owner can grant approval rights to a collaborator from the session toolbar.
- **Session handoff:** transfer full session ownership to another team member. Useful for async review: finish your part, hand off, and let a colleague continue.

Team features require ClawDE+ for the session host. The daemon routes collaboration traffic through the relay — no direct peer-to-peer connection between team members' machines.

The relevant RPC methods for team management are `team.listMembers` (returns current collaborators on a session) and `team.setSharedSessions` (controls which sessions are visible to team members).

## Pricing

| Billing | Price |
| --- | --- |
| Monthly | $1.99/mo |
| Annual | $19.99/yr (saves ~17%) |

Cancel any time from your account page at [nself.org/account](https://nself.org/account). After cancellation, ClawDE+ features are available until the end of the current billing period, then the app reverts to free mode.

## Related

- [[Getting-Started]] — install ClawDE and run your first session
- [[Multi-Account]] — round-robin account switching on Personal Remote tier
- [[Configuration]] — `config.toml` reference, including sync and team settings
- [[Daemon-Reference|Daemon API Reference]] — `sync.*` and `team.*` RPC methods
