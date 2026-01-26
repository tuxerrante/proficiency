# Makefile for proficiency
# Dependencies are structured to enforce quality gates:
# fmt -> lint -> test

.PHONY: all fmt fmt-go fmt-md lint test build clean help

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
	@echo "  build      - Build the CLI binary (depends on test)"
	@echo "  clean      - Remove build artifacts"
	@echo "  all        - Run test and build (default)"

# Format Go files
fmt-go:
	@echo "==> Formatting Go files..."
	gofmt -w .
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w .; \
	else \
		echo "Warning: goimports not installed, skipping. Install with: go install golang.org/x/tools/cmd/goimports@latest"; \
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

# Build the CLI binary (depends on test)
build: test
	@echo "==> Building proficiency..."
	go build -o proficiency ./cmd/proficiency

# Clean build artifacts
clean:
	@echo "==> Cleaning..."
	rm -f proficiency
	rm -rf profiles/
