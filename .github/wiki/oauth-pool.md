# Multi-Account OAuth Pool

ClawDE supports connecting multiple OAuth accounts per provider (Google, GitHub, Anthropic). This is managed via the OAuth Accounts screen in the desktop app.

## Free vs ClawDE bundle

| Tier | Accounts per provider |
|---|---|
| Free | 1 |
| ClawDE bundle ($0.99/mo) | Unlimited |

Attempting to add a second account on the free tier returns a `license_required` error surfaced inline in the UI.

## Supported providers

| Provider | Use case |
|---|---|
| Google | Google Cloud, Workspace APIs |
| GitHub | Repository access, Copilot-style context |
| Anthropic | Claude API (alternative to `ANTHROPIC_API_KEY`) |

## Adding an account

1. Open the **OAuth Accounts** screen (key icon in the nav rail).
2. Select a provider from the dropdown.
3. Click **Connect** — a browser window opens to complete the OAuth flow.
4. On success the account appears in the list immediately.

## Removing an account

Click the trash icon on any account row. The account is removed from the daemon immediately.

## AgentChat integration

The model selector in AgentChat shows which OAuth account will be used for each provider. You can override the active account via the dropdown before sending a message.

## Implementation

- Screen: `clawde/apps/desktop/src/components/OAuthPoolScreen.tsx`
- Validation: `clawde/apps/desktop/src/lib/validation/schemas.ts` (`oauthSetupSchema`)
- Daemon API: `listOAuthAccounts`, `addOAuthAccount`, `removeOAuthAccount` in `tauriApi.ts`
- Bundle license gate: enforced by daemon — `license_required` error if N>1 on free tier
- SPORT: T-P3-E5-W1-S2-T02

## Troubleshooting

**OAuth popup doesn't open:** Ensure your default browser is set and the daemon is running (`nself start`).

**Token expired:** Remove and re-add the account. The daemon does not auto-refresh OAuth tokens — re-auth is required.

**Bundle required message:** Run `nself license set <your-key>` to activate the ClawDE bundle. Purchase at [clawde.nself.org](https://clawde.nself.org).
