package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/profile"
)

const reportSchemaVersion = "v1"

const (
	modeLoad     = "load"
	modeWatch    = "watch"
	modeSnapshot = "snapshot"
)

type runReport struct {
	SchemaVersion string          `json:"schemaVersion"`
	Timestamp     time.Time       `json:"timestamp"`
	ToolVersion   string          `json:"toolVersion"`
	RunConfig     reportRunConfig `json:"runConfig"`
	Profiles      []reportProfile `json:"profiles"`
	LoadStats     *reportLoad     `json:"loadStats,omitempty"`
	Thresholds    reportThreshold `json:"thresholds"`
}

type reportRunConfig struct {
	Mode             string `json:"mode"`
	OpenAPIPath      string `json:"openapiPath,omitempty"`
	TargetURL        string `json:"targetUrl"`
	PprofURL         string `json:"pprofUrl"`
	OutputDir        string `json:"outputDir"`
	ReportPath       string `json:"reportPath"`
	DurationMS       int64  `json:"durationMs"`
	CPUDurationMS    int64  `json:"cpuDurationMs"`
	Concurrency      int    `json:"concurrency"`
	RPS              int    `json:"rps"`
	SkipLoad         bool   `json:"skipLoad"`
	ProfileTypes     string `json:"profileTypes"`
	SampleIntervalMS int64  `json:"sampleIntervalMs"`
	SampleCount      int    `json:"sampleCount"`
	FailOn           string `json:"failOn,omitempty"`
	NoProgress       bool   `json:"noProgress"`
}

type reportProfile struct {
	Type       string `json:"type"`
	FilePath   string `json:"filePath"`
	SizeBytes  int64  `json:"sizeBytes"`
	DurationMS int64  `json:"durationMs"`
}

type reportLoad struct {
	TotalRequests int64                 `json:"totalRequests"`
	SuccessCount  int64                 `json:"successCount"`
	ErrorCount    int64                 `json:"errorCount"`
	DurationMS    int64                 `json:"durationMs"`
	Endpoints     []reportEndpointStats `json:"endpoints"`
}

type reportEndpointStats struct {
	Endpoint string `json:"endpoint"`
	Count    int64  `json:"count"`
	MinMS    int64  `json:"minMs"`
	MaxMS    int64  `json:"maxMs"`
	AvgMS    int64  `json:"avgMs"`
	TotalMS  int64  `json:"totalMs"`
}

type reportThreshold struct {
	Configured bool                       `json:"configured"`
	Passed     bool                       `json:"passed"`
	Rules      []reportThresholdRule      `json:"rules,omitempty"`
	Violations []reportThresholdViolation `json:"violations,omitempty"`
}

type reportThresholdRule struct {
	ProfileType string  `json:"profileType"`
	Percentage  float64 `json:"percentage"`
}

type reportThresholdViolation struct {
	Function    string  `json:"function"`
	ProfileType string  `json:"profileType"`
	Percentage  float64 `json:"percentage"`
	Threshold   float64 `json:"threshold"`
}

func buildRunReport(
	cfg Config,
	pprofURL string,
	profiles []*profile.CollectedProfile,
	loadStats *load.Stats,
	thresholds []analysis.Threshold,
	violations []analysis.Violation,
	now time.Time,
	toolVersion string,
) runReport {
	report := runReport{
		SchemaVersion: reportSchemaVersion,
		Timestamp:     now,
		ToolVersion:   toolVersion,
		RunConfig: reportRunConfig{
			Mode:             modeFromConfig(cfg),
			OpenAPIPath:      cfg.OpenAPIPath,
			TargetURL:        cfg.TargetURL,
			PprofURL:         pprofURL,
			OutputDir:        cfg.OutputDir,
			ReportPath:       cfg.ReportPath,
			DurationMS:       cfg.Duration.Milliseconds(),
			CPUDurationMS:    cfg.CPUDuration.Milliseconds(),
			Concurrency:      cfg.Concurrency,
			RPS:              cfg.RPS,
			SkipLoad:         cfg.SkipLoad,
			ProfileTypes:     cfg.ProfileTypes,
			SampleIntervalMS: cfg.SampleInterval.Milliseconds(),
			SampleCount:      cfg.SampleCount,
			FailOn:           cfg.FailOn,
			NoProgress:       cfg.NoProgress,
		},
		Profiles:   reportProfiles(profiles),
		LoadStats:  reportLoadStats(loadStats),
		Thresholds: reportThresholds(thresholds, violations),
	}

	return report
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

func reportProfiles(profiles []*profile.CollectedProfile) []reportProfile {
	if len(profiles) == 0 {
		return []reportProfile{}
	}

	items := make([]reportProfile, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, reportProfile{
			Type:       p.Type.DisplayName(),
			FilePath:   p.FilePath,
			SizeBytes:  p.Size,
			DurationMS: p.Duration.Milliseconds(),
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

func reportLoadStats(stats *load.Stats) *reportLoad {
	if stats == nil {
		return nil
	}

	keys := make([]string, 0, len(stats.EndpointLatency))
	for key := range stats.EndpointLatency {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	endpoints := make([]reportEndpointStats, 0, len(keys))
	for _, key := range keys {
		ls := stats.EndpointLatency[key]
		endpoints = append(endpoints, reportEndpointStats{
			Endpoint: key,
			Count:    ls.Count,
			MinMS:    ls.Min.Milliseconds(),
			MaxMS:    ls.Max.Milliseconds(),
			AvgMS:    ls.Avg.Milliseconds(),
			TotalMS:  ls.Total.Milliseconds(),
		})
	}

	return &reportLoad{
		TotalRequests: stats.TotalRequests,
		SuccessCount:  stats.SuccessCount,
		ErrorCount:    stats.ErrorCount,
		DurationMS:    stats.Duration.Milliseconds(),
		Endpoints:     endpoints,
	}
}

func reportThresholds(thresholds []analysis.Threshold, violations []analysis.Violation) reportThreshold {
	result := reportThreshold{
		Configured: len(thresholds) > 0,
		Passed:     len(violations) == 0,
	}

	if len(thresholds) > 0 {
		rules := make([]reportThresholdRule, 0, len(thresholds))
		for _, t := range thresholds {
			rules = append(rules, reportThresholdRule{
				ProfileType: string(t.Type),
				Percentage:  t.Percentage,
			})
		}
		sort.Slice(rules, func(i, j int) bool {
			return rules[i].ProfileType < rules[j].ProfileType
		})
		result.Rules = rules
	}

	if len(violations) > 0 {
		items := make([]reportThresholdViolation, 0, len(violations))
		for _, v := range violations {
			items = append(items, reportThresholdViolation{
				Function:    v.Function,
				ProfileType: string(v.Threshold.Type),
				Percentage:  v.Percentage,
				Threshold:   v.Threshold.Percentage,
			})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].ProfileType == items[j].ProfileType {
				return items[i].Function < items[j].Function
			}
			return items[i].ProfileType < items[j].ProfileType
		})
		result.Violations = items
	}

	return result
}

func writeRunReport(path string, report runReport) error {
	if path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating report directory: %w", err)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling report json: %w", err)
	}

	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing report file: %w", err)
	}

	return nil
}
