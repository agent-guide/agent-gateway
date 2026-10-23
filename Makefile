# Makefile for Caddy LLM Router

.PHONY: all build build-agw build-agwd build-agwctl build-xcaddy clean deps fmt verify-release verify-adoption smoke-release smoke-container smoke-first-run smoke-observability release-archive verify-release-archive first-run-archive verify-first-run-archive test-extension-consumer test-extension-consumer-candidate test-extension-consumer-release

# Binary names
BINARY_NAME=agw
DAEMON_BINARY_NAME=agwd
CLI_BINARY_NAME=agwctl

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
XCADDY=xcaddy
RELEASE_VERSION?=0.7.2
RELEASE_OUTPUT_DIR?=dist/release

all: clean deps build

# Build the binary (recommended method)
build:
	@echo "Building $(BINARY_NAME)..."
	$(GOBUILD) -o $(BINARY_NAME) ./cmd/agw
	$(GOBUILD) -o $(DAEMON_BINARY_NAME) ./cmd/agwd
	$(GOBUILD) -o $(CLI_BINARY_NAME) ./cmd/agwctl

build-agw:
	$(GOBUILD) -o $(BINARY_NAME) ./cmd/agw

build-agwd:
	$(GOBUILD) -o $(DAEMON_BINARY_NAME) ./cmd/agwd

build-agwctl:
	$(GOBUILD) -o $(CLI_BINARY_NAME) ./cmd/agwctl

build-xcaddy:
	@echo "Buiding with xcaddy..."
	XCADDY_DEBUG=1 $(XCADDY) build --with github.com/agent-guide/agent-gateway=$(shell pwd)

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(DAEMON_BINARY_NAME)
	rm -f $(CLI_BINARY_NAME)
	rm -rf buildenv_*

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Format code
fmt:
	@echo "Formatting code..."
	$(GOCMD) fmt ./...

# Run the same non-mutating release gates used by CI.
verify-release:
	./scripts/verify-release

# Check published examples, documentation links/parity, shell syntax, and
# release-facing files for local paths or credential-shaped values.
verify-adoption:
	./scripts/verify-adoption

# Exercise the built binaries and both HTTP Agent ingress paths as processes.
smoke-release: build
	AGW_RELEASE_SMOKE=1 $(GOTEST) ./tests/release -run TestReleaseProcesses -v

# Build and smoke the native gateway and deterministic fixture images.
smoke-container:
	./scripts/smoke-container $(RELEASE_VERSION)

# Exercise the first-run project from a temporary directory outside the repo.
smoke-first-run:
	./scripts/smoke-first-run $(RELEASE_VERSION)

# Include provisioned Prometheus, Tempo, OpenTelemetry Collector, and Grafana.
smoke-observability:
	AGW_SMOKE_OBSERVABILITY=1 ./scripts/smoke-first-run $(RELEASE_VERSION)

# Build and verify a native release archive. CI runs this target on every
# supported operating-system and architecture tuple.
release-archive:
	./scripts/package-release-archive $(RELEASE_VERSION) $(RELEASE_OUTPUT_DIR)

verify-release-archive: release-archive
	./scripts/verify-release-archive \
		$(RELEASE_OUTPUT_DIR)/agent-gateway-v$(RELEASE_VERSION)-$$(go env GOOS)-$$(go env GOARCH).tar.gz \
		$(RELEASE_VERSION) $$(go env GOOS) $$(go env GOARCH)

# Package the checkout's first-run project with immutable image references.
# Release automation supplies the two digest-qualified references.
first-run-archive:
	@test -n "$(GATEWAY_IMAGE_REF)" || (echo 'set GATEWAY_IMAGE_REF to an image digest reference' >&2; exit 2)
	@test -n "$(FIXTURE_IMAGE_REF)" || (echo 'set FIXTURE_IMAGE_REF to an image digest reference' >&2; exit 2)
	./scripts/package-first-run $(RELEASE_VERSION) \
		"$(GATEWAY_IMAGE_REF)" "$(FIXTURE_IMAGE_REF)" $(RELEASE_OUTPUT_DIR)

verify-first-run-archive: first-run-archive
	./scripts/verify-first-run-archive \
		$(RELEASE_OUTPUT_DIR)/agent-gateway-first-run-v$(RELEASE_VERSION).tar.gz \
		$(RELEASE_VERSION) "$(GATEWAY_IMAGE_REF)" "$(FIXTURE_IMAGE_REF)"

# Build the independent Go-module entries against this checkout using a temporary
# replacement only; no go.work or replace directive is checked in.
test-extension-consumer:
	./scripts/test-extension-consumer

# Immutable candidate proof; commits must be recorded before invoking this gate.
test-extension-consumer-candidate:
	@test -n "$(COMMUNITY_COMMIT)" || (echo 'set COMMUNITY_COMMIT to an exact commit' >&2; exit 2)
	./scripts/test-extension-consumer candidate "$(COMMUNITY_COMMIT)"

# Fresh caches, published dependency, no checkout replacement or workspace.
test-extension-consumer-release:
	./scripts/test-extension-consumer release
