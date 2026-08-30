package load

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/tuxerrante/proficiency/internal/openapi"
)

func TestRunner_Run(t *testing.T) {
	// Track request counts per endpoint
	var requestCount atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	endpoints := []openapi.Endpoint{
		{Method: "GET", Path: "/pets"},
		{Method: "GET", Path: "/health"},
	}

	cfg := Config{
		Concurrency: 2,
		RPS:         50,
		Duration:    1 * time.Second,
		Timeout:     5 * time.Second,
	}

	runner := NewRunner(cfg)

	ctx := context.Background()
	stats, err := runner.Run(ctx, server.URL, endpoints)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify we got some requests
	if stats.TotalRequests == 0 {
		t.Error("expected some requests to be made")
	}

	// With 50 RPS for 1 second, we expect roughly 50 requests (with some tolerance)
	if stats.TotalRequests < 30 || stats.TotalRequests > 70 {
		t.Errorf("expected roughly 50 requests, got %d", stats.TotalRequests)
	}

	// All should be successful. Requests canceled by the run deadline are not
	// completed results and must not enter the aggregate.
	if stats.SuccessCount != stats.TotalRequests {
		t.Errorf("expected all requests to succeed, got %d/%d",
			stats.SuccessCount, stats.TotalRequests)
	}
	if stats.ErrorCount != 0 {
		t.Errorf("expected no errors, got %d", stats.ErrorCount)
	}

	// Verify we have latency stats for endpoints
	if len(stats.EndpointLatency) == 0 {
		t.Error("expected endpoint latency stats")
	}
	for endpoint, latency := range stats.EndpointLatency {
		if latency.P50Bound == 0 || latency.P95Bound == 0 || latency.P99Bound == 0 {
			t.Errorf("%s percentiles were not populated: %+v", endpoint, latency)
		}
		var samples int64
		for _, count := range latency.Histogram.Buckets {
			samples += count
		}
		samples += latency.Histogram.Overflow
		if samples != latency.Count {
			t.Errorf("%s histogram samples = %d, want %d", endpoint, samples, latency.Count)
		}
	}
}

func TestLatencyHistogram(t *testing.T) {
	var histogram LatencyHistogram
	for _, latency := range []time.Duration{
		500 * time.Microsecond,
		time.Millisecond,
		time.Millisecond + 1,
		6 * time.Millisecond,
		25 * time.Millisecond,
		75 * time.Millisecond,
		250 * time.Millisecond,
		750 * time.Millisecond,
		2 * time.Second,
		8 * time.Second,
		12 * time.Second,
	} {
		histogram.Observe(latency)
	}

	wantBuckets := [16]int64{0, 0, 1, 1, 1, 0, 1, 1, 0, 1, 1, 0, 1, 1, 0, 1}
	if histogram.Buckets != wantBuckets {
		t.Fatalf("buckets = %v, want %v", histogram.Buckets, wantBuckets)
	}
	if histogram.Overflow != 1 {
		t.Fatalf("overflow = %d, want 1", histogram.Overflow)
	}

	tests := []struct {
		name       string
		percentile int
		want       time.Duration
	}{
		{name: "p50", percentile: 50, want: 100 * time.Millisecond},
		{name: "p95", percentile: 95, want: 12 * time.Second},
		{name: "p99", percentile: 99, want: 12 * time.Second},
		{name: "invalid low", percentile: 0, want: 0},
		{name: "invalid high", percentile: 101, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := histogram.PercentileUpperBound(test.percentile, 12*time.Second); got != test.want {
				t.Fatalf("PercentileUpperBound(%d) = %v, want %v", test.percentile, got, test.want)
			}
		})
	}

	var singleBucket LatencyHistogram
	singleBucket.Observe(3 * time.Millisecond)
	if got := singleBucket.PercentileUpperBound(99, 3*time.Millisecond); got != 5*time.Millisecond {
		t.Fatalf("single-sample p99 bound = %v, want 5ms", got)
	}

	var empty LatencyHistogram
	if got := empty.PercentileUpperBound(99, 0); got != 0 {
		t.Fatalf("empty p99 bound = %v, want 0", got)
	}
}

func TestLatencyHistogramObserveAllocations(t *testing.T) {
	var histogram LatencyHistogram
	allocations := testing.AllocsPerRun(1000, func() {
		histogram.Observe(25 * time.Millisecond)
	})
	if allocations != 0 {
		t.Fatalf("Observe() allocations = %v, want 0", allocations)
	}
}

func TestLatencyStatsHasNoPadding(t *testing.T) {
	var stats LatencyStats
	want := 8*unsafe.Sizeof(int64(0)) + unsafe.Sizeof(stats.Histogram)
	if got := unsafe.Sizeof(stats); got != want {
		t.Fatalf("LatencyStats size = %d, want packed size %d", got, want)
	}
}

func BenchmarkLatencyHistogramObserve(b *testing.B) {
	for _, latency := range []time.Duration{
		500 * time.Microsecond,
		25 * time.Millisecond,
		5 * time.Second,
		15 * time.Second,
	} {
		b.Run(latency.String(), func(b *testing.B) {
			var histogram LatencyHistogram
			b.ReportAllocs()
			for b.Loop() {
				histogram.Observe(latency)
			}
		})
	}
}

func TestRunner_Run_NoEndpoints(t *testing.T) {
	cfg := DefaultConfig()
	runner := NewRunner(cfg)

	ctx := context.Background()
	_, err := runner.Run(ctx, "http://localhost:8080", nil)
	if err == nil {
		t.Error("expected error for no endpoints")
	}
}

func TestRunner_Run_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoints := []openapi.Endpoint{
		{Method: "GET", Path: "/slow"},
	}

	cfg := Config{
		Concurrency: 2,
		RPS:         10,
		Duration:    10 * time.Second, // Long duration
		Timeout:     5 * time.Second,
	}

	runner := NewRunner(cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after 500ms
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := runner.Run(ctx, server.URL, endpoints)
	elapsed := time.Since(start)

	// Should complete before duration due to cancellation
	if elapsed > 2*time.Second {
		t.Errorf("expected early termination, took %v", elapsed)
	}

	// Error is nil because cancellation is graceful
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunner_Run_DropsRunCancellationError(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	cfg := Config{
		Concurrency: 1,
		RPS:         100,
		Duration:    time.Second,
		Timeout:     time.Second,
	}
	runner := NewRunner(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancelledAfterStart := make(chan bool, 1)
	go func() {
		select {
		case <-started:
			cancel()
			cancelledAfterStart <- true
		case <-time.After(500 * time.Millisecond):
			cancel()
			cancelledAfterStart <- false
		}
	}()
	stats, err := runner.Run(
		ctx,
		server.URL,
		[]openapi.Endpoint{{Method: "GET", Path: "/wait"}},
	)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !<-cancelledAfterStart {
		t.Fatal("request did not reach the handler before cancellation")
	}
	if stats.TotalRequests != 0 || stats.ErrorCount != 0 {
		t.Fatalf("deadline-canceled result was aggregated: %+v", stats)
	}
}

func TestRunner_Run_CountsRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	cfg := Config{
		Concurrency: 1,
		RPS:         100,
		Duration:    150 * time.Millisecond,
		Timeout:     25 * time.Millisecond,
	}
	runner := NewRunner(cfg)
	stats, err := runner.Run(
		context.Background(),
		server.URL,
		[]openapi.Endpoint{{Method: "GET", Path: "/wait"}},
	)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if stats.ErrorCount == 0 || stats.ErrorCount != stats.TotalRequests {
		t.Fatalf("request timeout results were not aggregated: %+v", stats)
	}
}

func TestRunner_Run_ServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	endpoints := []openapi.Endpoint{
		{Method: "GET", Path: "/error"},
	}

	cfg := Config{
		Concurrency: 1,
		RPS:         20,
		Duration:    500 * time.Millisecond,
		Timeout:     5 * time.Second,
	}

	runner := NewRunner(cfg)

	ctx := context.Background()
	stats, err := runner.Run(ctx, server.URL, endpoints)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// All requests should be counted as errors (non-2xx)
	if stats.ErrorCount != stats.TotalRequests {
		t.Errorf("expected all requests to be errors, got %d/%d",
			stats.ErrorCount, stats.TotalRequests)
	}

	if stats.SuccessCount != 0 {
		t.Errorf("expected no successes, got %d", stats.SuccessCount)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Concurrency != 10 {
		t.Errorf("expected concurrency 10, got %d", cfg.Concurrency)
	}

	if cfg.RPS != 100 {
		t.Errorf("expected RPS 100, got %d", cfg.RPS)
	}

	if cfg.Duration != 30*time.Second {
		t.Errorf("expected duration 30s, got %v", cfg.Duration)
	}

	if cfg.Timeout != 10*time.Second {
		t.Errorf("expected timeout 10s, got %v", cfg.Timeout)
	}
}

// Regression: Result.Timestamp was removed — verify Result has no Timestamp field.
func TestResult_NoTimestampField(t *testing.T) {
	rt := reflect.TypeFor[Result]()
	_, found := rt.FieldByName("Timestamp")
	if found {
		t.Error("Result should not have a Timestamp field (dead code, was set but never read)")
	}
}

func TestResultBufferSize(t *testing.T) {
	if got := resultBufferSize(2); got != 4 {
		t.Fatalf("resultBufferSize(2) = %d, want 4", got)
	}
	if got := resultBufferSize(100000); got != 4096 {
		t.Fatalf("resultBufferSize(100000) = %d, want 4096", got)
	}
}

func TestClassifyRequestErrorPreservesTransportError(t *testing.T) {
	transportErr := errors.New("transport failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, canceledByRun := classifyRequestError(transportErr, ctx)
	if canceledByRun {
		t.Fatal("transport error was classified as run cancellation")
	}
	if !errors.Is(got, transportErr) {
		t.Fatalf("error = %v, want wrapped transport error", got)
	}
}

// Regression: workers must not block on a full channel — ctx.Done select prevents deadlock.
func TestRunner_Run_NoDeadlockOnSlowConsumer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{
		Concurrency: 5,
		RPS:         1000,
		Duration:    200 * time.Millisecond,
		Timeout:     2 * time.Second,
	}

	runner := NewRunner(cfg)
	ctx := context.Background()
	endpoints := []openapi.Endpoint{{Method: "GET", Path: "/fast"}}

	done := make(chan struct{})
	go func() {
		_, _ = runner.Run(ctx, server.URL, endpoints)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete — possible deadlock")
	}
}

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

func TestRunner_MakeRequest_WritesJSONBodyForWriteMethods(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"name":"test"}`)
	methods := []string{http.MethodPost, http.MethodPut, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method {
					t.Errorf("expected method %s, got %s", method, r.Method)
					return
				}

				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("failed reading body: %v", err)
					return
				}
				if string(body) != string(payload) {
					t.Errorf("unexpected request body: got %q want %q", string(body), string(payload))
					return
				}

				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("expected Content-Type application/json, got %q", got)
					return
				}

				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()

			runner := NewRunner(Config{
				Concurrency: 1,
				RPS:         1,
				Duration:    time.Second,
				Timeout:     2 * time.Second,
			})

			result := runner.makeRequest(context.Background(), server.URL, openapi.Endpoint{
				Method:      method,
				Path:        "/items",
				HasBody:     true,
				ContentType: "application/json",
				Body:        payload,
			})

			if result.Error != nil {
				t.Fatalf("makeRequest failed: %v", result.Error)
			}
			if result.StatusCode != http.StatusCreated {
				t.Fatalf("expected status %d, got %d", http.StatusCreated, result.StatusCode)
			}
		})
	}
}

func TestRunner_MakeRequest_IgnoresBodyForReadMethods(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"name":"test"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected method GET, got %s", r.Method)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed reading body: %v", err)
			return
		}
		if len(body) != 0 {
			t.Errorf("expected empty request body, got %q", string(body))
			return
		}
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("expected no Content-Type header, got %q", got)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner := NewRunner(Config{
		Concurrency: 1,
		RPS:         1,
		Duration:    time.Second,
		Timeout:     2 * time.Second,
	})

	result := runner.makeRequest(context.Background(), server.URL, openapi.Endpoint{
		Method:      http.MethodGet,
		Path:        "/items",
		HasBody:     true,
		ContentType: "application/json",
		Body:        payload,
	})

	if result.Error != nil {
		t.Fatalf("makeRequest failed: %v", result.Error)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, result.StatusCode)
	}
}

func TestRunner_MakeRequest_WritesBodyForVendorJSONContentType(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"name":"test"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed reading body: %v", err)
			return
		}
		if string(body) != string(payload) {
			t.Errorf("unexpected request body: got %q want %q", string(body), string(payload))
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/problem+json" {
			t.Errorf("expected Content-Type application/problem+json, got %q", got)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	runner := NewRunner(Config{
		Concurrency: 1,
		RPS:         1,
		Duration:    time.Second,
		Timeout:     2 * time.Second,
	})

	result := runner.makeRequest(context.Background(), server.URL, openapi.Endpoint{
		Method:      http.MethodPost,
		Path:        "/items",
		HasBody:     true,
		ContentType: "application/problem+json",
		Body:        payload,
	})

	if result.Error != nil {
		t.Fatalf("makeRequest failed: %v", result.Error)
	}
	if result.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, result.StatusCode)
	}
}
