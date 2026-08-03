package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tuxerrante/proficiency"
)

type Config struct {
	proficiency.Config

	Version bool
}

func defaultConfig() Config {
	return Config{
		Config: proficiency.DefaultConfig(),
	}
}

func parseFlags() Config {
	cfg := defaultConfig()
	registerFlags(flag.CommandLine, &cfg)

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: proficiency [options]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Proficiency profiles your Go API by generating load and collecting pprof data.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  proficiency --openapi api.yaml --target http://localhost:6060 --duration 30s")
	}

	flag.Parse()
	return cfg
}

func registerFlags(fs *flag.FlagSet, cfg *Config) {
	fs.StringVar(&cfg.OpenAPIPath, "openapi", cfg.OpenAPIPath, "Path to OpenAPI spec file (required)")
	fs.StringVar(&cfg.TargetURL, "target", cfg.TargetURL, "Target service URL, e.g. http://localhost:8080 (required)")
	fs.StringVar(&cfg.PprofURL, "pprof-target", cfg.PprofURL, "Pprof target URL if different from --target")
	fs.DurationVar(&cfg.Duration, "duration", cfg.Duration, "Load test or watch duration")
	fs.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "Number of concurrent workers")
	fs.IntVar(&cfg.RPS, "rps", cfg.RPS, "Target requests per second")
	fs.DurationVar(&cfg.RequestTimeout, "request-timeout", cfg.RequestTimeout, "Maximum duration for one generated request")
	fs.StringVar(&cfg.OutputDir, "output", cfg.OutputDir, "Directory for profile output")
	fs.DurationVar(&cfg.CPUDuration, "cpu-duration", cfg.CPUDuration, "CPU profile collection duration")
	fs.BoolVar(&cfg.SkipLoad, "skip-load", cfg.SkipLoad, "Skip load generation and only collect profiles")
	fs.BoolVar(&cfg.Version, "version", false, "Print version and exit")
	fs.StringVar(&cfg.FailOn, "fail-on", cfg.FailOn,
		"Profile thresholds, e.g. cpu:30,alloc:50")
	fs.DurationVar(&cfg.SampleInterval, "sample-interval", cfg.SampleInterval,
		"Interval between profile samples; enables watch mode with --skip-load")
	fs.IntVar(&cfg.SampleCount, "sample-count", cfg.SampleCount,
		"Maximum samples per type (0 = duration-limited)")
	fs.StringVar(&cfg.ProfileTypes, "profile-types", cfg.ProfileTypes,
		"Comma-separated profile types: cpu, heap, block, goroutine")
	fs.BoolVar(&cfg.NoProgress, "no-progress", cfg.NoProgress,
		"Disable the live progress status line")
	fs.StringVar(&cfg.ReportPath, "report", cfg.ReportPath, "Write the versioned JSON report to this path")
	fs.StringVar(&cfg.BaselinePath, "baseline", cfg.BaselinePath,
		"Compare the new report with this baseline report")
	fs.StringVar(&cfg.FailOnRegression, "fail-on-regression", cfg.FailOnRegression,
		"Regression limits, e.g. latency:10:200us,throughput:10:5rps,error-rate:1,cpu:5")
	fs.IntVar(&cfg.TopFunctions, "top-functions", cfg.TopFunctions,
		"Number of top functions to record per profile (0 disables analysis)")
	fs.StringVar(&cfg.Metadata.Label, "label", cfg.Metadata.Label, "Human-readable report label")
	fs.StringVar(&cfg.Metadata.Repository, "repository", os.Getenv("GITHUB_REPOSITORY"), "Source repository identifier")
	fs.StringVar(&cfg.Metadata.Revision, "revision", os.Getenv("GITHUB_SHA"), "Source revision identifier")
	fs.StringVar(&cfg.Metadata.Ref, "ref", os.Getenv("GITHUB_REF"), "Source ref identifier")
}

func parseFlagsFromArgs(args []string) (Config, error) {
	cfg := defaultConfig()
	fs := flag.NewFlagSet("proficiency", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateConfig(cfg Config) error {
	return cfg.Validate()
}
