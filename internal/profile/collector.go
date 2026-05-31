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
	"time"
)

// Type represents the kind of pprof profile to collect.
type Type string

// defaultOutputDir is the default directory for storing collected profiles.
const defaultOutputDir = "./profiles"

// Supported profile types.
const (
	ProfileCPU   Type = "profile" // CPU profile (requires duration parameter)
	ProfileHeap  Type = "heap"    // Heap memory profile
	ProfileBlock Type = "block"   // Blocking profile
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

	filename := fmt.Sprintf("%s_%d.pprof", filenamePrefix, time.Now().Unix())
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
