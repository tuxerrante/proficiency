# Live Progress Reporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Print a live status line every second during load tests showing requests sent, error rate, RPS, and ETA.

**Architecture:** Add `LiveCounters` (padded atomic counters) to the `Runner` struct, incremented by workers. A separate `ProgressReporter` reads them on a `time.Ticker` and prints to an `io.Writer`. CLI wires the two together in `runWithLoad()`.

**Tech Stack:** Go 1.26, `sync/atomic`, `time.Ticker`, `unsafe.Offsetof`

**Spec:** `docs/superpowers/specs/2026-06-02-live-progress-reporter-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/load/runner.go` | Add `LiveCounters` struct, `Counters` field on `Runner`, atomic increments in `worker()` |
| Modify | `internal/load/runner_test.go` | Padding test, concurrent access test, counter-integration test |
| Create | `internal/load/progress.go` | `ProgressReporter` struct, `Start()`/`Stop()`, ticker loop, format output |
| Create | `internal/load/progress_test.go` | Output format test, stop-clean test, zero-requests edge case |
| Modify | `cmd/proficiency/run.go` | Wire reporter into `runWithLoad()` |

---

## PR 1: LiveCounters (`issue/15-live-counters`)

### Task 1: Write padding test for LiveCounters

**Files:**
- Modify: `internal/load/runner_test.go`
- Modify: `internal/load/runner.go` (minimal struct to compile)

- [ ] **Step 1: Add the LiveCounters struct (empty, just enough to compile the test)**

Add to `internal/load/runner.go`, after the `Runner` struct (before `NewRunner`):

```go
// LiveCounters holds atomic counters updated by workers and read by the
// progress reporter. Each field is padded to a 64-byte cache line to prevent
// false sharing: without padding, two atomics on the same cache line force
// CPU cores to invalidate each other's caches on every write, even though
// they are logically independent. Atomics (not a mutex) because each counter
// is a single independent value — a mutex would serialize all workers through
// one lock on every request, which is unnecessary contention.
// A mutex would only be needed if the reader required a consistent snapshot
// of multiple fields simultaneously.
type LiveCounters struct {
	Requests atomic.Int64
	_pad0    [64 - unsafe.Sizeof(atomic.Int64{})]byte
	Errors   atomic.Int64
	_pad1    [64 - unsafe.Sizeof(atomic.Int64{})]byte
}
```

Add `"sync/atomic"` and `"unsafe"` to the import block in `runner.go`.

- [ ] **Step 2: Write the padding test**

Add to `internal/load/runner_test.go`:

```go
func TestLiveCounters_Padding(t *testing.T) {
	t.Parallel()

	var c LiveCounters
	errorsOffset := unsafe.Offsetof(c.Errors)

	// Errors must start at byte 64 — its own cache line, not sharing with Requests.
	if errorsOffset != 64 {
		t.Errorf("Errors field offset = %d, want 64 (cache-line aligned)", errorsOffset)
	}

	// Total struct size should be at least 128 (two cache lines).
	size := unsafe.Sizeof(c)
	if size < 128 {
		t.Errorf("LiveCounters size = %d, want >= 128 (two cache lines)", size)
	}
}
```

Add `"unsafe"` to the test file import block.

- [ ] **Step 3: Run test to verify it passes**

Run: `go test -v -run TestLiveCounters_Padding ./internal/load/`
Expected: PASS — the struct layout guarantees 64-byte alignment.

- [ ] **Step 4: Commit**

```bash
git add internal/load/runner.go internal/load/runner_test.go
git commit -m "feat(load): add LiveCounters struct with cache-line padding (#15)

Atomic counters padded to 64-byte cache lines to prevent false
sharing between worker goroutines."
```

---

### Task 2: Write concurrent access test

**Files:**
- Modify: `internal/load/runner_test.go`

- [ ] **Step 1: Write the concurrent access test**

Add to `internal/load/runner_test.go`:

```go
func TestLiveCounters_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	var c LiveCounters
	const goroutines = 100
	const incPerGoroutine = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range incPerGoroutine {
				c.Requests.Add(1)
				if c.Requests.Load()%10 == 0 {
					c.Errors.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	gotRequests := c.Requests.Load()
	if gotRequests != goroutines*incPerGoroutine {
		t.Errorf("Requests = %d, want %d", gotRequests, goroutines*incPerGoroutine)
	}

	// Errors are non-deterministic (Load races with Add from other goroutines),
	// but must be positive and not exceed requests.
	gotErrors := c.Errors.Load()
	if gotErrors <= 0 {
		t.Error("expected some errors to be recorded")
	}
	if gotErrors > gotRequests {
		t.Errorf("Errors (%d) exceeds Requests (%d)", gotErrors, gotRequests)
	}
}
```

Add `"sync"` to the test file import block (if not already present).

- [ ] **Step 2: Run test with race detector**

Run: `go test -v -race -run TestLiveCounters_ConcurrentAccess ./internal/load/`
Expected: PASS with no race warnings.

- [ ] **Step 3: Commit**

```bash
git add internal/load/runner_test.go
git commit -m "test(load): add concurrent access test for LiveCounters (#15)"
```

---

### Task 3: Wire counters into Runner and worker()

**Files:**
- Modify: `internal/load/runner.go`
- Modify: `internal/load/runner_test.go`

- [ ] **Step 1: Write the integration test**

Add to `internal/load/runner_test.go`:

```go
func TestRunner_Run_IncrementsCounters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoints := []openapi.Endpoint{
		{Method: "GET", Path: "/test"},
	}

	cfg := Config{
		Concurrency: 2,
		RPS:         50,
		Duration:    500 * time.Millisecond,
		Timeout:     5 * time.Second,
	}

	runner := NewRunner(cfg)
	stats, err := runner.Run(context.Background(), server.URL, endpoints)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	gotRequests := runner.Counters.Requests.Load()
	if gotRequests != stats.TotalRequests {
		t.Errorf("Counters.Requests = %d, want %d (Stats.TotalRequests)", gotRequests, stats.TotalRequests)
	}

	gotErrors := runner.Counters.Errors.Load()
	if gotErrors != stats.ErrorCount {
		t.Errorf("Counters.Errors = %d, want %d (Stats.ErrorCount)", gotErrors, stats.ErrorCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v -race -run TestRunner_Run_IncrementsCounters ./internal/load/`
Expected: FAIL — `runner.Counters` does not exist yet on `Runner`.

- [ ] **Step 3: Add Counters field to Runner and increment in worker()**

In `internal/load/runner.go`, add the `Counters` field to the `Runner` struct:

```go
type Runner struct {
	client   *http.Client
	config   Config
	limiter  *rate.Limiter
	Counters LiveCounters
}
```

In the `worker()` method, increment counters after `makeRequest()` returns. Replace the send-to-channel block:

```go
		result := r.makeRequest(ctx, targetURL, endpoint)
		r.Counters.Requests.Add(1)
		if result.Error != nil || result.StatusCode < 200 || result.StatusCode >= 300 {
			r.Counters.Errors.Add(1)
		}

		select {
		case results <- result:
		case <-ctx.Done():
			return
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -race -run TestRunner_Run_IncrementsCounters ./internal/load/`
Expected: PASS.

- [ ] **Step 5: Run full test suite to check for regressions**

Run: `go test -v -race ./internal/load/`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/load/runner.go internal/load/runner_test.go
git commit -m "feat(load): wire LiveCounters into Runner and worker (#15)

Workers atomically increment Requests and Errors counters on every
request, enabling lock-free reads from the progress reporter."
```

---

### Task 4: Create PR 1

- [ ] **Step 1: Push branch and create PR**

```bash
git push -u origin issue/15-live-counters
```

Create PR with:
- Title: `feat(load): add atomic LiveCounters with cache-line padding (#15)`
- Body: explain the false sharing rationale, link to issue #15, include test plan

- [ ] **Step 2: Wait for review / run self-review**

Check line count: if > 50 lines of code, wait for copilot review or run `/code-review`.

---

## PR 2: ProgressReporter (`issue/15-progress-reporter`)

Branch from: `main` (after PR 1 is merged)

### Task 5: Write output format test

**Files:**
- Create: `internal/load/progress_test.go`
- Create: `internal/load/progress.go` (minimal to compile)

- [ ] **Step 1: Create progress.go with minimal struct**

Create `internal/load/progress.go`:

```go
package load

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// ProgressReporter prints a live status line at regular intervals during
// a load test. It reads LiveCounters atomically — no synchronization
// needed with the workers writing to them.
type ProgressReporter struct {
	counters  *LiveCounters
	duration  time.Duration
	startTime time.Time
	w         io.Writer
	done      chan struct{}
	stopped   sync.WaitGroup
}

// NewProgressReporter creates a reporter that reads from the given counters.
// w is the output destination (os.Stderr in production, bytes.Buffer in tests).
func NewProgressReporter(counters *LiveCounters, duration time.Duration, w io.Writer) *ProgressReporter {
	return &ProgressReporter{
		counters: counters,
		duration: duration,
		w:        w,
		done:     make(chan struct{}),
	}
}

// Start begins printing status lines every second in a background goroutine.
func (p *ProgressReporter) Start() {
	p.startTime = time.Now()
	p.stopped.Add(1)
	go p.loop()
}

// Stop signals the reporter to stop and waits for the goroutine to exit.
func (p *ProgressReporter) Stop() {
	close(p.done)
	p.stopped.Wait()
}

func (p *ProgressReporter) loop() {
	defer p.stopped.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.printStatus()
		}
	}
}

func (p *ProgressReporter) printStatus() {
	elapsed := time.Since(p.startTime).Truncate(time.Second)
	reqs := p.counters.Requests.Load()
	errs := p.counters.Errors.Load()

	elapsedSec := elapsed.Seconds()
	rps := float64(0)
	if elapsedSec > 0 {
		rps = float64(reqs) / elapsedSec
	}

	errPct := float64(0)
	if reqs > 0 {
		errPct = float64(errs) / float64(reqs) * 100
	}

	remaining := p.duration - elapsed
	if remaining < 0 {
		remaining = 0
	}

	fmt.Fprintf(p.w, "\r[%s/%s] %d reqs | %.1f%% err | %.0f RPS | ETA %s",
		elapsed, p.duration.Truncate(time.Second),
		reqs, errPct, rps,
		remaining.Truncate(time.Second))
}
```

- [ ] **Step 2: Write the output format test**

Create `internal/load/progress_test.go`:

```go
package load

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressReporter_Output(t *testing.T) {
	t.Parallel()

	var counters LiveCounters
	counters.Requests.Store(100)
	counters.Errors.Store(5)

	var buf bytes.Buffer
	reporter := NewProgressReporter(&counters, 30*time.Second, &buf)

	// Call printStatus directly to test formatting without timing dependencies.
	reporter.startTime = time.Now().Add(-10 * time.Second)
	reporter.printStatus()

	output := buf.String()

	// Verify key components are present.
	if !strings.Contains(output, "100 reqs") {
		t.Errorf("output missing request count: %q", output)
	}
	if !strings.Contains(output, "5.0% err") {
		t.Errorf("output missing error rate: %q", output)
	}
	if !strings.Contains(output, "RPS") {
		t.Errorf("output missing RPS: %q", output)
	}
	if !strings.Contains(output, "ETA") {
		t.Errorf("output missing ETA: %q", output)
	}
	if !strings.Contains(output, "10s/30s") {
		t.Errorf("output missing elapsed/total: %q", output)
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test -v -race -run TestProgressReporter_Output ./internal/load/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/load/progress.go internal/load/progress_test.go
git commit -m "feat(load): add ProgressReporter with formatted status line (#15)

Reads LiveCounters atomically every second via time.Ticker and
prints requests, error rate, RPS, and ETA to the given io.Writer."
```

---

### Task 6: Write Stop and edge-case tests

**Files:**
- Modify: `internal/load/progress_test.go`

- [ ] **Step 1: Write the stop-clean test**

Add to `internal/load/progress_test.go`:

```go
func TestProgressReporter_StopClean(t *testing.T) {
	t.Parallel()

	var counters LiveCounters
	var buf bytes.Buffer

	reporter := NewProgressReporter(&counters, 5*time.Second, &buf)
	reporter.Start()

	// Let it tick at least once.
	time.Sleep(1100 * time.Millisecond)

	// Stop must return promptly without hanging.
	done := make(chan struct{})
	go func() {
		reporter.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success — stopped cleanly.
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return — possible goroutine leak")
	}
}
```

- [ ] **Step 2: Write the zero-requests edge case test**

Add to `internal/load/progress_test.go`:

```go
func TestProgressReporter_ZeroRequests(t *testing.T) {
	t.Parallel()

	var counters LiveCounters // zero values
	var buf bytes.Buffer

	reporter := NewProgressReporter(&counters, 10*time.Second, &buf)
	reporter.startTime = time.Now().Add(-2 * time.Second)

	// Must not panic on division by zero.
	reporter.printStatus()

	output := buf.String()
	if !strings.Contains(output, "0 reqs") {
		t.Errorf("output missing zero request count: %q", output)
	}
	if !strings.Contains(output, "0.0% err") {
		t.Errorf("output missing zero error rate: %q", output)
	}
}
```

- [ ] **Step 3: Run all progress tests with race detector**

Run: `go test -v -race -run TestProgressReporter ./internal/load/`
Expected: All PASS with no race warnings.

- [ ] **Step 4: Commit**

```bash
git add internal/load/progress_test.go
git commit -m "test(load): add stop-clean and zero-requests tests for ProgressReporter (#15)"
```

---

### Task 7: Create PR 2

- [ ] **Step 1: Run full test suite**

Run: `go test -v -race ./internal/load/`
Expected: All tests PASS.

Run: `make lint`
Expected: 0 issues.

- [ ] **Step 2: Push branch and create PR**

```bash
git push -u origin issue/15-progress-reporter
```

Create PR with:
- Title: `feat(load): add ProgressReporter with time.Ticker (#15)`
- Body: describe the reporter design, link to issue #15, include test plan

- [ ] **Step 3: Wait for review / run self-review**

Check line count: if > 50 lines, wait for copilot review or run `/code-review`.

---

## PR 3: CLI Wiring (`issue/15-cli-progress`)

Branch from: `main` (after PR 2 is merged)

### Task 8: Wire reporter into runWithLoad

**Files:**
- Modify: `cmd/proficiency/run.go`

- [ ] **Step 1: Add the reporter to runWithLoad()**

In `cmd/proficiency/run.go`, in the `runWithLoad()` function, add the reporter after creating the runner (around line 124, after `runner := load.NewRunner(runnerCfg)`):

```go
	runner := load.NewRunner(runnerCfg)

	reporter := load.NewProgressReporter(&runner.Counters, cfg.Duration, os.Stderr)
	reporter.Start()
	defer reporter.Stop()
```

Add a `fmt.Fprintln(os.Stderr)` after `reporter.Stop()` returns to clear the `\r` line. Use a named defer to sequence them:

```go
	reporter := load.NewProgressReporter(&runner.Counters, cfg.Duration, os.Stderr)
	reporter.Start()
	defer func() {
		reporter.Stop()
		fmt.Fprintln(os.Stderr) // newline after \r status line
	}()
```

The `"github.com/tuxerrante/proficiency/internal/load"` import is already present.

- [ ] **Step 2: Run full test suite**

Run: `go test -v -race ./...`
Expected: All tests PASS.

Run: `make lint`
Expected: 0 issues.

- [ ] **Step 3: Check escape analysis**

Run: `go build -gcflags='-m' ./cmd/proficiency 2>&1 | grep -i 'escape\|heap' | head -20`
Expected: No unexpected escapes from the LiveCounters or ProgressReporter. The `LiveCounters` struct lives on `Runner` which is heap-allocated via `NewRunner` — that's expected and fine.

- [ ] **Step 4: Manual smoke test**

```bash
make build-only
(cd e2e/testserver && go run .) &
./proficiency --openapi ./e2e/openapi.yaml --target http://localhost:8080 --duration 10s --concurrency 5 --rps 50
kill %1
```

Expected: Status line updates every second with format `[Xs/10s] N reqs | X.X% err | X RPS | ETA Xs`.

- [ ] **Step 5: Commit**

```bash
git add cmd/proficiency/run.go
git commit -m "feat: wire live progress reporter into CLI (#15)

During load tests, a status line now updates every second showing
requests, error rate, RPS, and ETA. Closes #15."
```

---

### Task 9: Create PR 3

- [ ] **Step 1: Push branch and create PR**

```bash
git push -u origin issue/15-cli-progress
```

Create PR with:
- Title: `feat: wire live progress reporter into CLI (#15)`
- Body: link to issue #15, note that it closes the issue, include smoke test output

- [ ] **Step 2: Wait for review / run self-review**

- [ ] **Step 3: After merge, update issue #15 with results**

Comment on issue #15 with:
- Links to all 3 merged PRs
- Summary of what was implemented
- Escape analysis output
- Race detector confirmation
