// Package main provides the CLI entry point for the proficiency tool.
// It orchestrates OpenAPI parsing, load generation, and profile collection.
//
// The CLI follows the Unix philosophy: do one thing well, be composable.
// Output is designed for both human readability and machine parsing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/openapi"
	"github.com/tuxerrante/proficiency/internal/profile"
)

// Version is set at build time via -ldflags.
var Version = "dev"

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
}

// main is the entry point. It parses flags, validates configuration,
// and orchestrates the profiling workflow.
//
// EXIT CODES:
// - 0: Success
// - 1: Configuration or validation error
// - 2: Runtime error (network, file I/O, etc.)
//
// SIGNAL HANDLING:
// - SIGINT/SIGTERM: Graceful shutdown, saves partial profiles if possible.
func main() {
	cfg := parseFlags()

	if cfg.Version {
		fmt.Printf("proficiency version %s\n", Version)
		os.Exit(0)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}
}

// parseFlags defines and parses CLI flags.
//
// DESIGN DECISION: Using standard library flag package because:
// - No external dependencies for basic CLI
// - Familiar to Go developers
// - Sufficient for our simple flag set
//
// ALTERNATIVE: cobra or urfave/cli provide subcommands and richer help,
// but add dependency weight. Consider if we add subcommands later.
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
		if _, err := os.Stat(cfg.OpenAPIPath); os.IsNotExist(err) {
			return fmt.Errorf("OpenAPI spec file not found: %s", cfg.OpenAPIPath)
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

	if cfg.SampleInterval > 0 && slices.Contains(profileTypes, profile.ProfileCPU) {
		return errors.New("CPU profiles are incompatible with --sample-interval (each sample blocks for --cpu-duration). Use goroutine, heap, or block instead")
	}

	return nil
}

// run executes the main profiling workflow.
//
// WORKFLOW:
// 1. Parse OpenAPI spec to extract endpoints (skipped in watch mode)
// 2. Verify pprof is available on target
// 3. Run load test AND collect profiles concurrently (or watch mode)
// 4. Print summary with analysis hints.
func run(ctx context.Context, cfg Config) error {
	var endpoints []openapi.Endpoint

	// Step 1: Parse OpenAPI spec (only when generating load)
	if !cfg.SkipLoad {
		fmt.Printf("Parsing OpenAPI spec: %s\n", cfg.OpenAPIPath)

		parser := openapi.NewParser()
		var err error
		endpoints, err = parser.ParseFile(ctx, cfg.OpenAPIPath)
		if err != nil {
			return fmt.Errorf("parsing OpenAPI spec: %w", err)
		}

		fmt.Printf("Parsed %d endpoints from %s\n", len(endpoints), cfg.OpenAPIPath)
		for _, ep := range endpoints {
			fmt.Printf("  %s %s\n", ep.Method, ep.Path)
		}
	}

	// Determine pprof target URL (defaults to --target if not specified).
	pprofURL := cfg.TargetURL
	if cfg.PprofURL != "" {
		pprofURL = cfg.PprofURL
	}

	// Step 2: Verify pprof availability
	fmt.Printf("\nVerifying pprof availability at %s...\n", pprofURL)

	collectorCfg := profile.CollectorConfig{
		TargetURL:   pprofURL,
		OutputDir:   cfg.OutputDir,
		CPUDuration: cfg.CPUDuration,
	}

	collector, err := profile.NewCollector(collectorCfg)
	if err != nil {
		return fmt.Errorf("creating profile collector: %w", err)
	}

	if err := collector.CheckPprofAvailable(ctx); err != nil {
		return fmt.Errorf("pprof check failed: %w", err)
	}
	fmt.Println("pprof endpoints available")

	// Parse --fail-on thresholds early so invalid values fail fast.
	thresholds, err := analysis.ParseThresholds(cfg.FailOn)
	if err != nil {
		return fmt.Errorf("invalid --fail-on value: %w", err)
	}

	profileTypes, err := profile.ParseProfileTypes(cfg.ProfileTypes)
	if err != nil {
		return fmt.Errorf("invalid --profile-types: %w", err)
	}

	// Step 3: Run load test and collect profiles concurrently
	var profiles []*profile.CollectedProfile

	switch {
	case !cfg.SkipLoad:
		profiles, err = runWithLoad(ctx, cfg, collector, endpoints, profileTypes)
	case cfg.SampleInterval > 0:
		profiles, err = runWatchMode(ctx, cfg, collector, profileTypes)
	default:
		profiles, err = runSkipLoad(ctx, cfg, collector, profileTypes)
	}
	if err != nil {
		return err
	}

	// Step 4: Evaluate thresholds if --fail-on was specified.
	if len(thresholds) > 0 {
		violations, err := analysis.CheckThresholds(profiles, thresholds)
		if err != nil {
			return fmt.Errorf("threshold analysis failed: %w", err)
		}

		if len(violations) > 0 {
			fmt.Fprintf(os.Stderr, "\nFAIL: performance thresholds exceeded\n")
			for _, v := range violations {
				fmt.Fprintf(os.Stderr, "  %-40s %5.1f%%  (threshold: %.0f%%)\n",
					v.Function, v.Percentage, v.Threshold.Percentage)
			}

			return fmt.Errorf("%d threshold violation(s) detected", len(violations))
		}

		fmt.Println("\nPASS: all thresholds within limits")
	}

	fmt.Println("\nProfiling complete!")
	printAnalysisHints(cfg.OutputDir, profileTypes)
	return nil
}

// runWithLoad generates HTTP load from the OpenAPI spec while collecting profiles
// in parallel. CPU profiles start immediately; snapshot profiles (heap, block,
// goroutine) are scheduled late in the load window to capture peak-load state.
func runWithLoad(ctx context.Context, cfg Config, collector *profile.Collector, endpoints []openapi.Endpoint, profileTypes []profile.Type) ([]*profile.CollectedProfile, error) {
	fmt.Printf("\nStarting load test with parallel profiling: %v, %d concurrent, %d RPS\n",
		cfg.Duration, cfg.Concurrency, cfg.RPS)

	runnerCfg := load.Config{
		Concurrency: cfg.Concurrency,
		RPS:         cfg.RPS,
		Duration:    cfg.Duration,
		Timeout:     10 * time.Second,
	}

	runner := load.NewRunner(runnerCfg)

	type profileResult struct {
		profileType profile.Type
		profile     *profile.CollectedProfile
		err         error
	}

	profileCtx, cancelProfiles := context.WithCancel(ctx)
	defer cancelProfiles()

	resultCh := make(chan profileResult, len(profileTypes))

	for _, pt := range profileTypes {
		go func(pt profile.Type) {
			if pt == profile.ProfileCPU {
				p, err := collector.CollectCPU(profileCtx)
				resultCh <- profileResult{pt, p, err}
				return
			}
			// Snapshot profiles are taken late in the load window
			// to capture peak-load state.
			delay := time.Duration(float64(cfg.Duration) * 0.8)
			select {
			case <-time.After(delay):
			case <-profileCtx.Done():
				resultCh <- profileResult{pt, nil, profileCtx.Err()}
				return
			}
			p, err := collector.CollectByType(profileCtx, pt)
			resultCh <- profileResult{pt, p, err}
		}(pt)
	}

	// Run load test (blocks for cfg.Duration).
	stats, loadErr := runner.Run(ctx, cfg.TargetURL, endpoints)
	if loadErr != nil {
		cancelProfiles()
		for range profileTypes {
			<-resultCh
		}
		return nil, fmt.Errorf("load test: %w", loadErr)
	}

	fmt.Printf("\nLoad test complete: %d requests sent (%d success, %d errors)\n",
		stats.TotalRequests, stats.SuccessCount, stats.ErrorCount)

	fmt.Println("\nLatency summary:")
	for endpoint, ls := range stats.EndpointLatency {
		fmt.Printf("  %s: avg=%v, min=%v, max=%v (n=%d)\n",
			endpoint, ls.Avg.Round(time.Millisecond),
			ls.Min.Round(time.Millisecond),
			ls.Max.Round(time.Millisecond), ls.Count)
	}

	fmt.Println("\nWaiting for profile collection to complete...")

	var profiles []*profile.CollectedProfile
	for range profileTypes {
		r := <-resultCh
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s profile collection failed: %v\n", r.profileType, r.err)
		} else {
			profiles = append(profiles, r.profile)
		}
	}

	fmt.Println()
	for _, p := range profiles {
		fmt.Printf("%s profile saved: %s (%d bytes, took %v)\n",
			p.Type, p.FilePath, p.Size, p.Duration.Round(time.Millisecond))
	}

	return profiles, nil
}

// runWatchMode samples pprof endpoints at regular intervals without generating load.
// All requested profile types are collected in parallel so their time-series
// cover the same observation window.
func runWatchMode(ctx context.Context, cfg Config, collector *profile.Collector, profileTypes []profile.Type) ([]*profile.CollectedProfile, error) {
	fmt.Printf("\nWatch mode: sampling %v every %v", cfg.ProfileTypes, cfg.SampleInterval)
	if cfg.SampleCount > 0 {
		fmt.Printf(" (max %d samples per type)", cfg.SampleCount)
	}
	if cfg.Duration > 0 {
		fmt.Printf(" for %v", cfg.Duration)
	}
	fmt.Println()

	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Duration)
		defer cancel()
	}

	type seriesResult struct {
		profiles []*profile.CollectedProfile
		err      error
	}

	results := make([]seriesResult, len(profileTypes))
	var wg sync.WaitGroup

	for i, pt := range profileTypes {
		wg.Add(1)
		go func(idx int, profileType profile.Type) {
			defer wg.Done()
			series, err := collector.CollectSeries(ctx, profileType, cfg.SampleInterval, cfg.SampleCount)
			results[idx] = seriesResult{series, err}
		}(i, pt)
	}
	wg.Wait()

	var allProfiles []*profile.CollectedProfile
	for i, r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("collecting %s series: %w", profileTypes[i], r.err)
		}
		fmt.Printf("  %s: %d samples collected\n", profileTypes[i], len(r.profiles))
		allProfiles = append(allProfiles, r.profiles...)
	}

	return allProfiles, nil
}

// runSkipLoad collects a single snapshot of each requested profile type
// without generating any HTTP load. Useful for profiling an already-running service.
func runSkipLoad(ctx context.Context, cfg Config, collector *profile.Collector, profileTypes []profile.Type) ([]*profile.CollectedProfile, error) {
	fmt.Println("\nSkipping load test (--skip-load)")
	fmt.Printf("\nCollecting profiles: %v\n", cfg.ProfileTypes)

	var profiles []*profile.CollectedProfile
	for _, pt := range profileTypes {
		p, err := collector.CollectByType(ctx, pt)
		if err != nil {
			return nil, fmt.Errorf("collecting %s profile: %w", pt, err)
		}
		profiles = append(profiles, p)
		fmt.Printf("%s profile saved: %s (%d bytes, took %v)\n",
			p.Type, p.FilePath, p.Size, p.Duration.Round(time.Millisecond))
	}

	return profiles, nil
}

func printAnalysisHints(outputDir string, profileTypes []profile.Type) {
	fmt.Printf("\nAnalyze profiles with:\n")
	for _, pt := range profileTypes {
		switch pt {
		case profile.ProfileCPU:
			fmt.Printf("  go tool pprof %s/cpu_*.pprof            # CPU interactive CLI\n", outputDir)
			fmt.Printf("  go tool pprof -http=:8081 %s/cpu_*.pprof    # CPU web UI\n", outputDir)
		case profile.ProfileHeap:
			fmt.Printf("  go tool pprof -http=:8081 %s/heap_*.pprof   # heap flamegraph\n", outputDir)
		case profile.ProfileBlock:
			fmt.Printf("  go tool pprof -http=:8081 %s/block_*.pprof  # I/O blocking\n", outputDir)
		case profile.ProfileGoroutine:
			fmt.Printf("  go tool pprof -http=:8081 %s/goroutine_*.pprof  # goroutine stacks\n", outputDir)
		}
	}
}
