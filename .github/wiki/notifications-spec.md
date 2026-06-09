# ClawDE Workstation Notifications Specification

**Spec version:** P1-E2-W5-S05-T03
**Status:** Planned
**References:** ADR-001, ADR-008, daemon-spec.md (P1-E2-W4-S04-T02), task-queue-spec.md (P1-E2-W5-S05-T02)
**Scope:** User-visible workstation notification system. Local delivery only — no remote transmission.

---

## 1. Overview

The ClawDE notification system surfaces daemon lifecycle events, task state transitions, inbox arrivals, quota changes, permission prompts, and security warnings to the user via native OS notifications and an in-app history panel.

**Core invariants:**
- Local-only: no notification payload leaves the device under any circumstances.
- Privacy-first: notification content is never logged to persistent disk unless the user enables `debug_logging = true` in config.
- Critical events always bypass quiet hours.
- Suppressed notifications still appear in the in-app history panel — nothing is silently dropped.

---

## 2. Notification UX Matrix

The locked event list is 10 events (LEDGER §G). Each row defines the full delivery contract.

| # | Trigger Event | Severity | Title Copy | Body Copy | Action Button | Suppression Rule | Test State |
|---|---|---|---|---|---|---|---|
| 1 | `daemon-started` | info | "ClawDE Ready" | "Daemon started. Workspace {workspace_id} is active." | — | snooze/DND honored | pending |
| 2 | `daemon-crashed` | critical | "ClawDE Crashed" | "Daemon exited unexpectedly (exit {code}). Restart in progress." | "View Logs" | bypasses quiet hours always | pending |
| 3 | `daemon-stopped` | warning | "ClawDE Stopped" | "Daemon stopped. Sessions are paused. Restart with `clawde start`." | "Restart" | snooze/DND honored | pending |
| 4 | `inbox-message-received` | info | "New Inbox Message" | "Priority: {priority} · Type: {type} · From: {sender}" | "Open" | snooze/DND honored; customizable per message type | pending |
| 5 | `task-completed` | info | "Task Complete" | "{task_title} finished successfully." | "View" | snooze/DND honored | pending |
| 6 | `task-failed` | error | "Task Failed" | "{task_title} failed after {retry_count} attempt(s). Check logs." | "View Logs" | snooze/DND honored | pending |
| 7 | `task-blocked` | warning | "Task Blocked" | "{task_title} is waiting on a dependency or user action." | "Review" | snooze/DND honored | pending |
| 8 | `quota-paused` | warning | "Quota Paused" | "AI provider quota reached. Resume at {resume_time}." | "View Quota" | snooze/DND honored | pending |
| 9 | `permission-prompt-required` | critical | "Permission Required" | "{tool_name} is requesting {permission_type} access. Approve to continue." | "Approve" | bypasses quiet hours always | pending |
| 10 | `security-warning` | critical | "Security Alert" | "{warning_message}" | "Review" | bypasses quiet hours always | pending |

**Note:** If a future wave adds daemon events (e.g. P1-E2-W4-S04-T04 extension), this ticket must be re-opened to add corresponding rows to this table.

---

## 3. Severity Levels and Visual Treatment

Four severity levels with distinct rendering contracts:

### 3.1 `info`

- **Color:** sky-500 (`#0ea5e9`) banner border and icon tint
- **Position:** top-right corner overlay
- **Persistence:** auto-dismisses after **5 seconds** if no interaction
- **Quiet hours:** queued; delivered at quiet-hours-end in a digest bundle
- **DND:** queued until DND ends

### 3.2 `warning`

- **Color:** amber-500 (`#f59e0b`) banner border and icon tint
- **Position:** top-right corner overlay
- **Persistence:** persistent until user explicitly acknowledges (tap/click to dismiss or swipe)
- **Quiet hours:** queued; delivered at quiet-hours-end in a digest bundle
- **DND:** queued until DND ends

### 3.3 `error`

- **Color:** red-500 (`#ef4444`) banner border and icon tint
- **Position:** top-right corner overlay
- **Persistence:** persistent until acknowledged; **action button is mandatory** (no dismiss without taking action)
- **Quiet hours:** queued; delivered at quiet-hours-end (does NOT bypass quiet hours — only critical does)
- **DND:** queued until DND ends

### 3.4 `critical`

- **Color:** red-700 (`#b91c1c`) modal background with red-700 border
- **Position:** modal overlay — blocks UX until resolved
- **Persistence:** blocks all app interaction until the user takes the required action or explicitly dismisses with reason
- **Quiet hours:** **always bypasses** quiet hours without exception
- **DND:** **always bypasses** global DND without exception

**Background color (all severities):** gray-950 (`#030712`) — nSelf dark background standard.

---

## 4. Quiet Hours

### 4.1 Default Window

Default quiet hours: **22:00–08:00 local time** (UTC offset applied per system clock).

### 4.2 Configuration

Configurable in `~/.config/clawde/notifications.toml`:

```toml
[quiet_hours]
enabled = true
start = "22:00"      # 24-hour HH:MM local time
end = "08:00"        # 24-hour HH:MM local time; if end < start, range wraps midnight
timezone = "local"   # "local" or IANA tz string e.g. "America/New_York"
```

### 4.3 Quiet Hours Behavior Matrix

| Severity | During Quiet Hours |
|---|---|
| `info` | Queued in memory. Delivered at `end` time as digest bundle. |
| `warning` | Queued in memory. Delivered at `end` time as digest bundle. |
| `error` | Queued in memory. Delivered at `end` time as digest bundle. |
| `critical` | **Delivered immediately.** Bypasses quiet hours unconditionally. |

### 4.4 Digest Bundle

At quiet-hours-end, queued notifications are delivered as a single digest:

- Maximum 10 items shown individually.
- If more than 10 are queued: show the 10 most recent, append "… and N more notifications. Open history to review."
- Digest notification severity: the highest severity level of all queued items.
- Each digest item links to the in-app history panel for details.

---

## 5. Local-Only Privacy Posture

**Non-negotiable invariants:**

1. No notification payload (title, body, event data) is transmitted to any remote service at any time.
2. The notification subsystem makes **zero network calls**. There is no outbound request path from notification dispatch.
3. Notification content is **not written to persistent disk** unless `debug_logging = true` in `~/.config/clawde/notifications.toml`. When debug logging is enabled, entries are written to `~/.local/share/clawde/debug-notifications.log` with user-visible opt-in consent on first enable.
4. The in-app history panel stores entries in **clawd's local SQLite database** (ADR-001 storage boundary) — never in a shared or cloud-synced location.
5. OS notification content is governed by the OS notification subsystem's own data handling. ClawDE passes minimal metadata: only the rendered title and body strings, no session IDs, file paths, or sensitive tokens in notification text.

---

## 6. Platform Delivery Mechanisms

### 6.1 macOS

**Primary (macOS 12+):** `UNUserNotificationCenter` (UserNotifications framework)
- Required entitlement: `com.apple.security.app-sandbox.notifications` (in `clawde.entitlements`)
- Authorization: call `UNUserNotificationCenter.requestAuthorization(options: [.alert, .sound, .badge])` at first launch. Store the authorization result in SQLite; do not re-prompt if denied.
- Critical notifications: delivered via `UNNotificationInterruptionLevel.critical`. Requires `UNNotificationExtensionDefaultContentHidden` + separate entitlement request `com.apple.developer.usernotifications.critical-alerts`.

**Legacy (macOS <12):** `NSUserNotification` via `NSUserNotificationCenter`
- No entitlement required for non-sandboxed builds.
- Critical severity: displayed as modal in-app instead of OS critical alert (OS limitation).

**Fallback (OS permission denied on both paths):** Log the notification to in-app history panel only. Emit a one-time in-app banner: "OS notifications are disabled. Enable them in System Settings > Notifications > ClawDE."

### 6.2 Linux

**Primary:** `libnotify` / `notify-send` via D-Bus (`org.freedesktop.Notifications`)
- No special permissions required for desktop sessions.
- Urgency mapping: `info` → `NOTIFY_URGENCY_LOW`, `warning` → `NOTIFY_URGENCY_NORMAL`, `error` → `NOTIFY_URGENCY_NORMAL`, `critical` → `NOTIFY_URGENCY_CRITICAL`.
- Expiry: `info` → 5000ms, others → 0 (persistent until dismissed by user).

**Fallback (D-Bus unavailable, e.g. headless, missing libnotify-bin):** Log to in-app history panel only. Log a warning to daemon stdout: `WARN notify: D-Bus unavailable, falling back to in-app history`.

**Dependency check:** On startup, clawd checks for `notify-send` in `PATH`. If absent, logs once: `WARN notify: notify-send not found; install libnotify-bin for OS notifications`.

### 6.3 Windows

**Primary:** WinRT `ToastNotification` (Windows.UI.Notifications)
- Requires AUMID (Application User Model ID) registration. ClawDE registers its AUMID at install time via the NSIS/WiX installer writing a registry key: `HKCU\Software\Classes\AppUserModelId\com.nself.clawde`.
- Toast template: `ToastGeneric` with title, body, and optional action button.
- Critical severity: delivered as `ToastNotificationPriority::High` with `ToastNotification.SuppressPopup = false`.

**Fallback (AUMID not registered or COM error):** Log to in-app history panel only. Emit a one-time in-app banner: "Toast notifications require re-running the ClawDE installer to register the AUMID."

---

## 7. Suppression Rules

### 7.1 Per-Notification Snooze

Users may right-click any OS notification or long-press on mobile to access snooze options:

| Duration | Snooze Key |
|---|---|
| 1 hour | `snooze_1h` |
| 8 hours | `snooze_8h` |
| 24 hours | `snooze_24h` |
| Forever | `snooze_forever` |

Snooze state is stored in SQLite (`clawd_notification_suppression` table) keyed by `(trigger_event, snooze_key)`. Snoozed notifications still route to the in-app history panel with `suppressed = true` flag.

`critical` severity notifications **cannot be snoozed**. The snooze context menu is not offered for critical events.

### 7.2 Global Do Not Disturb (DND)

DND suspends all non-critical notifications until the duration expires or the user manually ends it.

**CLI command:**

```bash
clawde notifications pause            # DND until manually ended
clawde notifications pause 1h         # DND for 1 hour
clawde notifications pause 8h         # DND for 8 hours
clawde notifications pause 24h        # DND for 24 hours
clawde notifications resume           # end DND immediately
clawde notifications status           # show DND state and expiry
```

**DND behavior matrix:**

| Severity | During DND |
|---|---|
| `info` | Queued until DND ends; delivered as digest |
| `warning` | Queued until DND ends; delivered as digest |
| `error` | Queued until DND ends; delivered as digest |
| `critical` | **Delivered immediately.** DND does not apply. |

DND state is stored in `~/.config/clawde/notifications.toml` under `[dnd]` section. The daemon re-reads config on SIGHUP.

### 7.3 Suppression + History Guarantee

All suppressed or DND-queued notifications appear in the in-app notification history panel with their original timestamp and a `[Suppressed]` or `[Queued — DND]` tag. Nothing is ever silently dropped.

---

## 8. In-App Notification History Panel

### 8.1 Location

Accessible from the ClawDE sidebar: a dedicated "Notifications" icon (bell) opens the history panel as a slide-in drawer from the right.

### 8.2 Storage

- Stored in `clawd`'s local SQLite database (ADR-001), table `clawd_notification_history`.
- Maximum **500 entries**. When the limit is reached, the oldest entries are pruned (FIFO).
- Schema:

```sql
CREATE TABLE clawd_notification_history (
  id           TEXT    PRIMARY KEY,  -- UUID
  ts           TEXT    NOT NULL,     -- ISO 8601 UTC
  trigger_event TEXT   NOT NULL,     -- e.g. "daemon-crashed"
  severity     TEXT    NOT NULL,     -- info | warning | error | critical
  title        TEXT    NOT NULL,
  body         TEXT    NOT NULL,
  action_taken TEXT,                 -- null | "approved" | "dismissed" | "snoozed_1h" | ...
  suppressed   INTEGER NOT NULL DEFAULT 0,  -- 1 if DND/snooze applied
  workspace_id TEXT    NOT NULL
);
```

### 8.3 Panel UI Contract

Each entry in the panel shows:
- Timestamp (`ts`) — human-readable (e.g. "Today 14:32", "Yesterday 09:11")
- Severity badge with color per §3
- Title
- Body (truncated to 120 chars, expandable)
- Action taken (or "No action" if dismissed without action)
- Tag if suppressed: `[Suppressed]` or `[Queued]`

### 8.4 Filtering

The panel supports filtering by:
- **Severity:** info / warning / error / critical (multi-select)
- **Event type:** dropdown of all 10 trigger event types
- **Date range:** last 24h / last 7 days / last 30 days / all time

Filters are ephemeral (session-only, not persisted to config).

---

## 9. Integration Test Plan

The following tests verify end-to-end notification delivery. All tests are manual/integration-level in P1; automated harness is out of scope.

| # | Test | Steps | Expected |
|---|---|---|---|
| T1 | All 10 events fire notification | Trigger each event type via `clawde test notification <event>` | OS notification appears with correct title, body, and severity for each |
| T2 | DND queues info and warning | Enable DND (`clawde notifications pause`); trigger `daemon-started` (info) and `daemon-stopped` (warning) | No OS notification fires; events appear in history with `[Queued — DND]` tag |
| T3 | Critical bypasses DND | Enable DND; trigger `daemon-crashed` (critical) | OS critical notification fires immediately despite DND |
| T4 | Quiet hours queues non-critical | Set quiet hours to current time window; trigger `task-completed` (info) | Notification queued; at quiet-hours-end a digest OS notification fires listing queued items |
| T5 | Critical bypasses quiet hours | Set quiet hours to current time window; trigger `permission-prompt-required` (critical) | OS critical notification fires immediately despite quiet hours |
| T6 | Snooze suppresses and records | Trigger `task-blocked` (warning); snooze 1h | No further OS notification for 1h; history panel shows `[Suppressed]` entry |
| T7 | History panel shows 500 limit | Insert 501+ notification entries | Oldest entry pruned; panel shows 500 entries |
| T8 | OS deny fallback | Revoke OS notification permission; trigger any event | No OS notification; in-app history panel entry created; one-time in-app banner shown |
| T9 | DND CLI round-trip | `clawde notifications pause 1h`; `clawde notifications status` | Status shows DND active with expiry; `clawde notifications resume` clears it |
| T10 | Digest bundle capped at 10 | Queue 15 notifications during quiet hours | Digest delivers 10 items + "…and 5 more" message |
