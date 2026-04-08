# Serious Self-Hosted Maintenance Mode

Maintenance mode is the install-wide write freeze for serious self-hosted Urgentry.

When maintenance mode is on:

- reads stay online
- `/healthz`, `/readyz`, `/metrics`, and profiling stay reachable
- login and logout still work
- ingest and mutating API or web actions return `503 Service Unavailable`
- workers and schedulers can keep draining backlog until the install is quiet enough for operator work

Use it before:

- schema upgrades
- backup restore
- rollback
- secret rotation that requires role restarts
- backfill or rebuild actions where new writes would invalidate the operator window

## Commands

```bash
cd .
go run ./cmd/urgentry self-hosted maintenance-status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
go run ./cmd/urgentry self-hosted enter-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --reason "upgrade window"
go run ./cmd/urgentry self-hosted leave-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
```

Compose installs can use the wrapper:

```bash
cd .
bash deploy/compose/ops.sh maintenance-status
bash deploy/compose/ops.sh enter-maintenance "upgrade window"
bash deploy/compose/ops.sh leave-maintenance
```

Every maintenance transition now writes an install-ledger entry. Use `--actor` and `--source` when you need the recorded operator identity to be explicit instead of relying on the shell defaults.

## Recommended drain workflow

1. Enter maintenance mode with a clear reason.
2. Confirm new writes fail closed with `503` on ingest and mutating API routes.
3. Watch `/ops/` until queue backlog and operator activity are quiet enough for the planned action.
4. Stop or restart roles only after the drain is complete.
5. Run the upgrade, restore, rollback, or rotation step.
6. Leave maintenance mode.
7. Rerun `preflight`, `status`, and smoke validation.

## Surfaces

- the install state is persisted in the control plane and survives restarts
- `/ops/` shows the current maintenance state, reason, and age
- `/ops/` also shows the install ledger with maintenance action metadata
- `maintenance-status` prints the same state together with the drain checklist

## Validation

- [maintenance_test.go](../../internal/selfhostedops/maintenance_test.go) covers the control-plane workflow
- [maintenance_test.go](../../internal/middleware/maintenance_test.go) covers request blocking rules
- [server_test.go](../../internal/http/server_test.go) covers live HTTP behavior
