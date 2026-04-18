# Upgrading ClawDE

## Automatic Updates

ClawDE updates automatically when idle (no active sessions).

**macOS/Linux (Homebrew):** Updated via `brew upgrade clawd` or automatically via daemon self-update.

**Windows:** Updated via the Windows installer or daemon self-update.

**Mobile:** Updated via App Store or Google Play.

## Manual Update

To force an immediate update:

```bash
clawd stop
brew upgrade clawd   # macOS/Linux via Homebrew
clawd start
```

Or download the latest binary from [GitHub Releases](https://github.com/nself-org/clawde/releases).

## Database Migrations

The daemon runs SQLite migrations automatically on startup. Migrations are:
- **Additive only**: New columns, new tables — never destructive
- **Idempotent**: Safe to run multiple times
- **Backward compatible**: v0.1.0 databases upgrade cleanly to v0.2.0+

You do not need to do anything. The daemon handles migrations.

### Auto-Backup Before Migrations

Starting with v0.2.1, the daemon automatically backs up the database before running migrations:

- **Backup location**: `~/.clawd/backups/clawd-{version}.db`
- One backup per version — re-running the same version does not overwrite the backup
- On fresh install (no existing database), no backup is created

To find your backups:

```bash
ls ~/.clawd/backups/
# clawd-0.2.1.db  clawd-0.2.0.db  …
```

## Recovery Mode

If a migration failure prevents the daemon from starting normally, start in recovery mode:

```bash
clawd --no-migrate
```

In recovery mode:
- Database migrations are **skipped**
- `daemon.status` returns `recoveryMode: true`
- The Flutter desktop app shows a recovery overlay with retry and rollback options
- Your existing data is safe — nothing is modified

### Recovery Steps

1. Start in recovery mode: `clawd --no-migrate`
2. Connect with the desktop app — it shows the recovery overlay
3. Options from the recovery overlay:
   - **Retry migration**: Restart normally (`clawd`) after the issue is resolved
   - **Rollback**: Restore from a pre-migration backup (see below)

### Manual Rollback via Backup

To restore a previous database after a failed migration:

```bash
clawd stop

# Find available backups
ls ~/.clawd/backups/

# Restore the backup you want (replace version as needed)
cp ~/.clawd/backups/clawd-0.2.0.db ~/.clawd/clawd.db

# Downgrade the binary (if needed)
brew install clawd@0.2.0   # or download from GitHub Releases

clawd start
```

## Rollback (Binary Only)

If you need to downgrade to a previous version without a migration issue:

1. Stop the daemon: `clawd stop`
2. Install the previous binary
3. Start the daemon: `clawd start`

The database is backward compatible — downgrading does not corrupt data. New columns added by the newer version are simply ignored by the older version.

## Config File Migration

If your `clawd.toml` has unrecognized keys after an upgrade, the daemon will log a warning but continue. Old keys are ignored. Check [[Configuration]] for the current key reference.

## Version History

See [[Changelog]] for version history and what changed in each release.

## Getting Help

If you encounter issues after upgrading, check [[Troubleshooting]] first, then open an issue on [GitHub](https://github.com/nself-org/clawde/issues).
