package proficiency

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/openapi"
	"github.com/tuxerrante/proficiency/internal/profile"
	"golang.org/x/term"
)

// GateError reports failed profile gates after the report has been written.
type GateError struct {
	ThresholdViolations int
}

func (err *GateError) Error() string {
	return fmt.Sprintf("performance gates failed: %d threshold violation(s)", err.ThresholdViolations)
}

// Run executes one profiling workflow and returns its report. When configured
// gates fail, Run returns both the report and a *GateError.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.ToolVersion == "" {
		cfg.ToolVersion = "dev"
	}

	stdout := cfg.stdout()
	stderr := cfg.stderr()
	var endpoints []openapi.Endpoint

	if !cfg.SkipLoad {
		writef(stdout, "Parsing OpenAPI spec: %s\n", cfg.OpenAPIPath)
		parser := openapi.NewParser()
		var err error
		endpoints, err = parser.ParseFile(ctx, cfg.OpenAPIPath)
		if err != nil {
			return nil, fmt.Errorf("parsing OpenAPI spec: %w", err)
		}

		writef(stdout, "Parsed %d endpoints from %s\n", len(endpoints), cfg.OpenAPIPath)
		for _, endpoint := range endpoints {
			writef(stdout, "  %s %s\n", endpoint.Method, endpoint.Path)
		}
	}

	writef(stdout, "\nVerifying pprof availability at %s...\n", cfg.pprofURL())
	collector, err := profile.NewCollector(profile.CollectorConfig{
		TargetURL:   cfg.pprofURL(),
		OutputDir:   cfg.OutputDir,
		CPUDuration: cfg.CPUDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("creating profile collector: %w", err)
	}
	if err := collector.CheckPprofAvailable(ctx); err != nil {
		return nil, fmt.Errorf("pprof check failed: %w", err)
	}
	writeln(stdout, "pprof endpoints available")

	thresholds, err := analysis.ParseThresholds(cfg.FailOn)
	if err != nil {
		return nil, fmt.Errorf("invalid --fail-on value: %w", err)
	}
	profileTypes, err := profile.ParseProfileTypes(cfg.ProfileTypes)
	if err != nil {
		return nil, fmt.Errorf("invalid --profile-types: %w", err)
	}

	var profiles []*profile.CollectedProfile
	var loadStats *load.Stats
	switch {
	case !cfg.SkipLoad:
		profiles, loadStats, err = runWithLoad(ctx, cfg, collector, endpoints, profileTypes, stdout, stderr)
	case cfg.SampleInterval > 0:
		profiles, err = runWatchMode(ctx, cfg, collector, profileTypes, stdout, stderr)
	default:
		profiles, err = runSnapshot(ctx, cfg, collector, profileTypes, stdout)
	}
	if err != nil {
		return nil, err
	}

	profileAnalysis, err := analysis.AnalyzeProfiles(profiles, cfg.TopFunctions)
	if err != nil {
		return nil, fmt.Errorf("profile analysis failed: %w", err)
	}
	violations, err := analysis.CheckThresholds(profiles, thresholds)
	if err != nil {
		return nil, fmt.Errorf("threshold analysis failed: %w", err)
	}

	report := buildReport(
		cfg,
		profiles,
		loadStats,
		profileAnalysis,
		thresholds,
		violations,
		time.Now().UTC(),
	)

	printThresholds(stdout, stderr, thresholds, violations)
	if err := writeConfiguredReport(cfg, report, stdout); err != nil {
		return &report, err
	}

	if len(violations) > 0 {
		return &report, &GateError{
			ThresholdViolations: len(violations),
		}
	}

	writeln(stdout, "\nProfiling complete!")
	printAnalysisHints(stdout, cfg.OutputDir, profileTypes)
	return &report, nil
}

func writeConfiguredReport(cfg Config, report Report, output io.Writer) error {
	if cfg.ReportPath == "" {
		return nil
	}
	if err := WriteReport(cfg.ReportPath, report); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	writef(output, "Report written to %s\n", cfg.ReportPath)
	return nil
}

func runWithLoad(
	ctx context.Context,
	cfg Config,
	collector *profile.Collector,
	endpoints []openapi.Endpoint,
	profileTypes []profile.Type,
	stdout io.Writer,
	stderr io.Writer,
) ([]*profile.CollectedProfile, *load.Stats, error) {
	writef(stdout, "\nStarting load test with parallel profiling: %v, %d concurrent, %d RPS\n",
		cfg.Duration, cfg.Concurrency, cfg.RPS)

	runner := load.NewRunner(load.Config{
		Concurrency: cfg.Concurrency,
		RPS:         cfg.RPS,
		Duration:    cfg.Duration,
		Timeout:     cfg.RequestTimeout,
	})

	var reporter *load.ProgressReporter
	if !cfg.NoProgress && isTerminalWriter(stderr) {
		reporter = load.NewProgressReporter(&runner.Counters, cfg.Duration, stderr)
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

	for _, profileType := range profileTypes {
		go func(profileType profile.Type) {
			if profileType == profile.ProfileCPU {
				collected, collectErr := collector.CollectByType(profileCtx, profileType)
				resultCh <- profileResult{profileType, collected, collectErr}
				return
			}

			delayFactor := 0.8
			if profileType == profile.ProfileBlock {
				delayFactor = 0.9
			}
			timer := time.NewTimer(time.Duration(float64(cfg.Duration) * delayFactor))
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-profileCtx.Done():
				resultCh <- profileResult{profileType, nil, profileCtx.Err()}
				return
			}
			collected, collectErr := collector.CollectByType(profileCtx, profileType)
			resultCh <- profileResult{profileType, collected, collectErr}
		}(profileType)
	}

	stats, loadErr := runner.Run(ctx, cfg.TargetURL, endpoints)
	if reporter != nil {
		reporter.Stop()
		writeln(stderr)
	}
	if loadErr != nil {
		cancelProfiles()
		for range profileTypes {
			<-resultCh
		}
		return nil, nil, fmt.Errorf("load test: %w", loadErr)
	}

	writef(stdout, "\nLoad test complete: %d requests sent (%d success, %d errors)\n",
		stats.TotalRequests, stats.SuccessCount, stats.ErrorCount)
	writeln(stdout, "\nLatency summary:")
	for endpoint, latency := range stats.EndpointLatency {
		writef(stdout, "  %s: avg=%v, min=%v, max=%v (n=%d)\n",
			endpoint,
			latency.Avg.Round(time.Millisecond),
			latency.Min.Round(time.Millisecond),
			latency.Max.Round(time.Millisecond),
			latency.Count,
		)
	}
	writeln(stdout, "\nWaiting for profile collection to complete...")

	profiles := make([]*profile.CollectedProfile, 0, len(profileTypes))
	for range profileTypes {
		result := <-resultCh
		if result.err != nil {
			writef(stderr, "Warning: %s profile collection failed: %v\n",
				result.profileType.DisplayName(), result.err)
			continue
		}
		profiles = append(profiles, result.profile)
	}
	if len(profiles) == 0 {
		return nil, stats, fmt.Errorf("all %d profile collection(s) failed", len(profileTypes))
	}

	writeln(stdout)
	for _, collected := range profiles {
		writef(stdout, "%s profile saved: %s (%d bytes, took %v)\n",
			collected.Type,
			collected.FilePath,
			collected.Size,
			collected.Duration.Round(time.Millisecond),
		)
	}
	return profiles, stats, nil
}

func runWatchMode(
	ctx context.Context,
	cfg Config,
	collector *profile.Collector,
	profileTypes []profile.Type,
	stdout io.Writer,
	stderr io.Writer,
) ([]*profile.CollectedProfile, error) {
	writef(stdout, "\nWatch mode: sampling %v every %v", cfg.ProfileTypes, cfg.SampleInterval)
	if cfg.SampleCount > 0 {
		writef(stdout, " (max %d samples per type)", cfg.SampleCount)
	}
	writef(stdout, " for %v\n", cfg.Duration)

	collectCtx, cancelCollect := context.WithTimeout(ctx, cfg.Duration)
	defer cancelCollect()

	type seriesResult struct {
		profileType profile.Type
		profiles    []*profile.CollectedProfile
		err         error
	}
	resultCh := make(chan seriesResult, len(profileTypes))
	for _, profileType := range profileTypes {
		go func(profileType profile.Type) {
			series, collectErr := collector.CollectSeries(
				collectCtx,
				profileType,
				cfg.SampleInterval,
				cfg.SampleCount,
			)
			resultCh <- seriesResult{profileType, series, collectErr}
		}(profileType)
	}

	var profiles []*profile.CollectedProfile
	for range profileTypes {
		result := <-resultCh
		if result.err != nil {
			writef(stderr, "Warning: %s series collection failed: %v\n",
				result.profileType.DisplayName(), result.err)
		}
		if len(result.profiles) > 0 {
			writef(stdout, "  %s: %d samples collected\n",
				result.profileType.DisplayName(), len(result.profiles))
			profiles = append(profiles, result.profiles...)
		}
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("all %d profile series collection(s) failed", len(profileTypes))
	}
	return profiles, nil
}

func runSnapshot(
	ctx context.Context,
	cfg Config,
	collector *profile.Collector,
	profileTypes []profile.Type,
	stdout io.Writer,
) ([]*profile.CollectedProfile, error) {
	writeln(stdout, "\nSkipping load test (--skip-load)")
	writef(stdout, "\nCollecting profiles: %v\n", cfg.ProfileTypes)

	profiles := make([]*profile.CollectedProfile, 0, len(profileTypes))
	for _, profileType := range profileTypes {
		collected, err := collector.CollectByType(ctx, profileType)
		if err != nil {
			return nil, fmt.Errorf("collecting %s profile: %w", profileType.DisplayName(), err)
		}
		profiles = append(profiles, collected)
		writef(stdout, "%s profile saved: %s (%d bytes, took %v)\n",
			collected.Type,
			collected.FilePath,
			collected.Size,
			collected.Duration.Round(time.Millisecond),
		)
	}
	return profiles, nil
}

func printThresholds(
	stdout io.Writer,
	stderr io.Writer,
	thresholds []analysis.Threshold,
	violations []analysis.Violation,
) {
	if len(thresholds) == 0 {
		return
	}
	if len(violations) == 0 {
		writeln(stdout, "\nPASS: all profile thresholds within limits")
		return
	}

	writeln(stderr, "\nFAIL: performance thresholds exceeded")
	for _, violation := range violations {
		writef(stderr, "  %-40s %5.1f%%  (threshold: %.0f%%)\n",
			violation.Function,
			violation.Percentage,
			violation.Threshold.Percentage,
		)
	}
}

func printAnalysisHints(output io.Writer, outputDir string, profileTypes []profile.Type) {
	writeln(output, "\nAnalyze profiles with:")
	for _, profileType := range profileTypes {
		switch profileType {
		case profile.ProfileCPU:
			writef(output, "  go tool pprof %s/cpu_*.pprof                # CPU interactive CLI\n", outputDir)
			writef(output, "  go tool pprof -http=:8081 %s/cpu_*.pprof    # CPU web UI\n", outputDir)
		case profile.ProfileHeap:
			writef(output, "  go tool pprof -http=:8081 %s/heap_*.pprof   # heap flamegraph\n", outputDir)
		case profile.ProfileBlock:
			writef(output, "  go tool pprof -http=:8081 %s/block_*.pprof  # I/O blocking\n", outputDir)
		case profile.ProfileGoroutine:
			writef(output, "  go tool pprof -http=:8081 %s/goroutine_*.pprof  # goroutine stacks\n", outputDir)
		}
	}
}

func writef(output io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(output, format, args...)
}

func writeln(output io.Writer, args ...any) {
	_, _ = fmt.Fprintln(output, args...)
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
