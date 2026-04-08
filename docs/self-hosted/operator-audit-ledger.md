# Operator Audit Ledger

The operator audit ledger is the install-wide record of serious self-hosted actions. It gives operators one place to review operational changes across backup, restore, upgrade, rollback, maintenance, backfill, reprocess, and secret-related workflows.

## Purpose

- Keep a durable trail for high-impact install operations.
- Make support, recovery, and change review visible from the ops surface.
- Provide a shared history across control-plane and telemetry-plane actions.

## Recorded Actions

The ledger records entries for actions such as:

- backup capture
- restore apply and verify
- upgrade apply
- rollback planning and execution
- maintenance mode entry and exit
- backfill create and cancel
- debug-file reprocess
- secret rotation and other operator-driven lifecycle changes

Each entry should include the action name, status, actor, source, timestamps, and any useful metadata for later review.

## CLI and Script Touchpoints

Entries are emitted by the shipped operator-facing entrypoints that perform or wrap install-wide actions:

- `urgentry self-hosted record-action`
- `urgentry self-hosted enter-maintenance`
- `urgentry self-hosted leave-maintenance`
- org backfill and native reprocess APIs that now write install-ledger rows for native reprocess and telemetry rebuild actions

The shipped Compose helper scripts call the same recording path when they drive these flows:

- `deploy/compose/ops.sh`
- `deploy/compose/backup.sh`
- `deploy/compose/restore.sh`
- `deploy/compose/upgrade.sh`

## Inspecting the Ledger

The ops page exposes the most recent install-wide entries in the new install ledger section. Use it to confirm:

- which action ran
- who or what triggered it
- whether it completed or failed
- when it happened
- which install-scoped metadata was attached as raw JSON

For deeper inspection, use the underlying operator audit store in the control plane and compare the page view with the recorded action history.

For manual actions that are not yet wrapped by a shipped script, record them directly:

```bash
cd .
go run ./cmd/urgentry self-hosted record-action \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" \
  --action secret.rotate \
  --detail "rotated metrics token"
```

## Validation Expectations

When changing the ledger or a workflow that emits entries:

- verify the command or script still completes its primary task
- verify a ledger entry is written for the action
- verify the ops page surfaces the new entry
- run the relevant Go tests plus `make test` (fast local loop) and `make lint`; run `make test-merge` (canonical merge-safe command) before a PR merge
- run `git diff --check` before closing the task
