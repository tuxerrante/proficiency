// Package proficiency profiles Go HTTP APIs from an OpenAPI specification.
package proficiency

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/profile"
)

const (
	defaultOutputDir      = "./profiles"
	defaultProfileTypes   = "cpu,heap,block"
	defaultTopFunctions   = 20
	defaultRequestTimeout = 10 * time.Second
)

// Config controls one profiling run.
type Config struct {
	OpenAPIPath      string
	TargetURL        string
	PprofURL         string
	Duration         time.Duration
	Concurrency      int
	RPS              int
	RequestTimeout   time.Duration
	OutputDir        string
	CPUDuration      time.Duration
	SkipLoad         bool
	FailOn           string
	SampleInterval   time.Duration
	SampleCount      int
	ProfileTypes     string
	NoProgress       bool
	ReportPath       string
	BaselinePath     string
	FailOnRegression string
	TopFunctions     int
	ToolVersion      string
	Metadata         Metadata
	Output           io.Writer
	ErrorOutput      io.Writer
}

// DefaultConfig returns the defaults used by the CLI.
func DefaultConfig() Config {
	return Config{
		Duration:       30 * time.Second,
		Concurrency:    10,
		RPS:            100,
		RequestTimeout: defaultRequestTimeout,
		OutputDir:      defaultOutputDir,
		CPUDuration:    30 * time.Second,
		ProfileTypes:   defaultProfileTypes,
		TopFunctions:   defaultTopFunctions,
		ToolVersion:    "dev",
	}
}

// Validate checks whether the configuration is internally consistent.
func (cfg Config) Validate() error {
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
	if cfg.RequestTimeout <= 0 {
		return errors.New("request timeout must be positive")
	}
	if cfg.CPUDuration <= 0 {
		return errors.New("--cpu-duration must be positive")
	}
	if cfg.TopFunctions < 0 {
		return errors.New("--top-functions cannot be negative")
	}
	if cfg.SampleInterval > 0 && cfg.SampleInterval < 500*time.Millisecond {
		return errors.New("--sample-interval must be at least 500ms")
	}
	if cfg.SampleCount > 0 && cfg.SampleInterval == 0 {
		return errors.New("--sample-count requires --sample-interval")
	}
	if cfg.FailOnRegression != "" && cfg.BaselinePath == "" {
		return errors.New("--fail-on-regression requires --baseline")
	}
	if cfg.BaselinePath != "" && cfg.ReportPath != "" && cfg.BaselinePath == cfg.ReportPath {
		return errors.New("--baseline and --report must use different paths")
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

	if _, err := ParseRegressionRules(cfg.FailOnRegression); err != nil {
		return fmt.Errorf("invalid --fail-on-regression value: %w", err)
	}
	if _, err := analysis.ParseThresholds(cfg.FailOn); err != nil {
		return fmt.Errorf("invalid --fail-on value: %w", err)
	}

	return nil
}

func (cfg Config) stdout() io.Writer {
	if cfg.Output == nil {
		return io.Discard
	}
	return cfg.Output
}

func (cfg Config) stderr() io.Writer {
	if cfg.ErrorOutput == nil {
		return io.Discard
	}
	return cfg.ErrorOutput
}

func (cfg Config) pprofURL() string {
	if cfg.PprofURL != "" {
		return cfg.PprofURL
	}
	return cfg.TargetURL
}
