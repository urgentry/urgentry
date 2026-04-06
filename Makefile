.PHONY: build build-tiny build-debug check-links check-tidy test test-fast-with-timings test-compat test-merge test-race lint lint-full bench bench-budget selfhosted-bench selfhosted-eval run tiny-smoke tiny-launch-gate clean fuzz docker release tidy vulncheck profile profile-trace profile-bench synthetic-registry synthetic-registry-check synthetic-generate synthetic-audit synthetic-check

# Default binary name
BINARY := urgentry
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || cat ../../VERSION 2>/dev/null || echo dev)
PROFILE_DIR ?= profiles/latest
PROFILE_ITERATIONS ?= 200
PROFILE_GOMAXPROCS ?= 1
PROFILE_SCENARIOS := store-basic-error store-python-full envelope-single-error otlp-trace
PROFILE_RUN := GOMAXPROCS=$(PROFILE_GOMAXPROCS) go run ./cmd/urgentry profile
PROFILE_RESET := rm -rf $(PROFILE_DIR) && mkdir -p $(PROFILE_DIR)
FAST_TEST_PACKAGES = $$(go list ./... | grep -v '^urgentry/internal/compat$$')
BUILD_SCRIPT := bash ./scripts/build-urgentry.sh
GOLANGCI_LINT_VERSION := v1.64.5
GOVULNCHECK_VERSION := v1.1.4

define PROFILE_RUN_SCENARIOS
	for scenario in $(PROFILE_SCENARIOS); do \
		$(PROFILE_RUN) --scenario=$$scenario --kind=$(1) --iterations=$(PROFILE_ITERATIONS) --gomaxprocs=$(PROFILE_GOMAXPROCS) --out-dir=$(PROFILE_DIR)/$(1)/$$scenario; \
	done
endef

## build: Build the default optimized Tiny-mode binary
build:
	VERSION=$(VERSION) $(BUILD_SCRIPT) --output $(BINARY)

## build-tiny: Backward-compatible alias for the optimized default Tiny-mode build
build-tiny:
	VERSION=$(VERSION) $(BUILD_SCRIPT) --output $(BINARY)

## build-debug: Build an unstripped local debug binary
build-debug:
	go build -ldflags="-X urgentry/internal/config.Version=$(VERSION) -X urgentry/internal/config.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown) -X urgentry/internal/config.BuildDate=$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')" -o $(BINARY) ./cmd/urgentry

## run: Build and run with all roles
run: build
	./$(BINARY) serve --role=all

## tiny-smoke: Boot Tiny mode and verify login + bootstrap API flow
tiny-smoke:
	bash ./scripts/tiny-smoke.sh

## tiny-launch-gate: Run the Tiny public-launch hardening gate
tiny-launch-gate:
	bash ./scripts/tiny-launch-gate.sh

## test: Run the fast local test suite (excludes internal/compat, skips Postgres)
test:
	URGENTRY_SKIP_POSTGRES_TESTS=1 go test $(FAST_TEST_PACKAGES) -count=1

## check-links: Verify repo Markdown links are merge-safe
check-links:
	bash ../../scripts/check-markdown-links.sh

## check-tidy: Verify repo Go modules are tidy-clean
check-tidy:
	bash ../../scripts/check-go-mod-tidy.sh

## test-fast-with-timings: Run the fast local suite once, print slowest packages, and write test-results/fast-suite.{jsonl,json}
test-fast-with-timings:
	bash ./scripts/test-fast-with-timings.sh

## test-compat: Run the compatibility harness and live SDK matrix
test-compat:
	go test -tags=integration ./internal/compat -count=1

## test-merge: Run the per-merge repo health gate
test-merge: check-links check-tidy test-fast-with-timings test-cover test-compat vulncheck

## test-race: Run the fast local test suite with race detector (parallelism helps wall time)
test-race:
	go test -race -p 4 $(FAST_TEST_PACKAGES) -count=1

## test-race-integration: Race-enabled integration lane over booted server paths
test-race-integration:
	go test -race ./internal/http -run TestServer -count=1 -timeout=120s

## test-cover: Run tests with coverage report (fails below COVER_MIN)
COVER_MIN ?= 47.0
test-cover:
	go test -p 4 $(FAST_TEST_PACKAGES) -coverprofile=coverage.out -count=1
	go run ./tools/coversummary --profile=coverage.out --min=$(COVER_MIN)
	@echo "HTML report: go tool cover -html=coverage.out"

## lint: Run linters (requires golangci-lint)
## Runs errcheck, govet, staticcheck, unused, revive, gocritic, unconvert,
## usestdlibvars, fatcontext, and dupl via golangci-lint.
lint:
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 || (echo "golangci-lint not installed — run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)" && exit 1)
	golangci-lint run ./...

## lint-full: Full lint + vulnerability check (for CI and pre-release)
lint-full: lint
	@command -v govulncheck >/dev/null 2>&1 || (echo "govulncheck not installed — run: go install golang.org/x/vuln/cmd/govulncheck@latest" && exit 1)
	govulncheck ./...

## bench-pr: Short deterministic PR benchmark lane (~60s, stable for benchstat)
## The bridge telemetry benches run in separate go test invocations so replay
## and profile coverage do not destabilize the lighter query sub-benchmarks on
## local ephemeral Postgres clusters.
bench-pr:
	go test ./internal/envelope ./internal/grouping ./internal/normalize ./internal/domain ./internal/http -run '^$$' -bench . -benchmem -count=5 -cpu 1 -benchtime=500ms
	go test ./internal/telemetryquery -run '^$$' -bench '^BenchmarkBridgeService/(SearchLogs|ExecuteTransactionsTable|TraceDetail)$$' -benchmem -count=5 -cpu 1 -benchtime=500ms
	go test ./internal/telemetryquery -run '^$$' -bench '^BenchmarkBridgeService/(GetReplay|GetProfile)$$' -benchmem -count=5 -cpu 1 -benchtime=250ms

## bench-budget: Run bench-pr and enforce ns/op, B/op, and allocs/op budgets
bench-budget:
	$(MAKE) bench-pr > test-results/bench-pr-latest.txt 2>&1 || true
	bash scripts/check-bench-budget.sh test-results/bench-pr-latest.txt

## bench: Broad scheduled benchmark suite (all packages, single pass)
## Pipeline shutdown benchmarks are capped at 100 iterations to prevent
## multi-minute calibration stalls from low-ns/op + expensive setup/teardown.
## Output is persisted to test-results/bench-broad-TIMESTAMP.txt for benchstat.
bench:
	mkdir -p test-results
	{ go test -run '^$$' -bench=. -benchmem $$(go list ./... | grep -v /internal/pipeline) && \
	  go test -run '^$$' -bench=. -benchmem -benchtime=100x ./internal/pipeline; } \
	  2>&1 | tee test-results/bench-broad-$$(date +%Y%m%d-%H%M%S).txt

## selfhosted-bench: Run the serious self-hosted performance eval lane
selfhosted-bench:
	bash ../../eval/dimensions/selfhostedperf/run.sh

## selfhosted-eval: Run the serious self-hosted readiness scorecard (requires Docker Compose, kind, and kubectl)
selfhosted-eval:
	bash ../../eval/run-selfhosted.sh

## profile-bench: Run deterministic microbenchmarks for parse/normalize/grouping
profile-bench:
	GOMAXPROCS=$(PROFILE_GOMAXPROCS) go test ./internal/envelope ./internal/normalize ./internal/grouping -run '^$$' -bench . -benchmem -count=10 -cpu 1 -benchtime=500ms

## profile: Capture summary/cpu/heap/allocs artifacts for fixed profiling scenarios
profile:
	$(PROFILE_RESET)
	for kind in none cpu heap allocs; do \
		$(call PROFILE_RUN_SCENARIOS,$$kind); \
	done

## profile-trace: Capture execution traces for fixed profiling scenarios
profile-trace:
	$(PROFILE_RESET)
	$(call PROFILE_RUN_SCENARIOS,trace)

## synthetic-registry: Regenerate the checked-in synthetic surface/entity/query registries
synthetic-registry:
	go run ./tools/syntheticregistry --write

## synthetic-registry-check: Fail if the checked-in synthetic registries are stale
synthetic-registry-check:
	go run ./tools/syntheticregistry --write=false --check

## synthetic-generate: Regenerate synthetic corpus manifests and structured payload bodies
synthetic-generate:
	go run ./cmd/urgentry synthetic generate --surface all

## synthetic-audit: Summarize synthetic coverage against the checked-in registry and packs
synthetic-audit:
	go run ./cmd/urgentry synthetic audit

## synthetic-check: Verify synthetic manifests and scenario-pack tests
synthetic-check:
	go test ./internal/synthetic/... -count=1

## fuzz: Run fuzz tests (30 seconds each)
fuzz:
	go test -fuzz=Fuzz -fuzztime=30s ./internal/envelope/
	go test -fuzz=Fuzz -fuzztime=30s ./internal/normalize/

## docker: Build Docker image
docker:
	docker build --build-arg VERSION=$(VERSION) -t urgentry:$(VERSION) -t urgentry:latest .

## release: Build release binaries for all platforms
release:
	bash ./scripts/release.sh $(VERSION)

## tidy: Clean up module dependencies
tidy:
	go mod tidy

## vulncheck: Check for known vulnerabilities
vulncheck:
	@command -v govulncheck >/dev/null 2>&1 || (echo "govulncheck not installed — run: go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)" && exit 1)
	govulncheck ./...

## clean: Remove build artifacts
clean:
	rm -f $(BINARY) coverage.out *.prof
	rm -rf dist/

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'
