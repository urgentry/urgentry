# Urgentry Tiny Mode on Fly.io

Deploy Urgentry to Fly.io's global edge network. One command, persistent SQLite storage, HTTPS included.

## Deploy

```bash
cd apps/urgentry

# First time
fly launch --copy-config --name my-urgentry
fly volumes create urgentry_data --size 1 --region iad

# Deploy
fly deploy
```

## Access

```
https://my-urgentry.fly.dev
```

Check logs for bootstrap credentials:

```bash
fly logs
```

## Configure

Set secrets (not visible in config):

```bash
fly secrets set URGENTRY_BOOTSTRAP_EMAIL=admin@example.com
fly secrets set URGENTRY_BOOTSTRAP_PASSWORD=your-password
fly secrets set URGENTRY_BOOTSTRAP_PAT=gpat_your_token
```

## Point your SDKs

```python
sentry_sdk.init(dsn="https://key@my-urgentry.fly.dev/1")
```

## Cost

- **Free tier**: shared-cpu-1x, 256MB RAM, 1GB volume — good for evaluation
- **$5/mo**: shared-cpu-1x, 512MB RAM, persistent volume — good for small teams

## Backup

```bash
fly ssh console -C "cp -r /data /tmp/backup"
fly ssh sftp get /tmp/backup ./urgentry-backup
```

## Scale

Tiny mode is single-node. For multi-region or high availability, use the self-hosted Docker Compose or Helm deployment instead.
