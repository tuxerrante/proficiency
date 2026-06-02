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

	// Calling Stop() again must not panic (sync.Once protection).
	reporter.Stop()
}

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
