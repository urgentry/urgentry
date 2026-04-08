# Urgentry Documentation

Pick your deployment mode:

## [Tiny Mode](tiny/) — Start here

One binary, SQLite, zero infrastructure. Run on a laptop or a single server.

**Best for:** Evaluation, small teams, personal projects, local development.

```bash
make build && ./urgentry serve --role=all
```

## [Serious Self-Hosted](self-hosted/) — Production multi-node

Split roles on PostgreSQL, MinIO, Valkey, and NATS. Docker Compose or Kubernetes.

**Best for:** Teams that need shared infrastructure, HA, and operational depth.

```bash
cd deploy/compose && docker compose up
```

## Other documentation

| Directory | Contents |
|-----------|----------|
| [architecture/](architecture/) | ADRs, design docs, roadmaps, internal planning |
| [reference/](reference/) | Schema specs, compatibility matrices, toolchain matrix |
| [hosted/](hosted/) | Cloud/hosted service planning (internal) |
