# --fail-on Flag: CI Threshold Gating — Design Spec

**Date:** 2026-05-27
**Status:** Approved
**Repo:** proficiency

## Problem

Proficiency collects pprof profiles but always exits 0. CI pipelines need a
non-zero exit code when performance regressions are detected. The rh-perf
Stage 02 GH Action workflow needs this for exercise 3 (CI gating).

## Goal

Add a `--fail-on` flag that evaluates collected profiles against thresholds
and exits with code 1 when any function exceeds the threshold.

## Flag Format

```text
--fail-on=cpu:30           # fail if any function uses >30% of CPU samples
--fail-on=cpu:30,alloc:50  # multiple thresholds, comma-separated
--fail-on=cpu:30,block:20  # supported types: cpu, alloc, block
```

Supported profile types map to pprof file types:

- `cpu` → CPU profile (flat% of total samples)
- `alloc` → Heap profile using alloc_space (flat% of total allocations)
- `block` → Block profile (flat% of total blocking time)

## Architecture

New package `internal/analysis` with one public function:

```go
// CheckThresholds parses collected profiles and evaluates them against
// thresholds. Returns a list of violations. Each violation names the
// function, its percentage, and the threshold it exceeded.
func CheckThresholds(profiles []*profile.CollectedProfile, thresholds []Threshold) ([]Violation, error)
```

Profile parsing uses `github.com/google/pprof/profile` — the canonical Go
library for reading pprof protobuf files (same library `go tool pprof` uses).

## Integration into main.go

1. Parse `--fail-on` flag value into `[]Threshold` in `parseFlags()`
2. After profile collection completes (after line 292), call
   `analysis.CheckThresholds(profiles, thresholds)`
3. Print violations to stderr
4. Exit 1 if any violations found

When `--fail-on` is empty (default), skip analysis and exit 0 as before.

## Output Format

```text
FAIL: cpu threshold exceeded
  main.stringConcat  57.04%  (threshold: 30%)
  runtime.memmove    9.91%   (threshold: 30%)  [note: below threshold]

Only functions exceeding the threshold are listed as failures.
```

## New Dependency

`github.com/google/pprof` — the canonical pprof protobuf parser. Used by
`go tool pprof` itself. MIT license. Well-maintained by Google.

## Files

- Create: `internal/analysis/analysis.go` — threshold checking logic
- Create: `internal/analysis/analysis_test.go` — unit tests with fixture profiles
- Modify: `cmd/proficiency/main.go` — add flag, wire analysis after collection
- Modify: `go.mod` — add google/pprof dependency

## Out of Scope

- JSON report output (separate feature)
- Trend analysis or baseline comparison
- Profile type `goroutine` or `mutex` thresholds (can add later)
