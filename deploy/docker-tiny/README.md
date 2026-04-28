# Urgentry Tiny Mode — Docker

Single-container deployment with SQLite storage. Zero external dependencies.

## Quick Start

```bash
docker compose up -d
```

Open http://localhost:8080. The logs point to `bootstrap-credentials.txt` in the data directory:

```bash
docker compose logs urgentry
```

## What's Included

- Error tracking with full Sentry SDK compatibility
- Issue grouping, assignment, merge/unmerge
- Discover query builder
- Session replay, CPU profiling
- Alerts and cron monitors
- All data in a single SQLite file at `/data`

## Configuration

Set environment variables in `docker-compose.yml`:

| Variable | Default | Description |
|---|---|---|
| `URGENTRY_DATA_DIR` | `/data` | SQLite data directory |
| `URGENTRY_HTTP_ADDR` | `:8080` | Listen address |
| `URGENTRY_BOOTSTRAP_EMAIL` | auto | Admin email |
| `URGENTRY_BOOTSTRAP_PASSWORD` | auto | Admin password |
| `URGENTRY_BOOTSTRAP_PAT` | auto | Personal access token |
| `URGENTRY_INGEST_RATE_LIMIT` | `60` | Max events/minute/key |

## Backup

```bash
# Stop, copy the data volume, start
docker compose stop
docker cp $(docker compose ps -q urgentry):/data ./backup
docker compose start
```

## Upgrade

```bash
docker compose pull
docker compose up -d
```
