package proficiency

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/profile"
)

// ReportSchemaVersion identifies the JSON compatibility contract.
const ReportSchemaVersion = "v1"

const maxReportSize = 16 << 20

const (
	modeLoad     = "load"
	modeWatch    = "watch"
	modeSnapshot = "snapshot"
)

// Metadata identifies the source revision represented by a report.
type Metadata struct {
	Label      string `json:"label,omitempty"`
	Repository string `json:"repository,omitempty"`
	Revision   string `json:"revision,omitempty"`
	Ref        string `json:"ref,omitempty"`
}

// Report is the versioned, machine-readable result of a profiling run.
type Report struct {
	SchemaVersion string            `json:"schemaVersion"`
	Timestamp     time.Time         `json:"timestamp"`
	ToolVersion   string            `json:"toolVersion"`
	Metadata      Metadata          `json:"metadata,omitzero"`
	RunConfig     ReportRunConfig   `json:"runConfig"`
	Profiles      []ReportProfile   `json:"profiles"`
	LoadStats     *ReportLoad       `json:"loadStats,omitempty"`
	Analysis      []ProfileAnalysis `json:"analysis"`
	Thresholds    ThresholdResult   `json:"thresholds"`
	Comparison    *Comparison       `json:"comparison,omitempty"`
}

// ReportRunConfig records the inputs that materially affect a run.
type ReportRunConfig struct {
	Mode             string `json:"mode"`
	OpenAPIPath      string `json:"openapiPath,omitempty"`
	TargetURL        string `json:"targetUrl"`
	PprofURL         string `json:"pprofUrl"`
	OutputDir        string `json:"outputDir"`
	DurationMS       int64  `json:"durationMs"`
	CPUDurationMS    int64  `json:"cpuDurationMs"`
	Concurrency      int    `json:"concurrency"`
	RPS              int    `json:"rps"`
	RequestTimeoutMS int64  `json:"requestTimeoutMs"`
	SkipLoad         bool   `json:"skipLoad"`
	ProfileTypes     string `json:"profileTypes"`
	SampleIntervalMS int64  `json:"sampleIntervalMs"`
	SampleCount      int    `json:"sampleCount"`
	FailOn           string `json:"failOn,omitempty"`
	TopFunctions     int    `json:"topFunctions"`
}

// ReportProfile identifies one saved pprof artifact.
type ReportProfile struct {
	Type       string `json:"type"`
	Metric     string `json:"metric"`
	FilePath   string `json:"filePath"`
	SizeBytes  int64  `json:"sizeBytes"`
	DurationMS int64  `json:"durationMs"`
}

// ReportLoad contains aggregate load-generation measurements.
type ReportLoad struct {
	TotalRequests     int64                 `json:"totalRequests"`
	SuccessCount      int64                 `json:"successCount"`
	ErrorCount        int64                 `json:"errorCount"`
	ErrorRatePercent  float64               `json:"errorRatePercent"`
	DurationMS        int64                 `json:"durationMs"`
	RequestsPerSecond float64               `json:"requestsPerSecond"`
	Endpoints         []ReportEndpointStats `json:"endpoints"`
}

// ReportEndpointStats contains deterministic per-endpoint latency aggregates.
type ReportEndpointStats struct {
	Endpoint    string `json:"endpoint"`
	Count       int64  `json:"count"`
	MinMicros   int64  `json:"minMicros"`
	MaxMicros   int64  `json:"maxMicros"`
	AvgMicros   int64  `json:"avgMicros"`
	TotalMicros int64  `json:"totalMicros"`
}

// ProfileAnalysis contains the highest flat-cost functions in one profile.
type ProfileAnalysis struct {
	ProfileType string         `json:"profileType"`
	Functions   []FunctionStat `json:"functions"`
}

// FunctionStat contains a function's flat share of its profile.
type FunctionStat struct {
	Function   string  `json:"function"`
	Percentage float64 `json:"percentage"`
}

// ThresholdResult records configured profile gates and their outcome.
type ThresholdResult struct {
	Configured bool                 `json:"configured"`
	Passed     bool                 `json:"passed"`
	Rules      []ThresholdRule      `json:"rules,omitempty"`
	Violations []ThresholdViolation `json:"violations,omitempty"`
}

// ThresholdRule is one configured per-function profile threshold.
type ThresholdRule struct {
	ProfileType string  `json:"profileType"`
	Percentage  float64 `json:"percentage"`
}

// ThresholdViolation identifies a function that exceeded a profile threshold.
type ThresholdViolation struct {
	Function    string  `json:"function"`
	ProfileType string  `json:"profileType"`
	Percentage  float64 `json:"percentage"`
	Threshold   float64 `json:"threshold"`
}

func buildReport(
	cfg Config,
	profiles []*profile.CollectedProfile,
	loadStats *load.Stats,
	profileAnalysis []analysis.ProfileAnalysis,
	thresholds []analysis.Threshold,
	violations []analysis.Violation,
	now time.Time,
) Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Timestamp:     now,
		ToolVersion:   cfg.ToolVersion,
		Metadata:      cfg.Metadata,
		RunConfig: ReportRunConfig{
			Mode:             modeFromConfig(cfg),
			OpenAPIPath:      cfg.OpenAPIPath,
			TargetURL:        cfg.TargetURL,
			PprofURL:         cfg.pprofURL(),
			OutputDir:        cfg.OutputDir,
			DurationMS:       cfg.Duration.Milliseconds(),
			CPUDurationMS:    cfg.CPUDuration.Milliseconds(),
			Concurrency:      cfg.Concurrency,
			RPS:              cfg.RPS,
			RequestTimeoutMS: cfg.RequestTimeout.Milliseconds(),
			SkipLoad:         cfg.SkipLoad,
			ProfileTypes:     cfg.ProfileTypes,
			SampleIntervalMS: cfg.SampleInterval.Milliseconds(),
			SampleCount:      cfg.SampleCount,
			FailOn:           cfg.FailOn,
			TopFunctions:     cfg.TopFunctions,
		},
		Profiles:   reportProfiles(profiles),
		LoadStats:  reportLoadStats(loadStats),
		Analysis:   reportAnalysis(profileAnalysis),
		Thresholds: reportThresholds(thresholds, violations),
	}
}

func modeFromConfig(cfg Config) string {
	switch {
	case !cfg.SkipLoad:
		return modeLoad
	case cfg.SampleInterval > 0:
		return modeWatch
	default:
		return modeSnapshot
	}
}

func reportProfiles(profiles []*profile.CollectedProfile) []ReportProfile {
	items := make([]ReportProfile, 0, len(profiles))
	for _, collected := range profiles {
		items = append(items, ReportProfile{
			Type:       collected.Type.DisplayName(),
			Metric:     profileMetric(collected.Type),
			FilePath:   collected.FilePath,
			SizeBytes:  collected.Size,
			DurationMS: collected.Duration.Milliseconds(),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Type == items[j].Type {
			return items[i].FilePath < items[j].FilePath
		}
		return items[i].Type < items[j].Type
	})
	return items
}

func profileMetric(profileType profile.Type) string {
	switch profileType {
	case profile.ProfileCPU:
		return string(analysis.CPU)
	case profile.ProfileHeap:
		return string(analysis.Alloc)
	case profile.ProfileBlock:
		return string(analysis.Block)
	case profile.ProfileGoroutine:
		return string(analysis.Goroutine)
	default:
		return ""
	}
}

func reportLoadStats(stats *load.Stats) *ReportLoad {
	if stats == nil {
		return nil
	}

	keys := make([]string, 0, len(stats.EndpointLatency))
	for key := range stats.EndpointLatency {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	endpoints := make([]ReportEndpointStats, 0, len(keys))
	for _, key := range keys {
		latency := stats.EndpointLatency[key]
		endpoints = append(endpoints, ReportEndpointStats{
			Endpoint:    key,
			Count:       latency.Count,
			MinMicros:   latency.Min.Microseconds(),
			MaxMicros:   latency.Max.Microseconds(),
			AvgMicros:   latency.Avg.Microseconds(),
			TotalMicros: latency.Total.Microseconds(),
		})
	}

	result := &ReportLoad{
		TotalRequests: stats.TotalRequests,
		SuccessCount:  stats.SuccessCount,
		ErrorCount:    stats.ErrorCount,
		DurationMS:    stats.Duration.Milliseconds(),
		Endpoints:     endpoints,
	}
	if stats.TotalRequests > 0 {
		result.ErrorRatePercent = float64(stats.ErrorCount) / float64(stats.TotalRequests) * 100
	}
	if stats.Duration > 0 {
		result.RequestsPerSecond = float64(stats.TotalRequests) / stats.Duration.Seconds()
	}
	return result
}

func reportAnalysis(items []analysis.ProfileAnalysis) []ProfileAnalysis {
	result := make([]ProfileAnalysis, 0, len(items))
	for _, item := range items {
		functions := make([]FunctionStat, 0, len(item.Functions))
		for _, function := range item.Functions {
			functions = append(functions, FunctionStat{
				Function:   function.Function,
				Percentage: function.Percentage,
			})
		}
		result = append(result, ProfileAnalysis{
			ProfileType: string(item.Type),
			Functions:   functions,
		})
	}
	return result
}

func reportThresholds(thresholds []analysis.Threshold, violations []analysis.Violation) ThresholdResult {
	result := ThresholdResult{
		Configured: len(thresholds) > 0,
		Passed:     len(violations) == 0,
	}

	for _, threshold := range thresholds {
		result.Rules = append(result.Rules, ThresholdRule{
			ProfileType: string(threshold.Type),
			Percentage:  threshold.Percentage,
		})
	}
	sort.Slice(result.Rules, func(i, j int) bool {
		return result.Rules[i].ProfileType < result.Rules[j].ProfileType
	})

	for _, violation := range violations {
		result.Violations = append(result.Violations, ThresholdViolation{
			Function:    violation.Function,
			ProfileType: string(violation.Threshold.Type),
			Percentage:  violation.Percentage,
			Threshold:   violation.Threshold.Percentage,
		})
	}
	sort.Slice(result.Violations, func(i, j int) bool {
		if result.Violations[i].ProfileType == result.Violations[j].ProfileType {
			return result.Violations[i].Function < result.Violations[j].Function
		}
		return result.Violations[i].ProfileType < result.Violations[j].ProfileType
	})
	return result
}

// WriteReport writes a report atomically so readers never observe partial JSON.
func WriteReport(path string, report Report) error {
	if path == "" {
		return nil
	}
	normalizeReport(&report)
	if err := validateReport(report); err != nil {
		return fmt.Errorf("validating report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating report directory: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".proficiency-report-*")
	if err != nil {
		return fmt.Errorf("creating temporary report: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = file.Close()
		return fmt.Errorf("encoding report json: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("syncing report file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing report file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("installing report file: %w", err)
	}
	return nil
}

// ReadReport reads and validates a versioned report.
func ReadReport(path string) (Report, error) {
	file, err := os.Open(path) //nolint:gosec // the caller explicitly selects the baseline report
	if err != nil {
		return Report{}, fmt.Errorf("opening report: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return Report{}, fmt.Errorf("stating report: %w", err)
	}
	if info.Size() > maxReportSize {
		return Report{}, fmt.Errorf("report exceeds %d byte limit", maxReportSize)
	}

	decoder := json.NewDecoder(io.LimitReader(file, maxReportSize+1))
	var report Report
	if err := decoder.Decode(&report); err != nil {
		return Report{}, fmt.Errorf("decoding report json: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Report{}, errors.New("report contains multiple JSON values")
		}
		return Report{}, fmt.Errorf("decoding trailing report data: %w", err)
	}
	if err := validateReport(report); err != nil {
		return Report{}, fmt.Errorf("validating report: %w", err)
	}
	return report, nil
}

func normalizeReport(report *Report) {
	if report.Profiles == nil {
		report.Profiles = []ReportProfile{}
	}
	if report.Analysis == nil {
		report.Analysis = []ProfileAnalysis{}
	}
	if report.LoadStats != nil && report.LoadStats.Endpoints == nil {
		report.LoadStats.Endpoints = []ReportEndpointStats{}
	}
}

func validateReport(report Report) error {
	if report.SchemaVersion == "" {
		return errors.New("schemaVersion is required")
	}
	if report.SchemaVersion != ReportSchemaVersion {
		return fmt.Errorf("unsupported schema version %q", report.SchemaVersion)
	}
	if report.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if report.ToolVersion == "" {
		return errors.New("toolVersion is required")
	}
	if report.RunConfig.Mode == "" {
		return errors.New("runConfig.mode is required")
	}
	if report.RunConfig.TargetURL == "" {
		return errors.New("runConfig.targetUrl is required")
	}
	if report.RunConfig.PprofURL == "" {
		return errors.New("runConfig.pprofUrl is required")
	}
	if report.Profiles == nil {
		return errors.New("profiles must be an array")
	}
	if report.Analysis == nil {
		return errors.New("analysis must be an array")
	}
	if report.LoadStats != nil && report.LoadStats.Endpoints == nil {
		return errors.New("loadStats.endpoints must be an array")
	}
	if !report.Thresholds.Configured && !report.Thresholds.Passed {
		return errors.New("thresholds.passed must be true when thresholds are not configured")
	}
	return nil
}
