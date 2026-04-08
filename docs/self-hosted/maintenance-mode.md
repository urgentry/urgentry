# Maintenance Mode

Maintenance mode is the install-wide write freeze for self-hosted Urgentry.

Use it for upgrades, recovery work, or operator interventions that should stop new writes temporarily.

## Binary commands

```bash
./urgentry self-hosted maintenance-status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
./urgentry self-hosted enter-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --reason "upgrade window"
./urgentry self-hosted leave-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
```

## Compose wrapper

```bash
bash deploy/compose/ops.sh maintenance-status
bash deploy/compose/ops.sh enter-maintenance "upgrade window"
bash deploy/compose/ops.sh leave-maintenance
```

## Validation

After leaving maintenance mode, rerun:

```bash
bash deploy/compose/smoke.sh up
make selfhosted-sentry-baseline
```
