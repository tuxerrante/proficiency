# Makefile for proficiency
# Dependencies are structured to enforce quality gates:
# fmt -> lint -> test

.PHONY: all fmt fmt-go fmt-md skills-check lint test coverage build build-only clean help e2e e2e-clean container-test external-test release-assets release-verify

# Default target
all: test build

# Help target
help:
	@echo "Available targets:"
	@echo "  fmt        - Format Go and Markdown files"
	@echo "  fmt-go     - Format Go files with gofmt and goimports"
	@echo "  fmt-md     - Format Markdown files with prettier"
	@echo "  lint       - Run golangci-lint (depends on fmt)"
	@echo "  skills-check - Validate repository-local Agent Skills"
	@echo "  test       - Run tests with coverage (depends on lint)"
	@echo "  coverage   - Run tests and generate coverage.out profile"
	@echo "  build      - Build the CLI binary (depends on test)"
	@echo "  build-only - Build the CLI binary (no dependencies)"
	@echo "  clean      - Remove build artifacts"
	@echo "  e2e        - Run E2E tests (build stress server, profile, analyze)"
	@echo "  container-test - Run the Docker Compose integration test"
	@echo "  external-test  - Validate go install and the public package from a temporary module"
	@echo "  release-assets - Build and verify release assets (RELEASE_VERSION=vX.Y.Z)"
	@echo "  release-verify - Verify a published release and Marketplace listing"
	@echo "  e2e-clean  - Remove E2E artifacts"
	@echo "  all        - Run test and build (default)"

# Format Go files (gofumpt via golangci-lint)
fmt-go:
	@echo "==> Formatting Go files..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint fmt ./...; \
	elif command -v gofumpt >/dev/null 2>&1; then \
		gofumpt -w .; \
	else \
		gofmt -w .; \
		echo "Warning: golangci-lint or gofumpt not installed, fell back to gofmt"; \
	fi

# Format Markdown files
fmt-md:
	@echo "==> Formatting Markdown files..."
	@if command -v prettier >/dev/null 2>&1; then \
		prettier --write "**/*.md" 2>/dev/null || true; \
	else \
		echo "Warning: prettier not installed, skipping. Install with: npm install -g prettier"; \
	fi

# Format all files (Go + Markdown)
fmt: fmt-go fmt-md
	@echo "==> Formatting complete"

# Validate repository-local Agent Skills.
skills-check:
	@bash scripts/validate-skills.sh

# Lint with golangci-lint (depends on fmt and skill validation)
lint: fmt skills-check
	@echo "==> Running golangci-lint..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "Error: golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

# Run tests with coverage (depends on lint)
test: lint
	@echo "==> Running tests with coverage..."
	go test -v -race -cover ./...

# Run tests and generate coverage profile (for CI)
coverage: lint
	@echo "==> Running tests with coverage profile..."
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	@echo "==> Coverage summary:"
	@go tool cover -func=coverage.out | grep total

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.Version=$(VERSION)

# Build the CLI binary (depends on test)
build: test
	@echo "==> Building proficiency $(VERSION)..."
	go build -ldflags="$(LDFLAGS)" -o proficiency ./cmd/proficiency

# Build only (no dependencies, for CI after coverage)
build-only:
	@echo "==> Building proficiency $(VERSION)..."
	go build -ldflags="$(LDFLAGS)" -o proficiency ./cmd/proficiency

# Run E2E tests: build stress server, run proficiency, analyze profiles
e2e: build-only
	@chmod +x e2e/run.sh
	@PROFICIENCY_BIN_PREBUILT=1 ./e2e/run.sh
	go test -tags=e2e -v ./e2e

# Run the CLI and target service as isolated containers on one Docker network.
container-test:
	@chmod +x e2e/container.sh
	@./e2e/container.sh

# Exercise the module exactly as a separate Go project would consume it.
external-test:
	@chmod +x scripts/test-external-consumer.sh
	@./scripts/test-external-consumer.sh

DIST_DIR ?= dist

# Build the same release assets produced by the draft-release workflow.
release-assets:
	@test -n "$(RELEASE_VERSION)" || (echo "RELEASE_VERSION is required" >&2; exit 1)
	@bash scripts/release/build-assets.sh "$(RELEASE_VERSION)" "$(DIST_DIR)"
	@bash scripts/release/verify-assets.sh "$(RELEASE_VERSION)" "$(DIST_DIR)"

# Verify an immutable release, tagged go install, major Action tag, and Marketplace listing.
release-verify:
	@test -n "$(RELEASE_VERSION)" || (echo "RELEASE_VERSION is required" >&2; exit 1)
	@bash scripts/release/verify-published.sh "$(RELEASE_VERSION)"

# Remove E2E artifacts
e2e-clean:
	@rm -rf e2e-profiles bin/testserver stress.db

# Clean build artifacts
clean: e2e-clean
	@echo "==> Cleaning..."
	rm -f proficiency coverage.out
	rm -rf profiles/ dist/
