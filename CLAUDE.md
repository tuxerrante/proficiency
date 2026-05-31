# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

Proficiency is a Go-powered CLI tool and GitHub Action that automates API performance profiling.
It takes an OpenAPI/Swagger spec, generates load against the API, collects pprof profiles,
and produces machine-readable reports identifying performance bottlenecks.

## Go Development Guidelines

Follow the standards at: <https://raw.githubusercontent.com/tuxerrante/llm-prompt-template/refs/heads/main/backend/go.md>

## Build Commands

```bash
# Build the CLI
go build -o proficiency ./cmd/proficiency

# Install globally
go install ./cmd/proficiency

# Run tests with coverage
go test -v -cover ./...

# Run tests with race detection
go test -race ./...

# Format code (uses gofumpt via golangci-lint)
golangci-lint fmt ./...

# Lint (requires golangci-lint v2)
golangci-lint run
```

## Running the CLI

```bash
# Start the test server (exposes stress endpoints + pprof on :8080)
cd e2e/testserver && go run . &

# Run proficiency against it
./proficiency \
  --openapi ./e2e/openapi.yaml \
  --target http://localhost:8080 \
  --duration 10s \
  --concurrency 5 \
  --rps 50
```

Target service must expose pprof at `/debug/pprof/` (import `_ "net/http/pprof"`).

## Package Structure

```
cmd/proficiency/        CLI entry point, flag parsing, workflow orchestration
internal/
  openapi/              OpenAPI 3.0 parsing (uses kin-openapi)
    testdata/            Test fixtures (petstore.yaml)
  load/                 HTTP load generation with rate limiting (uses x/time/rate)
  profile/              pprof collection via HTTP
e2e/                    E2E test infrastructure
  testserver/           Stress test server (separate Go module)
docs/                   Architecture and implementation docs
```

## Key Design Decisions

- **kin-openapi** for OpenAPI parsing (native 3.0 support, validation built-in)
- **x/time/rate** token bucket for rate limiting (smooth distribution, burst handling)
- HTTP-based pprof collection (works with any Go service, no target changes needed)
- Parallel workflow: parse spec → verify pprof → [load test + profile collection]

See `docs/implementation.md` for detailed tradeoffs and alternatives.
