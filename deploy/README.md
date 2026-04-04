# Urgentry Deployment Guide

Choose your deployment method based on your needs.

## Tiny Mode (single binary, SQLite)

For solo developers, small teams, and evaluation.

| Method | Setup Time | Cost | Persistence | HTTPS |
|---|---|---|---|---|
| [Direct binary](direct/) | 60 seconds | Free (your hardware) | Local disk | Via reverse proxy |
| [Docker single container](docker-tiny/) | 2 minutes | Free (your hardware) | Docker volume | Via reverse proxy |
| [Fly.io](fly/) | 5 minutes | Free tier / $5/mo | Persistent volume | Included |
| [Railway](railway/) | 5 minutes | Free trial / $5/mo | Persistent volume | Included |
| [Render](render/) | 5 minutes | Free tier / $7/mo | Persistent disk | Included |

### Quick Start — Direct Binary

```bash
curl -fsSL https://urgentry.dev/install.sh | sh
urgentry serve
# Open http://localhost:8080
```

### Quick Start — Docker

```bash
docker run -p 8080:8080 -v urgentry-data:/data ghcr.io/ehmo/urgentry:latest serve
```

### Quick Start — Fly.io

```bash
fly launch --image ghcr.io/ehmo/urgentry:latest
fly volumes create urgentry_data --size 1
fly deploy
```

---

## Self-Hosted Mode (multi-role, Postgres)

For production teams needing horizontal scaling and durability.

| Method | Setup Time | Requirements |
|---|---|---|
| [Docker Compose](compose/) | 10 minutes | Docker, Docker Compose |
| [Helm / Kubernetes](helm/) | 15 minutes | Kubernetes cluster, Helm |
| [Direct binary (multi-role)](direct/) | 20 minutes | Postgres, NATS, S3, Valkey |

### Quick Start — Docker Compose

```bash
cd apps/urgentry/deploy/compose
cp .env.example .env
# Edit .env with your passwords
docker compose up -d
```

### Quick Start — Helm

```bash
helm install urgentry deploy/helm/urgentry/ \
  --set postgresql.password=changeme \
  --set bootstrap.password=changeme
```

---

## Architecture

```
Tiny Mode                              Self-Hosted Mode
──────────                             ─────────────────

┌──────────────────┐                   ┌─────────────┐  ┌──────────────┐
│  urgentry serve  │                   │   API (:80)  │  │ Ingest (:81) │
│  (all roles)     │                   └──────┬───────┘  └──────┬───────┘
│  SQLite + blobs  │                          │                 │
└──────────────────┘                   ┌──────┴─────────────────┴──────┐
     31 MB binary                      │  Postgres · NATS · S3 · Valkey│
     128 MB RAM                        └──────┬─────────────────┬──────┘
     1 container                       ┌──────┴───────┐  ┌──────┴───────┐
                                       │ Worker (:82) │  │ Sched (:83)  │
                                       └──────────────┘  └──────────────┘
```

## Cloudflare / Edge

Urgentry's Tiny mode is a single Go binary with SQLite — it requires a persistent
filesystem and can't run in serverless/edge environments like Cloudflare Workers,
Vercel Functions, or AWS Lambda.

For edge deployment, use **Fly.io** which provides persistent volumes on edge nodes,
or run the binary on any VPS (including Cloudflare's compute offerings, Hetzner,
DigitalOcean, etc.).
