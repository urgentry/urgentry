# Getting started with urgentry

This is the shortest path to a working Tiny-mode node.

## Before you start

- Go 1.26+
- a free local port, usually `8080`

## Build the binary

```bash
make build
```

This writes the binary at `./urgentry`.

## Start the server

```bash
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

On first boot, urgentry creates:
- a default organization
- a default project
- a bootstrap owner account
- a bootstrap PAT
- a default public ingest key

## Sign in

Open `http://localhost:8080/login/` and use the bootstrap credentials from `bootstrap-credentials.txt` in the data directory.

Create additional projects from `/manage/projects/`. The form creates the project under an existing team, provisions a default public key, selects the new project for this browser session, and sends you to that project's Keys & DSN page. Use the topbar project selector to switch project-scoped analytics pages between projects.

## Send a first event

Use the public project key from startup logs. The default internal project ID is `default-project`; generated SDK DSNs use a numeric project ID alias so Sentry SDKs accept them unchanged. You can copy the SDK DSN from `/settings/project/default/keys/` or call the keys API. Manual curl requests may use the internal project ID.

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

## Next steps

- [Tiny mode guide](docs/tiny/README.md)
- [Self-hosted mode](docs/self-hosted/README.md)
- [Download and install options](deploy/README.md)
