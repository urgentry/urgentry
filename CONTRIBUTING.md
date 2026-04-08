# Contributing to Urgentry

Thanks for taking a look at Urgentry.

This repo moves fast. Small, focused changes land better than wide speculative refactors. If you want to help, start with an issue, keep the scope tight, and leave the docs in better shape than you found them.

## Before you open a pull request

1. Read [README.md](README.md) and [QUICKSTART.md](QUICKSTART.md).
2. Read [AGENTS.md](AGENTS.md) for repo rules.
3. Read [AGENTS.md](AGENTS.md) if you touch the app.
4. Check existing issues before you start new work.
5. If you are reporting design-partner friction instead of sending a patch, use the design-partner feedback issue form.
6. Read [MAINTAINERS.md](MAINTAINERS.md) if you need to understand the current public support boundary.

## Development workflow

Run commands from the repo root unless a section says otherwise.

### Core app loop (fast local iteration)

```bash
cd .
make build
make test       # fast local loop
make lint
```

### Pre-merge validation

`make test-merge` is the canonical merge-safe command. Run it before every pull request:

```bash
cd .
make test-merge  # canonical merge-safe command — run before every merge
make test-compat # when protocol or SDK behavior changes
make test-race   # when concurrency changes
```

### Performance-sensitive work

If you change a hot path, capture the numbers:

```bash
cd .
make bench
make profile-bench
bash ../../eval/dimensions/performance/run.sh
```

## Pull request rules

- Keep each pull request about one thing.
- Add or update tests for behavior changes.
- Update docs in the same change when commands, behavior, packaging, or architecture changed.
- Use non-interactive Git commands.
- Keep commits scoped so maintainers can roll back a single task.
- Automated dependency pull requests are expected. Keep them narrow, run the smallest relevant validation, and note any skipped checks in the PR body.

## Clean-room rule

The repo contains upstream reference material under `research/reference/`.

That material is for scope study and compatibility pressure only. Do not copy implementation into Urgentry from those references.

## Documentation rule

If your change affects users, operators, or contributors, update the docs with the code. That includes:

- [README.md](README.md)
- [QUICKSTART.md](QUICKSTART.md)
- relevant files in [docs/](docs)
- [AGENTS.md](AGENTS.md) when repo workflow changes
- [AGENTS.md](AGENTS.md) when app workflow changes

## Style

Write direct code. Prefer clear data flow over clever abstraction.

For prose, keep it plain. Say what changed, why it changed, and how someone can verify it.
