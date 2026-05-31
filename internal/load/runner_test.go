package load

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

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

	// All should be successful
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

// Regression: channel buffer must be bounded regardless of user input.
func TestRunner_ChannelBufferCapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{
		Concurrency: 100000,
		RPS:         100000,
		Duration:    100 * time.Millisecond,
		Timeout:     5 * time.Second,
	}

	runner := NewRunner(cfg)
	ctx := context.Background()
	endpoints := []openapi.Endpoint{{Method: "GET", Path: "/test"}}

	// Should not panic or allocate excessive memory.
	stats, err := runner.Run(ctx, server.URL, endpoints)
	if err != nil {
		t.Fatalf("Run failed with extreme config: %v", err)
	}
	if stats.TotalRequests == 0 {
		t.Error("expected at least one request")
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
