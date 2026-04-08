# Urgentry Toolchain and Runtime Matrix

Canonical reference for supported toolchains, runtimes, and CI tool versions.

## Go Toolchain

| Component | Minimum | CI | Notes |
|-----------|---------|-----|-------|
| Go | 1.26.0 | 1.26.1 | Set in `go.mod`, CI workflows, and the Docker builder |
| golangci-lint | v1.64.5 | v1.64.5 | Used via `make lint` |
| govulncheck | v1.1.4 | v1.1.4 | Used via `make vulncheck` |

## SDK Runtimes (Compatibility Matrix)

Required runtimes must be available in CI. Skipping a required runtime fails the matrix.

| Runtime | Required | Version | Notes |
|---------|----------|---------|-------|
| Node.js | **yes** | 20+ | js-node SDK runner |
| Python | **yes** | 3.11+ | python SDK runner |
| Go | **yes** | 1.26+ | go SDK runner |
| Java | no | 21+ | Extended matrix only |
| .NET | no | 8.0+ | Extended matrix only |
| Ruby | no | 3.3+ | Extended matrix only |

## Infrastructure (Self-Hosted)

| Component | Tiny Mode | Serious Mode | Version |
|-----------|-----------|-------------|---------|
| PostgreSQL | n/a | **required** | 17+ |
| SQLite | embedded | n/a | via Go stdlib |
| MinIO | optional | **required** | latest |
| NATS | n/a | **required** | 2.11+ |
| Valkey | n/a | **required** | 8+ |

## CI Tool Versions

| Tool | Version | Pinned? | Notes |
|------|---------|---------|-------|
| actions/checkout | v4 | yes | All workflows |
| actions/setup-go | v5 | yes | All workflows |
| actions/upload-artifact | v4 | yes | Release workflow |

## Updating This Matrix

When changing toolchain requirements:
1. Update `go.mod` for Go version
2. Update CI workflows for action versions
3. Update this document
4. Run `make test-merge` to validate
