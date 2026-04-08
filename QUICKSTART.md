# Urgentry Quick Start

This is the shortest path to a working Tiny-mode node.

## Prerequisites

- Go 1.26+
- a free local port, usually `8080`

## Build

```bash
make build
```

This writes the binary at `./urgentry`.

## Start

```bash
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

On first boot Urgentry creates:
- a default organization
- a default project
- a bootstrap owner account
- a bootstrap PAT
- a default public ingest key

## Sign in

Open `http://localhost:8080/login/` and use the bootstrap credentials from the startup log.

## Send a first event

```bash
curl -X POST http://localhost:8080/api/default-project/store/ \
  -H "Content-Type: application/json" \
  -H "X-Sentry-Auth: Sentry sentry_key=<public_key>,sentry_version=7" \
  -d '{
    "event_id": "abcdef01234567890abcdef012345678",
    "message": "Something went wrong",
    "level": "error",
    "platform": "python"
  }'
```

## Next docs

- [docs/tiny/README.md](docs/tiny/README.md)
- [docs/self-hosted/README.md](docs/self-hosted/README.md)
- [deploy/README.md](deploy/README.md)
- [docs/reference/sentry-synthetic-baseline.md](docs/reference/sentry-synthetic-baseline.md)
