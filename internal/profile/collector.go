// Package profile provides pprof profile collection functionality.
// It connects to target services exposing /debug/pprof endpoints and retrieves
// CPU, heap, and block profiles for performance analysis.
package profile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Type represents the kind of pprof profile to collect.
type Type string

// defaultOutputDir is the default directory for storing collected profiles.
const defaultOutputDir = "./profiles"

// Supported profile types.
const (
	ProfileCPU       Type = "profile"   // CPU profile (requires duration parameter)
	ProfileHeap      Type = "heap"      // Heap memory profile
	ProfileBlock     Type = "block"     // Blocking profile
	ProfileGoroutine Type = "goroutine" // Goroutine profile
)

// CollectorConfig holds configuration for profile collection.
type CollectorConfig struct {
	TargetURL   string
	OutputDir   string
	CPUDuration time.Duration
	Timeout     time.Duration
}

// DefaultCollectorConfig returns a CollectorConfig with sensible defaults.
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		OutputDir:   defaultOutputDir,
		CPUDuration: 30 * time.Second,
		Timeout:     60 * time.Second,
	}
}

// CollectedProfile represents a successfully collected profile.
type CollectedProfile struct {
	Type     Type
	FilePath string
	Size     int64
	Duration time.Duration
}

// Collector handles retrieving pprof profiles from a target service.
type Collector struct {
	config CollectorConfig
	client *http.Client
}

// NewCollector creates a profile collector with the given configuration.
func NewCollector(cfg CollectorConfig) (*Collector, error) {
	if cfg.TargetURL == "" {
		return nil, errors.New("TargetURL is required")
	}

	if cfg.OutputDir == "" {
		cfg.OutputDir = defaultOutputDir
	}

	if cfg.CPUDuration == 0 {
		cfg.CPUDuration = 30 * time.Second
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = cfg.CPUDuration + 30*time.Second
	}

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

// collect fetches a profile from the given URL path and saves it to disk.
func (c *Collector) collect(ctx context.Context, profileType Type, urlPath string, filenamePrefix string) (*CollectedProfile, error) {
	url := c.config.TargetURL + urlPath
	start := time.Now()

	data, err := c.fetchProfile(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("collecting %s profile: %w", filenamePrefix, err)
	}

	filename := fmt.Sprintf("%s_%d.pprof", filenamePrefix, time.Now().UnixNano())
	filePath := filepath.Join(c.config.OutputDir, filename)

	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return nil, fmt.Errorf("writing %s profile to %s: %w", filenamePrefix, filePath, err)
	}

	return &CollectedProfile{
		Type:     profileType,
		FilePath: filePath,
		Size:     int64(len(data)),
		Duration: time.Since(start),
	}, nil
}

// CollectCPU retrieves a CPU profile from the target service.
// The profile collection takes at least CPUDuration seconds.
func (c *Collector) CollectCPU(ctx context.Context) (*CollectedProfile, error) {
	seconds := int(c.config.CPUDuration.Seconds())
	path := fmt.Sprintf("/debug/pprof/profile?seconds=%d", seconds)
	return c.collect(ctx, ProfileCPU, path, "cpu")
}

// CollectHeap retrieves a heap memory profile from the target service.
func (c *Collector) CollectHeap(ctx context.Context) (*CollectedProfile, error) {
	return c.collect(ctx, ProfileHeap, "/debug/pprof/heap", "heap")
}

// CollectBlock retrieves a blocking profile from the target service.
func (c *Collector) CollectBlock(ctx context.Context) (*CollectedProfile, error) {
	return c.collect(ctx, ProfileBlock, "/debug/pprof/block", "block")
}

// CollectGoroutine retrieves a goroutine profile from the target service.
func (c *Collector) CollectGoroutine(ctx context.Context) (*CollectedProfile, error) {
	return c.collect(ctx, ProfileGoroutine, "/debug/pprof/goroutine?debug=0", "goroutine")
}

// CollectByType dispatches to the appropriate collection method based on profile type.
func (c *Collector) CollectByType(ctx context.Context, profileType Type) (*CollectedProfile, error) {
	switch profileType {
	case ProfileCPU:
		return c.CollectCPU(ctx)
	case ProfileHeap:
		return c.CollectHeap(ctx)
	case ProfileBlock:
		return c.CollectBlock(ctx)
	case ProfileGoroutine:
		return c.CollectGoroutine(ctx)
	default:
		return nil, fmt.Errorf("unknown profile type: %s", profileType)
	}
}

// CollectSeries collects samples of the given profile type at regular intervals.
// It stops when ctx is cancelled or maxSamples is reached (0 = unlimited).
// On context cancellation, it returns the samples collected so far (not an error).
func (c *Collector) CollectSeries(ctx context.Context, profileType Type, interval time.Duration, maxSamples int) ([]*CollectedProfile, error) {
	var profiles []*CollectedProfile

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Collect the first sample immediately
	p, err := c.CollectByType(ctx, profileType)
	if err != nil {
		if ctx.Err() != nil {
			return profiles, nil
		}
		return nil, fmt.Errorf("collecting initial %s sample: %w", profileType, err)
	}
	profiles = append(profiles, p)

	for {
		if maxSamples > 0 && len(profiles) >= maxSamples {
			return profiles, nil
		}

		select {
		case <-ctx.Done():
			return profiles, nil
		case <-ticker.C:
			p, err := c.CollectByType(ctx, profileType)
			if err != nil {
				if ctx.Err() != nil {
					return profiles, nil
				}
				return nil, fmt.Errorf("collecting %s sample %d: %w", profileType, len(profiles)+1, err)
			}
			profiles = append(profiles, p)
		}
	}
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

	const maxProfileSize = 256 << 20 // 256MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxProfileSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if int64(len(data)) > maxProfileSize {
		return nil, fmt.Errorf("profile from %s exceeds %dMB limit", url, maxProfileSize>>20)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("empty profile returned from %s", url)
	}

	return data, nil
}

// userNameToType maps user-facing type names to internal Type constants.
// The pprof endpoint for CPU is "/debug/pprof/profile", but users type "cpu".
var userNameToType = map[string]Type{
	"cpu":                    ProfileCPU,
	string(ProfileHeap):      ProfileHeap,
	string(ProfileBlock):     ProfileBlock,
	string(ProfileGoroutine): ProfileGoroutine,
}

// ParseProfileTypes parses a comma-separated string of profile type names
// (e.g. "goroutine,heap") into a deduplicated slice of Type values.
func ParseProfileTypes(s string) ([]Type, error) {
	if s == "" {
		return nil, nil
	}

	seen := make(map[Type]bool)
	var types []Type

	for part := range strings.SplitSeq(s, ",") {
		name := strings.TrimSpace(part)
		pt, ok := userNameToType[name]
		if !ok {
			return nil, fmt.Errorf("unknown profile type %q, supported: cpu, heap, block, goroutine", name)
		}
		if !seen[pt] {
			seen[pt] = true
			types = append(types, pt)
		}
	}

	return types, nil
}

// CheckPprofAvailable verifies that pprof endpoints are accessible on the target.
func (c *Collector) CheckPprofAvailable(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	url := c.config.TargetURL + "/debug/pprof/"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
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
