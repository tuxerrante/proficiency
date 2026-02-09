// Package profile provides pprof profile collection and analysis functionality.
// It connects to target services exposing /debug/pprof endpoints and retrieves
// CPU and heap profiles for performance analysis.
package profile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Type represents the kind of pprof profile to collect.
type Type string

// Supported profile types.
const (
	ProfileCPU       Type = "profile"   // CPU profile (requires duration parameter)
	ProfileHeap      Type = "heap"      // Heap memory profile
	ProfileGoroutine Type = "goroutine" // Goroutine stack traces
	ProfileBlock     Type = "block"     // Blocking profile
	ProfileMutex     Type = "mutex"     // Mutex contention profile
)

// CollectorConfig holds configuration for profile collection.
type CollectorConfig struct {
	// TargetURL is the base URL of the service exposing pprof endpoints.
	// Should NOT include the /debug/pprof path.
	TargetURL string

	// OutputDir is the directory where collected profiles will be saved.
	OutputDir string

	// CPUDuration is how long to collect CPU profile samples.
	// Default: 30 seconds.
	CPUDuration time.Duration

	// Timeout is the maximum time to wait for profile collection.
	// Should be greater than CPUDuration for CPU profiles.
	Timeout time.Duration
}

// DefaultCollectorConfig returns a CollectorConfig with sensible defaults.
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		OutputDir:   "./profiles",
		CPUDuration: 30 * time.Second,
		Timeout:     60 * time.Second,
	}
}

// CollectedProfile represents a successfully collected profile.
type CollectedProfile struct {
	Type     Type
	FilePath string
	Size     int64
	Duration time.Duration // Time taken to collect
}

// Collector handles retrieving pprof profiles from a target service.
//
// DESIGN DECISION: Collecting profiles via HTTP rather than in-process because:
// - Target service runs separately (often in container/remote)
// - HTTP is the standard pprof interface, works with any Go service
// - Decouples profiling tool from target service implementation
//
// ALTERNATIVE: For in-process profiling, use runtime/pprof directly.
// This would be faster but requires instrumenting the target service,
// which defeats the purpose of an external profiling tool.
//
// TRADEOFF: HTTP collection adds network latency and may miss very
// short-lived bottlenecks. For precise profiling, increase CPUDuration.
type Collector struct {
	config CollectorConfig
	client *http.Client
}

// NewCollector creates a profile collector with the given configuration.
//
// BEHAVIOR:
// - Creates HTTP client with timeout appropriate for profile collection
// - Ensures output directory exists (creates if necessary)
// - Validates that TargetURL is set.
func NewCollector(cfg CollectorConfig) (*Collector, error) {
	if cfg.TargetURL == "" {
		return nil, errors.New("TargetURL is required")
	}

	if cfg.OutputDir == "" {
		cfg.OutputDir = "./profiles"
	}

	if cfg.CPUDuration == 0 {
		cfg.CPUDuration = 30 * time.Second
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = cfg.CPUDuration + 30*time.Second
	}

	// Ensure output directory exists
	if err := os.MkdirAll(cfg.OutputDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating output directory %s: %w", cfg.OutputDir, err)
	}

	client := &http.Client{
		Timeout: cfg.Timeout,
	}

	return &Collector{
		config: cfg,
		client: client,
	}, nil
}

// CollectCPU retrieves a CPU profile from the target service.
// The profile collection takes at least CPUDuration seconds.
//
// BEHAVIOR:
// - Sends request to /debug/pprof/profile?seconds=N
// - Blocks for the duration of sample collection
// - Saves profile to OutputDir/cpu_<timestamp>.pprof
// - Returns metadata about the collected profile
//
// ERROR CONDITIONS:
// - Target not reachable (connection refused, timeout)
// - pprof endpoint not enabled on target
// - Insufficient permissions to write output file
// - Context cancellation during collection
//
// NOTE: The target service must import _ "net/http/pprof" and expose
// the debug handlers, typically via http.DefaultServeMux on port 6060.
func (c *Collector) CollectCPU(ctx context.Context) (*CollectedProfile, error) {
	seconds := int(c.config.CPUDuration.Seconds())
	url := fmt.Sprintf("%s/debug/pprof/profile?seconds=%d", c.config.TargetURL, seconds)

	start := time.Now()

	data, err := c.fetchProfile(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("collecting CPU profile: %w", err)
	}

	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("cpu_%d.pprof", timestamp)
	filePath := filepath.Join(c.config.OutputDir, filename)

	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return nil, fmt.Errorf("writing CPU profile to %s: %w", filePath, err)
	}

	return &CollectedProfile{
		Type:     ProfileCPU,
		FilePath: filePath,
		Size:     int64(len(data)),
		Duration: time.Since(start),
	}, nil
}

// CollectHeap retrieves a heap memory profile from the target service.
// Unlike CPU profiles, heap profiles are instantaneous snapshots.
//
// BEHAVIOR:
// - Sends request to /debug/pprof/heap
// - Returns immediately with current heap state
// - Saves profile to OutputDir/heap_<timestamp>.pprof
//
// NOTE: Heap profiles show live objects. For allocation profiling,
// use /debug/pprof/allocs instead (can be added as ProfileAllocs).
func (c *Collector) CollectHeap(ctx context.Context) (*CollectedProfile, error) {
	url := c.config.TargetURL + "/debug/pprof/heap"

	start := time.Now()

	data, err := c.fetchProfile(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("collecting heap profile: %w", err)
	}

	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("heap_%d.pprof", timestamp)
	filePath := filepath.Join(c.config.OutputDir, filename)

	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return nil, fmt.Errorf("writing heap profile to %s: %w", filePath, err)
	}

	return &CollectedProfile{
		Type:     ProfileHeap,
		FilePath: filePath,
		Size:     int64(len(data)),
		Duration: time.Since(start),
	}, nil
}

// CollectBlock retrieves a blocking profile from the target service.
// It captures goroutines blocked on synchronization primitives and I/O,
// making it the right profile type for diagnosing slow database operations,
// file I/O waits, and lock contention.
//
// NOTE: The target must call runtime.SetBlockProfileRate(n) with n > 0
// before the profiled operations occur, otherwise the profile will be empty.
func (c *Collector) CollectBlock(ctx context.Context) (*CollectedProfile, error) {
	url := c.config.TargetURL + "/debug/pprof/block"

	start := time.Now()

	data, err := c.fetchProfile(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("collecting block profile: %w", err)
	}

	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("block_%d.pprof", timestamp)
	filePath := filepath.Join(c.config.OutputDir, filename)

	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return nil, fmt.Errorf("writing block profile to %s: %w", filePath, err)
	}

	return &CollectedProfile{
		Type:     ProfileBlock,
		FilePath: filePath,
		Size:     int64(len(data)),
		Duration: time.Since(start),
	}, nil
}

// CollectAll retrieves both CPU and heap profiles.
// CPU profile is collected first (takes longer), then heap snapshot.
//
// BEHAVIOR:
// - Collects CPU profile for configured duration
// - Then immediately collects heap snapshot
// - Returns both profiles or error if either fails
//
// TRADEOFF: Sequential collection means heap profile is taken after
// CPU profiling completes. For truly simultaneous profiles, use
// separate goroutines (but beware of resource contention on target).
func (c *Collector) CollectAll(ctx context.Context) ([]*CollectedProfile, error) {
	profiles := make([]*CollectedProfile, 0, 2)

	cpu, err := c.CollectCPU(ctx)
	if err != nil {
		return nil, fmt.Errorf("CPU profile: %w", err)
	}
	profiles = append(profiles, cpu)

	heap, err := c.CollectHeap(ctx)
	if err != nil {
		return profiles, fmt.Errorf("heap profile (CPU succeeded): %w", err)
	}
	profiles = append(profiles, heap)

	return profiles, nil
}

// fetchProfile makes an HTTP request to retrieve profile data.
func (c *Collector) fetchProfile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("empty profile returned from %s", url)
	}

	return data, nil
}

// CheckPprofAvailable verifies that pprof endpoints are accessible on the target.
// This is useful for early validation before starting a long load test.
func (c *Collector) CheckPprofAvailable(ctx context.Context) error {
	url := c.config.TargetURL + "/debug/pprof/"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	// Use short timeout for availability check
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pprof endpoint not reachable at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("pprof not enabled on target (404 at %s). Ensure target imports _ \"net/http/pprof\"", url)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from pprof endpoint", resp.StatusCode)
	}

	return nil
}
