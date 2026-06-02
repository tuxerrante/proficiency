package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/openapi"
	"github.com/tuxerrante/proficiency/internal/profile"
	"golang.org/x/term"
)

// run executes the main profiling workflow:
// 1. Parse OpenAPI spec (skipped in watch/snapshot mode)
// 2. Verify pprof availability
// 3. Collect profiles (load, watch, or snapshot mode)
// 4. Evaluate thresholds and print results.
func run(ctx context.Context, cfg Config) error {
	var endpoints []openapi.Endpoint

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

	pprofURL := cfg.TargetURL
	if cfg.PprofURL != "" {
		pprofURL = cfg.PprofURL
	}

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

	thresholds, err := analysis.ParseThresholds(cfg.FailOn)
	if err != nil {
		return fmt.Errorf("invalid --fail-on value: %w", err)
	}

	profileTypes, err := profile.ParseProfileTypes(cfg.ProfileTypes)
	if err != nil {
		return fmt.Errorf("invalid --profile-types: %w", err)
	}

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

	showProgress := !cfg.NoProgress && term.IsTerminal(int(os.Stderr.Fd()))
	var reporter *load.ProgressReporter
	if showProgress {
		reporter = load.NewProgressReporter(&runner.Counters, cfg.Duration, os.Stderr)
		reporter.Start()
		defer reporter.Stop()
	}

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
				p, err := collector.CollectByType(profileCtx, pt)
				resultCh <- profileResult{pt, p, err}
				return
			}
			// Snapshot profiles are staggered late in the load window
			// to capture peak-load state. Block profiles use 0.9 to
			// catch contention that peaks later than memory allocations.
			delayFactor := 0.8
			if pt == profile.ProfileBlock {
				delayFactor = 0.9
			}
			delay := time.Duration(float64(cfg.Duration) * delayFactor)
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-profileCtx.Done():
				resultCh <- profileResult{pt, nil, profileCtx.Err()}
				return
			}
			p, err := collector.CollectByType(profileCtx, pt)
			resultCh <- profileResult{pt, p, err}
		}(pt)
	}

	stats, loadErr := runner.Run(ctx, cfg.TargetURL, endpoints)
	if reporter != nil {
		reporter.Stop()
		fmt.Fprintln(os.Stderr)
	}

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
			fmt.Fprintf(os.Stderr, "Warning: %s profile collection failed: %v\n", r.profileType.DisplayName(), r.err)
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

	collectCtx, cancelCollect := context.WithCancel(ctx)
	defer cancelCollect()

	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		collectCtx, cancel = context.WithTimeout(collectCtx, cfg.Duration)
		defer cancel()
	}

	type seriesResult struct {
		profileType profile.Type
		profiles    []*profile.CollectedProfile
		err         error
	}

	resultCh := make(chan seriesResult, len(profileTypes))

	for _, pt := range profileTypes {
		go func(profileType profile.Type) {
			series, err := collector.CollectSeries(collectCtx, profileType, cfg.SampleInterval, cfg.SampleCount)
			resultCh <- seriesResult{profileType, series, err}
		}(pt)
	}

	// Drain all results. Treat per-type errors as warnings (matching runWithLoad)
	// to preserve partial data from successful types.
	var allProfiles []*profile.CollectedProfile
	for range profileTypes {
		r := <-resultCh
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s series collection failed: %v\n", r.profileType.DisplayName(), r.err)
		}
		if len(r.profiles) > 0 {
			fmt.Printf("  %s: %d samples collected\n", r.profileType.DisplayName(), len(r.profiles))
			allProfiles = append(allProfiles, r.profiles...)
		}
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
			return nil, fmt.Errorf("collecting %s profile: %w", pt.DisplayName(), err)
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
