# Urgentry Tiny Mode on Railway

Deploy Urgentry to Railway with zero config. Persistent volume for SQLite.

## Deploy

1. Fork [github.com/ehmo/gentry](https://github.com/ehmo/gentry)
2. Go to [railway.app](https://railway.app) → New Project → Deploy from GitHub
3. Select your fork, set root directory to repo root
4. Add a persistent volume mounted at `/data`
5. Deploy

## Environment Variables

Set in Railway dashboard:

```
URGENTRY_DATA_DIR=/data
URGENTRY_HTTP_ADDR=:8080
URGENTRY_BOOTSTRAP_EMAIL=admin@example.com
URGENTRY_BOOTSTRAP_PASSWORD=your-password
```

## Cost

- **Trial**: 500 hours free, 512MB RAM
- **Hobby ($5/mo)**: persistent volume, custom domains
