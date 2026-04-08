# Urgentry

Urgentry is a self-hosted error and telemetry system written in Go.

This repository ships the public Urgentry product surface:

- **Tiny mode** for single-node installs
- **Self-hosted mode** for split-role PostgreSQL-backed deployments

## Start here

### Tiny mode

Tiny mode is the fastest way to evaluate Urgentry.

```bash
make build
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

Then open `http://localhost:8080/login/`.

Use:
- [QUICKSTART.md](QUICKSTART.md)
- [docs/tiny/README.md](docs/tiny/README.md)
- [deploy/README.md](deploy/README.md)

### Self-hosted mode

Self-hosted mode runs split `api`, `ingest`, `worker`, and `scheduler` roles on PostgreSQL, MinIO, Valkey, and NATS.

Use:
- [docs/self-hosted/README.md](docs/self-hosted/README.md)
- [docs/self-hosted/deployment-guide.md](docs/self-hosted/deployment-guide.md)
- [deploy/README.md](deploy/README.md)

## Common commands

```bash
make build
make tiny-smoke
make test
make lint
make test-merge
make tiny-launch-gate
make tiny-sentry-baseline
make selfhosted-sentry-baseline
make release VERSION=v0.1.0
```

## Support and security

- [SUPPORT.md](SUPPORT.md)
- [SECURITY.md](SECURITY.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
