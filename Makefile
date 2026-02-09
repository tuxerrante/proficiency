# Makefile for proficiency
# Dependencies are structured to enforce quality gates:
# fmt -> lint -> test

.PHONY: all fmt fmt-go fmt-md lint test coverage build build-only clean help e2e e2e-clean

# Default target
all: test build

# Help target
help:
	@echo "Available targets:"
	@echo "  fmt        - Format Go and Markdown files"
	@echo "  fmt-go     - Format Go files with gofmt and goimports"
	@echo "  fmt-md     - Format Markdown files with prettier"
	@echo "  lint       - Run golangci-lint (depends on fmt)"
	@echo "  test       - Run tests with coverage (depends on lint)"
	@echo "  coverage   - Run tests and generate coverage.out profile"
	@echo "  build      - Build the CLI binary (depends on test)"
	@echo "  build-only - Build the CLI binary (no dependencies)"
	@echo "  clean      - Remove build artifacts"
	@echo "  e2e        - Run E2E tests (build stress server, profile, analyze)"
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

# Lint with golangci-lint (depends on fmt)
lint: fmt
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

# Build the CLI binary (depends on test)
build: test
	@echo "==> Building proficiency..."
	go build -o proficiency ./cmd/proficiency

# Build only (no dependencies, for CI after coverage)
build-only:
	@echo "==> Building proficiency..."
	go build -o proficiency ./cmd/proficiency

# Run E2E tests: build stress server, run proficiency, analyze profiles
e2e: build-only
	@chmod +x e2e/run.sh
	@./e2e/run.sh

# Remove E2E artifacts
e2e-clean:
	@rm -rf e2e-profiles bin/testserver stress.db

# Clean build artifacts
clean: e2e-clean
	@echo "==> Cleaning..."
	rm -f proficiency coverage.out
	rm -rf profiles/
