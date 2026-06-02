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
	stopOnce  sync.Once
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
// Safe to call multiple times.
func (p *ProgressReporter) Stop() {
	p.stopOnce.Do(func() { close(p.done) })
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

	remaining := max(p.duration-elapsed, 0)

	_, _ = fmt.Fprintf(p.w, "\r[%s/%s] %d reqs | %.1f%% err | %.0f RPS | ETA %s",
		elapsed, p.duration.Truncate(time.Second),
		reqs, errPct, rps,
		remaining.Truncate(time.Second))
}
