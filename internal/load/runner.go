// Package load provides HTTP load generation functionality.
// It executes concurrent requests against target endpoints with configurable
// rate limiting, concurrency, and duration controls.
package load

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tuxerrante/proficiency/internal/swagger"
	"golang.org/x/time/rate"
)

// Config holds the load test configuration parameters.
type Config struct {
	// Concurrency is the number of concurrent goroutines making requests.
	// Each goroutine maintains its own HTTP client connection pool.
	Concurrency int

	// RPS is the target requests per second across all goroutines.
	// The rate limiter distributes this evenly across workers.
	RPS int

	// Duration is the total time to run the load test.
	Duration time.Duration

	// Timeout is the maximum time to wait for a single request.
	Timeout time.Duration
}

// DefaultConfig returns a Config with sensible defaults for local testing.
func DefaultConfig() Config {
	return Config{
		Concurrency: 10,
		RPS:         100,
		Duration:    30 * time.Second,
		Timeout:     10 * time.Second,
	}
}

// Result contains the outcome of a single HTTP request.
type Result struct {
	Endpoint   string        // The endpoint path that was called
	Method     string        // HTTP method used
	StatusCode int           // Response status code
	Latency    time.Duration // Request duration
	Error      error         // Error if request failed
	Timestamp  time.Time     // When the request was initiated
}

// Stats aggregates results from a load test run.
type Stats struct {
	TotalRequests   int64                   // Total requests attempted
	SuccessCount    int64                   // Requests with 2xx status
	ErrorCount      int64                   // Requests that failed or returned non-2xx
	Duration        time.Duration           // Actual test duration
	EndpointLatency map[string]LatencyStats // Per-endpoint latency statistics
}

// LatencyStats contains latency percentiles for an endpoint.
type LatencyStats struct {
	Count int64
	Min   time.Duration
	Max   time.Duration
	Avg   time.Duration
	Total time.Duration
}

// Runner executes load tests against a target service.
//
// DESIGN DECISION: Using golang.org/x/time/rate for rate limiting because:
// - Token bucket algorithm provides smooth request distribution
// - Built-in burst handling prevents thundering herd
// - Well-tested and maintained by the Go team
//
// ALTERNATIVE: Custom rate limiter using time.Ticker could provide more control
// but would require significant testing to match the robustness of x/time/rate.
//
// ALTERNATIVE: vegeta (github.com/tsenart/vegeta) is a full-featured load testing
// library, but adds significant dependency weight for features we don't need.
// We implement only what's necessary for our profiling use case.
type Runner struct {
	client  *http.Client
	config  Config
	limiter *rate.Limiter
}

// NewRunner creates a load test runner with the given configuration.
//
// BEHAVIOR:
// - Creates an HTTP client with connection pooling sized for concurrency
// - Initializes rate limiter to enforce RPS across all workers
// - Client reuses connections via keep-alive for efficiency
func NewRunner(cfg Config) *Runner {
	transport := &http.Transport{
		MaxIdleConns:        cfg.Concurrency * 2,
		MaxIdleConnsPerHost: cfg.Concurrency,
		IdleConnTimeout:     90 * time.Second,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}

	// Burst allows small spikes while maintaining average RPS
	limiter := rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Concurrency)

	return &Runner{
		client:  client,
		config:  cfg,
		limiter: limiter,
	}
}

// Run executes the load test against the target URL for the configured duration.
// It distributes requests across all provided endpoints using round-robin scheduling.
//
// BEHAVIOR:
// - Spawns cfg.Concurrency goroutines, each making sequential requests
// - Rate limiter enforces global RPS limit across all goroutines
// - Continues until context is cancelled or duration expires
// - Collects all results for latency analysis
//
// CONCURRENCY MODEL:
// - Each goroutine has exclusive access to its iteration of the endpoint slice
// - Results are collected via channel to avoid lock contention
// - WaitGroup ensures clean shutdown before returning stats
//
// ERROR HANDLING:
// - Network errors are recorded in results but don't stop the test
// - Context cancellation triggers graceful shutdown of all workers
func (r *Runner) Run(ctx context.Context, targetURL string, endpoints []swagger.Endpoint) (*Stats, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no endpoints provided for load test")
	}

	ctx, cancel := context.WithTimeout(ctx, r.config.Duration)
	defer cancel()

	resultsCh := make(chan Result, r.config.RPS*int(r.config.Duration.Seconds()))
	var wg sync.WaitGroup

	startTime := time.Now()

	// Spawn worker goroutines
	for i := 0; i < r.config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r.worker(ctx, targetURL, endpoints, resultsCh, workerID)
		}(i)
	}

	// Close results channel when all workers complete
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	// Collect and aggregate results
	stats := &Stats{
		EndpointLatency: make(map[string]LatencyStats),
	}

	for result := range resultsCh {
		stats.TotalRequests++

		if result.Error != nil || result.StatusCode < 200 || result.StatusCode >= 300 {
			stats.ErrorCount++
		} else {
			stats.SuccessCount++
		}

		// Update per-endpoint latency stats
		key := result.Method + " " + result.Endpoint
		ls := stats.EndpointLatency[key]
		ls.Count++
		ls.Total += result.Latency
		if ls.Min == 0 || result.Latency < ls.Min {
			ls.Min = result.Latency
		}
		if result.Latency > ls.Max {
			ls.Max = result.Latency
		}
		stats.EndpointLatency[key] = ls
	}

	stats.Duration = time.Since(startTime)

	// Calculate averages
	for key, ls := range stats.EndpointLatency {
		if ls.Count > 0 {
			ls.Avg = ls.Total / time.Duration(ls.Count)
			stats.EndpointLatency[key] = ls
		}
	}

	return stats, nil
}

// worker is a goroutine that makes sequential requests to endpoints.
// It respects the rate limiter and stops when context is cancelled.
func (r *Runner) worker(ctx context.Context, targetURL string, endpoints []swagger.Endpoint, results chan<- Result, workerID int) {
	endpointIdx := workerID % len(endpoints) // Start at different endpoints to distribute load

	for {
		// Check for cancellation before waiting on rate limiter
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Wait for rate limiter permission
		if err := r.limiter.Wait(ctx); err != nil {
			return // Context cancelled
		}

		endpoint := endpoints[endpointIdx]
		endpointIdx = (endpointIdx + 1) % len(endpoints)

		result := r.makeRequest(ctx, targetURL, endpoint)

		select {
		case results <- result:
		case <-ctx.Done():
			return
		}
	}
}

// makeRequest executes a single HTTP request and returns the result.
func (r *Runner) makeRequest(ctx context.Context, targetURL string, endpoint swagger.Endpoint) Result {
	path := swagger.ResolvePath(endpoint.Path, endpoint.Parameters, nil)
	url := targetURL + path

	result := Result{
		Endpoint:  endpoint.Path,
		Method:    endpoint.Method,
		Timestamp: time.Now(),
	}

	req, err := http.NewRequestWithContext(ctx, endpoint.Method, url, nil)
	if err != nil {
		result.Error = fmt.Errorf("creating request: %w", err)
		return result
	}

	start := time.Now()
	resp, err := r.client.Do(req)
	result.Latency = time.Since(start)

	if err != nil {
		result.Error = fmt.Errorf("executing request: %w", err)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	// Drain body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)

	result.StatusCode = resp.StatusCode
	return result
}

// RequestCount returns the total number of requests made during a test.
// This is useful for progress reporting during long-running tests.
type RequestCounter struct {
	count atomic.Int64
}

func (rc *RequestCounter) Increment() {
	rc.count.Add(1)
}

func (rc *RequestCounter) Count() int64 {
	return rc.count.Load()
}
