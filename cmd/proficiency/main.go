// Package main provides the CLI entry point for the proficiency tool.
// It orchestrates OpenAPI parsing, load generation, and profile collection.
//
// The CLI follows the Unix philosophy: do one thing well, be composable.
// Output is designed for both human readability and machine parsing.
package main

import (
	"context"
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
	SwaggerPath string
	TargetURL   string
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
// - SIGINT/SIGTERM: Graceful shutdown, saves partial profiles if possible
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

	flag.StringVar(&cfg.SwaggerPath, "swagger", "", "Path to OpenAPI/Swagger spec file (required)")
	flag.StringVar(&cfg.TargetURL, "target", "", "Target service URL, e.g., http://localhost:6060 (required)")
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
		fmt.Fprintf(os.Stderr, "  proficiency --swagger api.yaml --target http://localhost:6060 --duration 30s\n\n")
	}

	flag.Parse()
	return cfg
}

// validateConfig ensures required fields are set and values are sensible.
func validateConfig(cfg Config) error {
	if cfg.SwaggerPath == "" {
		return fmt.Errorf("--swagger is required")
	}

	if cfg.TargetURL == "" {
		return fmt.Errorf("--target is required")
	}

	if cfg.Duration <= 0 {
		return fmt.Errorf("--duration must be positive")
	}

	if cfg.Concurrency <= 0 {
		return fmt.Errorf("--concurrency must be positive")
	}

	if cfg.RPS <= 0 {
		return fmt.Errorf("--rps must be positive")
	}

	// Verify swagger file exists
	if _, err := os.Stat(cfg.SwaggerPath); os.IsNotExist(err) {
		return fmt.Errorf("swagger file not found: %s", cfg.SwaggerPath)
	}

	return nil
}

// run executes the main profiling workflow.
//
// WORKFLOW:
// 1. Parse OpenAPI spec to extract endpoints
// 2. Verify pprof is available on target
// 3. Run load test against endpoints
// 4. Collect CPU and heap profiles
// 5. Print summary
func run(ctx context.Context, cfg Config) error {
	// Step 1: Parse OpenAPI spec
	fmt.Printf("Parsing OpenAPI spec: %s\n", cfg.SwaggerPath)

	parser := openapi.NewParser()
	endpoints, err := parser.ParseFile(ctx, cfg.SwaggerPath)
	if err != nil {
		return fmt.Errorf("parsing swagger spec: %w", err)
	}

	fmt.Printf("Parsed %d endpoints from %s\n", len(endpoints), cfg.SwaggerPath)
	for _, ep := range endpoints {
		fmt.Printf("  %s %s\n", ep.Method, ep.Path)
	}

	// Step 2: Verify pprof availability
	fmt.Printf("\nVerifying pprof availability at %s...\n", cfg.TargetURL)

	collectorCfg := profile.CollectorConfig{
		TargetURL:   cfg.TargetURL,
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

	// Step 3: Run load test (unless skipped)
	if !cfg.SkipLoad {
		fmt.Printf("\nStarting load test: %v, %d concurrent, %d RPS\n",
			cfg.Duration, cfg.Concurrency, cfg.RPS)

		runnerCfg := load.Config{
			Concurrency: cfg.Concurrency,
			RPS:         cfg.RPS,
			Duration:    cfg.Duration,
			Timeout:     10 * time.Second,
		}

		runner := load.NewRunner(runnerCfg)
		stats, err := runner.Run(ctx, cfg.TargetURL, endpoints)
		if err != nil {
			return fmt.Errorf("load test: %w", err)
		}

		fmt.Printf("Load test complete: %d requests sent (%d success, %d errors)\n",
			stats.TotalRequests, stats.SuccessCount, stats.ErrorCount)

		// Print per-endpoint latency summary
		fmt.Println("\nLatency summary:")
		for endpoint, ls := range stats.EndpointLatency {
			fmt.Printf("  %s: avg=%v, min=%v, max=%v (n=%d)\n",
				endpoint, ls.Avg.Round(time.Millisecond),
				ls.Min.Round(time.Millisecond),
				ls.Max.Round(time.Millisecond), ls.Count)
		}
	} else {
		fmt.Println("\nSkipping load test (--skip-load)")
	}

	// Step 4: Collect profiles
	fmt.Printf("\nCollecting profiles (CPU: %v)...\n", cfg.CPUDuration)

	profiles, err := collector.CollectAll(ctx)
	if err != nil {
		return fmt.Errorf("collecting profiles: %w", err)
	}

	for _, p := range profiles {
		fmt.Printf("%s profile saved: %s (%d bytes, took %v)\n",
			p.Type, p.FilePath, p.Size, p.Duration.Round(time.Millisecond))
	}

	fmt.Println("\nProfiling complete!")
	return nil
}
