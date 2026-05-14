# License State Machine

This page documents the full lifecycle of a ClawDE+ license: every state, every transition, and the side effects each transition produces in the desktop app, the daemon, and the billing backend.

ClawDE+ is the paid subscription that activates server-sync, mobile companion access, and team features. Validation runs against `api.clawde.io/daemon/verify`. The desktop app continues to function in free mode whenever the license is not active.

Billing is processed through Stripe. Subscription mutations (upgrades, downgrades, cancellations, refunds, disputes) emit webhooks that drive the state machine on the backend; the desktop app polls `daemon/verify` once per launch and every 24h while running.

## States

| State | Description | Plugins active? | Desktop behavior |
| --- | --- | --- | --- |
| `trial` | Free 14-day evaluation started on first install | Yes (full ClawDE+ feature set) | Status bar shows days remaining |
| `active-monthly` | Paid monthly subscription, current period in good standing | Yes | No banner |
| `active-annual` | Paid annual subscription, current period in good standing | Yes | No banner |
| `past-due` | Payment failed, inside the 14-day grace window | Yes | Yellow banner, 4 Stripe retries scheduled |
| `cancellation-pending` | User canceled; subscription active until period end | Yes | Banner shows period-end date |
| `revoked` | Subscription terminated (cancel + period end, grace expired, refund, or dispute lost) | No | Red banner; plugins dormant; CLI warns on next `clawde build` |
| `trial-resumed` | Re-installed on a new machine where remaining trial days exist | Yes | Status bar shows days remaining |

Trial eligibility is keyed by `device_hash + account_email`. Each account gets exactly one trial across all machines; reinstalls within the original 14 days resume `trial-resumed`. Trials that expired more than 30 days ago are not re-issued.

## Transitions

### Purchase

- `trial` -> `active-monthly`: user clicks **Buy monthly** on `nself.org/clawde/plus`. Stripe charges the card. Webhook `customer.subscription.created` lands. Daemon receives a new license key within 60 seconds.
- `trial` -> `active-annual`: same flow with the annual SKU. The annual price is $9.99/yr (~16% discount vs monthly).

### Upgrade (monthly to annual)

- `active-monthly` -> `active-annual`: user clicks **Upgrade to annual** in **Settings -> ClawDE+** or at `base.clawde.io/billing`. Stripe prorates: the unused portion of the current month becomes a credit, and the annual price is charged immediately minus that credit. The new period starts on the day of upgrade.

### Downgrade (annual to monthly)

- `active-annual` -> `active-monthly`: user clicks **Switch to monthly**. The change is scheduled, not immediate. The annual term completes (no refund of unused months). On renewal day the subscription switches to monthly billing.

### Cancellation

- `active-monthly` | `active-annual` -> `cancellation-pending`: user clicks **Cancel subscription**. Stripe marks `cancel_at_period_end = true`. No charge will occur at the next renewal.
- `cancellation-pending` -> `active-*` (uncancel): user clicks **Resume subscription** before the period ends. Stripe clears `cancel_at_period_end`. State returns to whichever active tier was running.
- `cancellation-pending` -> `revoked`: period end is reached. Daemon receives a revocation event from `daemon/verify` within 5 minutes. Plugins go dormant.

### Payment failure

- `active-*` -> `past-due`: Stripe charge fails. State flips on `invoice.payment_failed`. Stripe runs 4 retry attempts over 14 days (configurable per Stripe smart retries).
- `past-due` -> `active-*`: any retry succeeds (`invoice.payment_succeeded`). Banner clears.
- `past-due` -> `revoked`: 14-day grace expires with no successful retry. Stripe emits `customer.subscription.deleted`. Daemon receives revocation; plugins dormant; CLI warn on next build.

### Refund and dispute

- `active-*` | `cancellation-pending` -> `revoked` (refund): a full refund processed in Stripe triggers `charge.refunded` with full amount. Subscription is canceled immediately and license invalidated within 5 minutes. Partial refunds do not change state.
- `active-*` -> `revoked` (dispute lost): chargeback filed and lost. Stripe emits `charge.dispute.closed` with status `lost`. License is invalidated immediately. The account is flagged; re-subscription requires manual support review at `support@clawde.io`.
- `active-*` after dispute won: state unchanged.

### Manual revocation

- Any state -> `revoked`: support invalidates the key via the admin tool at `base.clawde.io/admin/licenses`. Used for ToS violations or fraud. Daemon picks up the new state within 5 minutes.

### Trial resumption

- `(no state)` -> `trial-resumed`: ClawDE installs on a new machine for an account whose trial is still within 14 days of first use. The remaining days carry over.

## Refund flow

1. User emails `support@clawde.io` or self-serves at `base.clawde.io/billing` within 14 days of purchase.
2. Support issues the refund through the Stripe dashboard. Stripe processes within 5-10 business days back to the original payment method.
3. The webhook `charge.refunded` (full-amount) triggers immediate subscription cancellation and license invalidation.
4. The user receives a confirmation email. Desktop app drops to free mode on next launch or within 24h, whichever is sooner.

Partial refunds (for example, prorated credit on a downgrade) do not invalidate the license.

## Dispute handling

1. Stripe notifies the team via `charge.dispute.created`. The subscription remains active during the dispute window.
2. Support submits evidence within 7 days through the Stripe dashboard.
3. `charge.dispute.closed` arrives with status `won` or `lost`.
   - `won`: state unchanged, no action.
   - `lost`: license revoked immediately; account flagged; re-subscription requires support review.

## License-key invalidation timing

| Trigger | Time to plugin dormancy |
| --- | --- |
| Cancellation period end | <= 5 minutes after period-end timestamp |
| Grace-period expiry (past-due) | <= 5 minutes after Stripe emits `customer.subscription.deleted` |
| Refund | <= 5 minutes after `charge.refunded` (full amount) |
| Dispute lost | <= 5 minutes after `charge.dispute.closed` status=lost |
| Manual revocation | <= 5 minutes after admin action |
| Desktop offline | Cached license honored for up to 7 days; on day 8 the daemon forces a re-verify and falls back to free mode if it cannot reach `daemon/verify` |

The daemon caches the last successful verify response with a 7-day TTL. This is the same fail-open posture used elsewhere in the nSelf ecosystem (see `memory/decisions.md` license fail-mode entry).

## Side effects per transition

| Transition | Daemon | Desktop app | CLI |
| --- | --- | --- | --- |
| Enter `active-*` | Receives license key, caches it | Hides upsell, unlocks Settings | Plugins install via `nself plugin install` |
| Enter `past-due` | Cached license still valid | Yellow banner with retry date | No change |
| Enter `cancellation-pending` | Cached license still valid | Banner shows period-end date | No change |
| Enter `revoked` | Drops license key, marks plugins dormant | Red banner, free-mode features only | `nself build` prints warning, plugins skipped |
| Enter `trial-resumed` | Receives trial token | Days-remaining badge | Same as `active-*` |

## Related

- [ClawDE+](ClawDE-Plus.md) - feature overview and activation
- [Teams Billing](Features/TeamsBilling.md) - per-seat team subscriptions
- [Daemon Reference](Daemon-Reference.md) - `daemon/verify` endpoint contract
