# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.2.0] - Unreleased

### Added

- Importable `github.com/tuxerrante/proficiency` package with `Config`, `Run`,
  report I/O, comparison APIs, and typed gate errors
- Versioned `v1` JSON reports containing source metadata, load statistics,
  profile artifacts, ranked bottlenecks, and threshold outcomes
- Baseline report comparison with latency, error-rate, throughput, CPU,
  allocation, block, and goroutine regression policies
- Composite GitHub Action with checksum-verified release downloads and an
  explicit source-build mode for pre-release validation
- Multi-stage non-root container image and Docker Compose integration test
- Ephemeral external-module test covering package import and `go install`
- Release workflow producing Linux/macOS archives and SHA-256 checksums

### Changed

- Moved profiling orchestration out of `cmd/proficiency` so the CLI and public
  package share one implementation
- E2E runs now assert a generated versioned report
- Upgraded Go to 1.26.5 and `kin-openapi` to 0.144.0 to include current
  standard-library and dependency security fixes

### Fixed

- Runs now fail when every requested profile collection fails instead of
  returning a successful empty result

## [0.1.2] - 2026-06-19

### Added

- Deterministic JSON request-body generation from OpenAPI examples, defaults,
  schemas, and scalar enum values
- Repository E2E coverage for POST request bodies

## [0.1.1] - 2026-06-02

Live progress reporting during load tests (Rung 1 of the Go Senior Roadmap).

### Added

- **Live progress reporter**: status line updates every second during load tests showing requests, error rate, RPS, and ETA (#38, #39, #40)
- **`--no-progress` flag**: explicitly disable status line output (#44)
- **TTY auto-detection**: progress line skipped automatically when stderr is not a terminal (CI, piped output) via `golang.org/x/term` (#44)
- **`Result.IsError()` helper**: centralized HTTP error classification (#41)
- **`LiveCounters` struct**: lock-free atomic counters with 64-byte cache-line padding to prevent false sharing (#38)

### Fixed

- **Counter/Stats divergence**: atomic counters now increment after successful channel send, preventing `Counters > Stats` on context cancellation (#41)
- **Double-close panic**: `ProgressReporter.Stop()` is now idempotent via `sync.Once` (#42)
- **Stderr interleaving**: reporter stops explicitly before post-load output, preventing `\r` from overwriting warnings (#42)

### Changed

- Updated `docs/implementation.md` to document progress reporter design decisions, parallel profile collection, and revised workflow (#43)
- Added `docs/superpowers/` to `.gitignore` (ephemeral process artifacts) (#43)

### Dependencies

- Added `golang.org/x/term` v0.43.0 (TTY detection)
- Upgraded `golang.org/x/sys` v0.32.0 → v0.44.0

## [0.1.0] - 2026-06-02

First public release — CLI tool for automated API performance profiling.

### Added

- **OpenAPI 3.0 parsing** via kin-openapi: extract endpoints from specs
- **HTTP load generation** with configurable concurrency, RPS, and duration
- **pprof profile collection**: CPU, heap, block, and goroutine profiles
- **Time-series sampling** via `--sample-interval` for leak/growth detection
- **Watch mode** (`--skip-load + --sample-interval`): monitor pprof endpoints without generating load, all profile types collected in parallel
- **CI threshold gating** via `--fail-on` (e.g., `cpu:30,alloc:50`): exit non-zero when function cost exceeds threshold
- **Configurable profile types** via `--profile-types` (cpu, heap, block, goroutine)
- **Separate pprof target** via `--pprof-target` for split-port deployments
- **E2E test infrastructure**: stress server with intentional CPU, memory, and I/O inefficiencies
- **Security scanning CI**: gosec, govulncheck, trivy on push/PRs/weekly
- **82% test coverage** with integration tests using httptest

### Security

- No known vulnerabilities at release time
- Profile files written with 0600 permissions, output directory with 0750
- Security policy documented in SECURITY.md

[Unreleased]: https://github.com/tuxerrante/proficiency/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/tuxerrante/proficiency/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/tuxerrante/proficiency/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/tuxerrante/proficiency/releases/tag/v0.1.0
