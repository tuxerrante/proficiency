# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

[Unreleased]: https://github.com/tuxerrante/proficiency/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/tuxerrante/proficiency/releases/tag/v0.1.0
