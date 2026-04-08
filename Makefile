.PHONY: build build-debug check-links check-tidy test test-merge test-race lint lint-full vulncheck run tiny-smoke tiny-launch-gate tiny-sentry-baseline selfhosted-sentry-baseline docker release tidy clean help

BINARY := urgentry
REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null || pwd)
ROOT_CHECK_LINKS := $(REPO_ROOT)/scripts/check-markdown-links.sh
ROOT_CHECK_TIDY := $(REPO_ROOT)/scripts/check-go-mod-tidy.sh
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || cat $(REPO_ROOT)/VERSION 2>/dev/null || echo dev)
BUILD_SCRIPT := bash ./scripts/build-urgentry.sh
GOLANGCI_LINT_VERSION := v1.64.5
GOVULNCHECK_VERSION := v1.1.4
PUBLIC_TEST_PACKAGES = $$(go list ./... \
	| grep -v '^urgentry/internal/compat$$' \
	| grep -v '^urgentry/cmd/urgentry$$' \
	| grep -v '^urgentry/internal/envelope$$' \
	| grep -v '^urgentry/internal/grouping$$' \
	| grep -v '^urgentry/internal/ingest$$' \
	| grep -v '^urgentry/internal/normalize$$' \
	| grep -v '^urgentry/internal/profile$$' \
	| grep -v '^urgentry/internal/synthetic')

## build: Build the default optimized Tiny-mode binary
build:
	VERSION=$(VERSION) $(BUILD_SCRIPT) --output $(BINARY)

## build-debug: Build an unstripped local debug binary
build-debug:
	go build -ldflags="-X urgentry/internal/config.Version=$(VERSION) -X urgentry/internal/config.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown) -X urgentry/internal/config.BuildDate=$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')" -o $(BINARY) ./cmd/urgentry

## run: Build and run Tiny mode locally
run: build
	./$(BINARY) serve --role=all

## tiny-smoke: Boot Tiny mode and verify login + bootstrap API flow
tiny-smoke:
	bash ./scripts/tiny-smoke.sh

## tiny-launch-gate: Run the Tiny public-launch hardening gate
tiny-launch-gate:
	bash ./scripts/tiny-launch-gate.sh

## tiny-sentry-baseline: Replay the checked-in Sentry baseline corpus against Tiny mode
tiny-sentry-baseline:
	bash ./scripts/tiny-sentry-baseline.sh

## selfhosted-sentry-baseline: Replay the checked-in Sentry baseline corpus against self-hosted mode
selfhosted-sentry-baseline:
	bash ./scripts/selfhosted-sentry-baseline.sh

## test: Run the public fast test suite
test:
	URGENTRY_SKIP_POSTGRES_TESTS=1 go test $(PUBLIC_TEST_PACKAGES) -count=1

## test-race: Run the public fast suite with the race detector
test-race:
	go test -race -p 4 $(PUBLIC_TEST_PACKAGES) -count=1

## check-links: Verify Markdown links are valid in the exported repo
check-links:
	bash "$(ROOT_CHECK_LINKS)"

## check-tidy: Verify the exported Go modules are tidy-clean
check-tidy:
	bash "$(ROOT_CHECK_TIDY)"

## lint: Run go vet and golangci-lint
lint:
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 || (echo "golangci-lint not installed — run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)" && exit 1)
	golangci-lint run ./...

## vulncheck: Check for known vulnerabilities
vulncheck:
	@command -v govulncheck >/dev/null 2>&1 || (echo "govulncheck not installed — run: go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)" && exit 1)
	govulncheck ./...

## lint-full: Lint plus vulnerability scan
lint-full: lint vulncheck

## test-merge: Run the public merge gate
test-merge: check-links check-tidy test vulncheck

## docker: Build a local Docker image
docker:
	docker build --build-arg VERSION=$(VERSION) -t urgentry:$(VERSION) -t urgentry:latest .

## release: Build release binaries and tarballs
release:
	bash ./scripts/release.sh $(VERSION)

## tidy: Clean up module dependencies
tidy:
	go mod tidy

## clean: Remove build artifacts
clean:
	rm -f $(BINARY) coverage.out *.prof
	rm -rf dist/

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'
