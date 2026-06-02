package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/tuxerrante/proficiency/internal/profile"
)

// Config holds all CLI configuration parsed from flags.
type Config struct {
	OpenAPIPath    string
	TargetURL      string
	PprofURL       string // Separate pprof target; defaults to TargetURL.
	Duration       time.Duration
	Concurrency    int
	RPS            int
	OutputDir      string
	CPUDuration    time.Duration
	SkipLoad       bool
	Version        bool
	FailOn         string
	SampleInterval time.Duration
	SampleCount    int
	ProfileTypes   string
	NoProgress     bool
}

// parseFlags defines and parses CLI flags.
//
// Uses the standard library flag package — no external dependencies,
// familiar to Go developers, sufficient for our flag set.
func parseFlags() Config {
	cfg := Config{}

	flag.StringVar(&cfg.OpenAPIPath, "openapi", "", "Path to OpenAPI spec file (required)")
	flag.StringVar(&cfg.TargetURL, "target", "", "Target service URL, e.g., http://localhost:8080 (required)")
	flag.StringVar(&cfg.PprofURL, "pprof-target", "", "Pprof target URL if different from --target (default: same as --target)")
	flag.DurationVar(&cfg.Duration, "duration", 30*time.Second, "Load test duration")
	flag.IntVar(&cfg.Concurrency, "concurrency", 10, "Number of concurrent workers")
	flag.IntVar(&cfg.RPS, "rps", 100, "Target requests per second")
	flag.StringVar(&cfg.OutputDir, "output", "./profiles", "Directory for profile output")
	flag.DurationVar(&cfg.CPUDuration, "cpu-duration", 30*time.Second, "CPU profile collection duration")
	flag.BoolVar(&cfg.SkipLoad, "skip-load", false, "Skip load generation, only collect profiles")
	flag.BoolVar(&cfg.Version, "version", false, "Print version and exit")
	flag.StringVar(&cfg.FailOn, "fail-on", "",
		"Comma-separated thresholds for CI gating (e.g. cpu:30,alloc:50). Exit non-zero if any function exceeds the threshold percentage.")
	flag.DurationVar(&cfg.SampleInterval, "sample-interval", 0,
		"Interval between profile samples for time-series collection (e.g. 2s). Enables watch mode when used with --skip-load.")
	flag.IntVar(&cfg.SampleCount, "sample-count", 0,
		"Maximum number of samples to collect (0 = unlimited, stops on --duration or Ctrl+C)")
	flag.StringVar(&cfg.ProfileTypes, "profile-types", "cpu,heap,block",
		"Comma-separated profile types to collect: cpu, heap, block, goroutine")
	flag.BoolVar(&cfg.NoProgress, "no-progress", false,
		"Disable live progress status line (auto-disabled when stderr is not a terminal)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: proficiency [options]\n\n")
		fmt.Fprintf(os.Stderr, "Proficiency profiles your Go API by generating load and collecting pprof data.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  proficiency --openapi api.yaml --target http://localhost:6060 --duration 30s\n\n")
	}

	flag.Parse()
	return cfg
}

// validateConfig ensures required fields are set and values are sensible.
// It checks flag consistency across the three operational modes:
// load (default), watch (--skip-load + --sample-interval), and snapshot (--skip-load).
func validateConfig(cfg Config) error {
	if cfg.TargetURL == "" {
		return errors.New("--target is required")
	}

	if !cfg.SkipLoad {
		if cfg.OpenAPIPath == "" {
			return errors.New("--openapi is required when load generation is enabled")
		}
		if _, err := os.Stat(cfg.OpenAPIPath); err != nil {
			return fmt.Errorf("OpenAPI spec not accessible: %w", err)
		}
		if cfg.SampleInterval > 0 {
			return errors.New("--sample-interval requires --skip-load (watch mode does not generate load)")
		}
	}

	if cfg.Duration <= 0 {
		return errors.New("--duration must be positive")
	}

	if cfg.Concurrency <= 0 {
		return errors.New("--concurrency must be positive")
	}

	if cfg.RPS <= 0 {
		return errors.New("--rps must be positive")
	}

	if cfg.SampleInterval > 0 && cfg.SampleInterval < 500*time.Millisecond {
		return errors.New("--sample-interval must be at least 500ms")
	}

	if cfg.SampleCount > 0 && cfg.SampleInterval == 0 {
		return errors.New("--sample-count requires --sample-interval")
	}

	profileTypes, err := profile.ParseProfileTypes(cfg.ProfileTypes)
	if err != nil {
		return fmt.Errorf("invalid --profile-types: %w", err)
	}
	if len(profileTypes) == 0 {
		return errors.New("--profile-types must specify at least one type")
	}

	if cfg.SampleInterval > 0 && slices.Contains(profileTypes, profile.ProfileCPU) {
		return errors.New("CPU profiles are incompatible with --sample-interval (each sample blocks for --cpu-duration). Use goroutine, heap, or block instead")
	}

	return nil
}
