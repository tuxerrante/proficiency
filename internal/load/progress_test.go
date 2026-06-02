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
