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
	"syscall"
	"time"

	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/openapi"
	"github.com/tuxerrante/proficiency/internal/profile"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Config holds all CLI configuration parsed from flags.
type Config struct {
	OpenAPIPath string
	TargetURL   string
	PprofURL    string // Separate pprof target; defaults to TargetURL.
	Duration    time.Duration
	Concurrency int
	RPS         int
	OutputDir   string
	CPUDuration time.Duration
	SkipLoad    bool
	Version     bool
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

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived %v, initiating graceful shutdown...\n", sig)
		cancel()
	}()

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
func validateConfig(cfg Config) error {
	if cfg.OpenAPIPath == "" {
		return errors.New("--openapi is required")
	}

	if cfg.TargetURL == "" {
		return errors.New("--target is required")
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

	// Verify OpenAPI spec file exists
	if _, err := os.Stat(cfg.OpenAPIPath); os.IsNotExist(err) {
		return fmt.Errorf("OpenAPI spec file not found: %s", cfg.OpenAPIPath)
	}

	return nil
}

// run executes the main profiling workflow.
//
// WORKFLOW:
// 1. Parse OpenAPI spec to extract endpoints
// 2. Verify pprof is available on target
// 3. Run load test AND collect profiles concurrently
// 4. Print summary with analysis hints.
func run(ctx context.Context, cfg Config) error {
	// Step 1: Parse OpenAPI spec
	fmt.Printf("Parsing OpenAPI spec: %s\n", cfg.OpenAPIPath)

	parser := openapi.NewParser()
	endpoints, err := parser.ParseFile(ctx, cfg.OpenAPIPath)
	if err != nil {
		return fmt.Errorf("parsing OpenAPI spec: %w", err)
	}

	fmt.Printf("Parsed %d endpoints from %s\n", len(endpoints), cfg.OpenAPIPath)
	for _, ep := range endpoints {
		fmt.Printf("  %s %s\n", ep.Method, ep.Path)
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

	// Step 3: Run load test and collect profiles concurrently
	if !cfg.SkipLoad {
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
			profile *profile.CollectedProfile
			err     error
		}

		cpuCh := make(chan profileResult, 1)
		heapCh := make(chan profileResult, 1)
		blockCh := make(chan profileResult, 1)

		// Start CPU profile collection concurrently with load.
		// The pprof endpoint blocks for CPUDuration, capturing samples
		// while the load test is generating traffic.
		go func() {
			p, err := collector.CollectCPU(ctx)
			cpuCh <- profileResult{p, err}
		}()

		// Schedule heap snapshot at ~80% through the load duration
		// to capture peak memory usage while load is still active.
		go func() {
			delay := time.Duration(float64(cfg.Duration) * 0.8)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				heapCh <- profileResult{nil, ctx.Err()}
				return
			}
			p, err := collector.CollectHeap(ctx)
			heapCh <- profileResult{p, err}
		}()

		// Schedule block profile at ~90% through the load duration
		// to capture accumulated I/O blocking and lock contention.
		go func() {
			delay := time.Duration(float64(cfg.Duration) * 0.9)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				blockCh <- profileResult{nil, ctx.Err()}
				return
			}
			p, err := collector.CollectBlock(ctx)
			blockCh <- profileResult{p, err}
		}()

		// Run load test (blocks for cfg.Duration).
		stats, loadErr := runner.Run(ctx, cfg.TargetURL, endpoints)
		if loadErr != nil {
			return fmt.Errorf("load test: %w", loadErr)
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

		// Wait for profile collection to finish.
		fmt.Println("\nWaiting for profile collection to complete...")
		cpuResult := <-cpuCh
		heapResult := <-heapCh
		blockResult := <-blockCh

		var profiles []*profile.CollectedProfile
		if cpuResult.err != nil {
			fmt.Fprintf(os.Stderr, "Warning: CPU profile collection failed: %v\n", cpuResult.err)
		} else {
			profiles = append(profiles, cpuResult.profile)
		}
		if heapResult.err != nil {
			fmt.Fprintf(os.Stderr, "Warning: heap profile collection failed: %v\n", heapResult.err)
		} else {
			profiles = append(profiles, heapResult.profile)
		}
		if blockResult.err != nil {
			fmt.Fprintf(os.Stderr, "Warning: block profile collection failed: %v\n", blockResult.err)
		} else {
			profiles = append(profiles, blockResult.profile)
		}

		fmt.Println()
		for _, p := range profiles {
			fmt.Printf("%s profile saved: %s (%d bytes, took %v)\n",
				p.Type, p.FilePath, p.Size, p.Duration.Round(time.Millisecond))
		}
	} else {
		fmt.Println("\nSkipping load test (--skip-load)")

		// Collect profiles without load.
		fmt.Printf("\nCollecting profiles (CPU: %v)...\n", cfg.CPUDuration)
		profiles, err := collector.CollectAll(ctx)
		if err != nil {
			return fmt.Errorf("collecting profiles: %w", err)
		}

		for _, p := range profiles {
			fmt.Printf("%s profile saved: %s (%d bytes, took %v)\n",
				p.Type, p.FilePath, p.Size, p.Duration.Round(time.Millisecond))
		}
	}

	fmt.Println("\nProfiling complete!")
	fmt.Printf("\nAnalyze profiles with:\n")
	fmt.Printf("  go tool pprof %s/cpu_*.pprof            # CPU interactive CLI\n", cfg.OutputDir)
	fmt.Printf("  go tool pprof -http=:8081 %s/cpu_*.pprof    # CPU web UI\n", cfg.OutputDir)
	fmt.Printf("  go tool pprof -http=:8081 %s/heap_*.pprof   # heap flamegraph\n", cfg.OutputDir)
	fmt.Printf("  go tool pprof -http=:8081 %s/block_*.pprof  # I/O blocking\n", cfg.OutputDir)
	return nil
}
