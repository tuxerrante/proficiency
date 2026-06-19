package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/profile"
)

func TestParseFlagsFromArgs_ReportPath(t *testing.T) {
	cfg, err := parseFlagsFromArgs([]string{
		"--target", "http://localhost:8080",
		"--openapi", "./testdata/openapi.yaml",
		"--report", "./profiles/report.json",
	})
	if err != nil {
		t.Fatalf("parseFlagsFromArgs() returned error: %v", err)
	}

	if cfg.ReportPath != "./profiles/report.json" {
		t.Fatalf("expected report path to be parsed, got %q", cfg.ReportPath)
	}
}

func TestBuildRunReport_IncludesLoadAndThresholdData(t *testing.T) {
	now := time.Date(2026, time.June, 19, 17, 30, 0, 0, time.UTC)
	cfg := Config{
		OpenAPIPath:    "./spec.yaml",
		TargetURL:      "http://localhost:8080",
		Duration:       30 * time.Second,
		Concurrency:    5,
		RPS:            100,
		OutputDir:      "./profiles",
		CPUDuration:    10 * time.Second,
		FailOn:         "cpu:30",
		ProfileTypes:   "cpu,heap",
		ReportPath:     "./profiles/report.json",
		SampleInterval: 0,
	}

	profiles := []*profile.CollectedProfile{
		{
			Type:     profile.ProfileHeap,
			FilePath: "./profiles/heap_1.pprof",
			Size:     512,
			Duration: 120 * time.Millisecond,
		},
		{
			Type:     profile.ProfileCPU,
			FilePath: "./profiles/cpu_1.pprof",
			Size:     1024,
			Duration: 2 * time.Second,
		},
	}

	loadStats := &load.Stats{
		TotalRequests: 100,
		SuccessCount:  98,
		ErrorCount:    2,
		Duration:      31 * time.Second,
		EndpointLatency: map[string]load.LatencyStats{
			"GET /z": {Count: 1, Min: 50 * time.Millisecond, Max: 50 * time.Millisecond, Avg: 50 * time.Millisecond, Total: 50 * time.Millisecond},
			"GET /a": {Count: 2, Min: 10 * time.Millisecond, Max: 40 * time.Millisecond, Avg: 25 * time.Millisecond, Total: 50 * time.Millisecond},
		},
	}

	thresholds := []analysis.Threshold{
		{Type: analysis.CPU, Percentage: 30},
	}
	violations := []analysis.Violation{
		{
			Function:   "main.hotPath",
			Percentage: 42.5,
			Threshold: analysis.Threshold{
				Type:       analysis.CPU,
				Percentage: 30,
			},
		},
	}

	report := buildRunReport(
		cfg,
		"http://localhost:8080",
		profiles,
		loadStats,
		thresholds,
		violations,
		now,
		"v0.1.2",
	)

	if report.SchemaVersion != reportSchemaVersion {
		t.Fatalf("unexpected schema version: %q", report.SchemaVersion)
	}
	if !report.Timestamp.Equal(now) {
		t.Fatalf("unexpected timestamp: %v", report.Timestamp)
	}
	if report.ToolVersion != "v0.1.2" {
		t.Fatalf("unexpected tool version: %q", report.ToolVersion)
	}
	if report.RunConfig.Mode != "load" {
		t.Fatalf("expected mode=load, got %q", report.RunConfig.Mode)
	}

	if len(report.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(report.Profiles))
	}
	if report.Profiles[0].Type != "cpu" {
		t.Fatalf("expected cpu profile first after sorting, got %q", report.Profiles[0].Type)
	}

	if report.LoadStats == nil {
		t.Fatal("expected load stats in report")
	}
	if len(report.LoadStats.Endpoints) != 2 {
		t.Fatalf("expected endpoint stats, got %d entries", len(report.LoadStats.Endpoints))
	}
	if report.LoadStats.Endpoints[0].Endpoint != "GET /a" {
		t.Fatalf("expected sorted endpoint stats, got %q first", report.LoadStats.Endpoints[0].Endpoint)
	}

	if !report.Thresholds.Configured {
		t.Fatal("expected configured thresholds")
	}
	if report.Thresholds.Passed {
		t.Fatal("expected thresholds to fail due to violations")
	}
	if len(report.Thresholds.Violations) != 1 {
		t.Fatalf("expected 1 threshold violation, got %d", len(report.Thresholds.Violations))
	}
}

func TestBuildRunReport_WithoutThresholds(t *testing.T) {
	cfg := Config{
		TargetURL:    "http://localhost:8080",
		OutputDir:    "./profiles",
		Duration:     10 * time.Second,
		CPUDuration:  5 * time.Second,
		ProfileTypes: "heap",
		SkipLoad:     true,
	}

	report := buildRunReport(
		cfg,
		cfg.TargetURL,
		nil,
		nil,
		nil,
		nil,
		time.Now().UTC(),
		"dev",
	)

	if report.Thresholds.Configured {
		t.Fatal("expected configured=false when no thresholds are passed")
	}
	if !report.Thresholds.Passed {
		t.Fatal("expected passed=true when no thresholds are configured")
	}
	if report.LoadStats != nil {
		t.Fatal("expected no load stats in skip-load mode")
	}
	if report.RunConfig.Mode != "snapshot" {
		t.Fatalf("expected snapshot mode, got %q", report.RunConfig.Mode)
	}
}

func TestWriteRunReport_CreatesFile(t *testing.T) {
	targetPath := filepath.Join(t.TempDir(), "nested", "report.json")

	input := runReport{
		SchemaVersion: reportSchemaVersion,
		Timestamp:     time.Date(2026, time.June, 19, 17, 31, 0, 0, time.UTC),
		ToolVersion:   "dev",
		RunConfig: reportRunConfig{
			Mode:       "snapshot",
			TargetURL:  "http://localhost:8080",
			PprofURL:   "http://localhost:8080",
			OutputDir:  "./profiles",
			ReportPath: targetPath,
		},
		Thresholds: reportThreshold{
			Configured: false,
			Passed:     true,
		},
	}

	if err := writeRunReport(targetPath, input); err != nil {
		t.Fatalf("writeRunReport() returned error: %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("reading report file: %v", err)
	}

	var got runReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal report json: %v", err)
	}

	if got.SchemaVersion != reportSchemaVersion {
		t.Fatalf("unexpected schema version in file: %q", got.SchemaVersion)
	}
	if got.RunConfig.Mode != "snapshot" {
		t.Fatalf("unexpected mode in file: %q", got.RunConfig.Mode)
	}
}
