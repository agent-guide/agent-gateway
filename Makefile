# Makefile for Caddy LLM Router

.PHONY: all build build-agw build-agwd build-agwctl build-xcaddy clean deps fmt

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
	XCADDY_DEBUG=1 $(XCADDY) build --with github.com/agent-guide/caddy-agent-gateway=$(shell pwd)

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
