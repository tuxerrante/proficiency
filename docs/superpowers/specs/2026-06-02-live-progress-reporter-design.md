# Live Progress Reporter — Design Spec

**Issue**: [#15 — Rung 1: Live Progress Reporter](https://github.com/tuxerrante/proficiency/issues/15)
**Date**: 2026-06-02

## Problem

During a load test the CLI is silent. Users need live feedback: requests sent, error rate, current RPS, ETA.

## Approach

Approach A — counters inside `Runner`, reporter as a separate struct. Workers do atomic writes, reporter does atomic reads on a `time.Ticker`. No mutex.

Rejected alternatives:
- **Reporter embedded in Runner**: mixes presentation into load generation, hard to test independently, hard to disable for non-TTY.
- **Callback interface**: over-engineered for a single consumer.

## Components

### 1. `LiveCounters` (in `internal/load/runner.go`)

```go
type LiveCounters struct {
    Requests atomic.Int64
    _pad0    [cacheLinePad]byte
    Errors   atomic.Int64
    _pad1    [cacheLinePad]byte
}
```

- `cacheLinePad` = `64 - unsafe.Sizeof(atomic.Int64{})`, computed as a constant.
- Each field on its own 64-byte cache line to prevent false sharing between cores.
- Workers call `counters.Requests.Add(1)` / `counters.Errors.Add(1)`.
- Reporter reads via `counters.Requests.Load()` / `counters.Errors.Load()`.
- Zero-value ready — no constructor needed.
- Exported `Counters` field on `Runner` so the caller can pass a pointer to the reporter.

### 2. `ProgressReporter` (new file `internal/load/progress.go`)

```go
type ProgressReporter struct {
    counters  *LiveCounters
    duration  time.Duration
    startTime time.Time
    w         io.Writer
    done      chan struct{}
}
```

- `NewProgressReporter(counters *LiveCounters, duration time.Duration, w io.Writer) *ProgressReporter`
- `Start()` launches a goroutine with `time.NewTicker(1 * time.Second)`.
- Each tick reads counters via `.Load()`, computes:
  - RPS = requests / elapsed seconds
  - Error rate = (errors / requests) * 100
  - ETA = duration - elapsed
- Output format: `[12s/60s] 1200 reqs | 23.4% err | 98 RPS | ETA 48s`
- Writes to `w` (defaults to `os.Stderr` in production; `bytes.Buffer` in tests).
- `Stop()` signals done channel, waits for goroutine exit, calls `ticker.Stop()`.

### 3. CLI wiring (in `cmd/proficiency/run.go`)

In `runWithLoad()`, after creating the runner:

```go
reporter := load.NewProgressReporter(&runner.Counters, cfg.Duration, os.Stderr)
reporter.Start()
defer reporter.Stop()
```

## Testing Strategy

| Test | File | Verifies |
|------|------|----------|
| `TestLiveCounters_Padding` | `runner_test.go` | `unsafe.Offsetof` confirms Errors starts at byte 64 (cache-line aligned) |
| `TestLiveCounters_ConcurrentAccess` | `runner_test.go` | Multiple goroutines increment, `go test -race` clean |
| `TestRunner_Run_IncrementsCounters` | `runner_test.go` | Counters match Stats after Run completes |
| `TestProgressReporter_Output` | `progress_test.go` | Format verification using `bytes.Buffer` |
| `TestProgressReporter_StopClean` | `progress_test.go` | No goroutine leak after Stop |
| `TestProgressReporter_ZeroRequests` | `progress_test.go` | Handles 0 requests without divide-by-zero |

All tests use `t.Parallel()` where independent. Race detection via `go test -race ./...`.

## PR Plan

| PR | Branch | Scope | ~Lines |
|----|--------|-------|--------|
| 1 | `issue/15-live-counters` | `LiveCounters` struct + padding + atomic increments in `worker()` + tests | ~80 |
| 2 | `issue/15-progress-reporter` | `ProgressReporter` struct + ticker loop + tests | ~120 |
| 3 | `issue/15-cli-progress` | CLI wiring in `run.go` + integration test | ~40 |

Each PR merges independently without breaking tests or coverage.

## Acceptance Criteria (from issue)

- [x] Design: `ProgressReporter` struct reading `atomic.Int64` counters from workers
- [x] Design: Status line every second via `time.Ticker`
- [x] Design: No mutex — atomic writes/reads only
- [x] Design: Cache-line padding to avoid false sharing
- [ ] Implementation: `go test -race` passes
- [ ] Implementation: `go build -gcflags='-m'` — no unexpected escapes
