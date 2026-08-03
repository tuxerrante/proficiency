package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tuxerrante/proficiency"
)

func TestParseFlagsFromArgs(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "owner/service")
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_REF", "refs/pull/7/merge")

	cfg, err := parseFlagsFromArgs([]string{
		"--target", "http://localhost:8080",
		"--openapi", "./api.yaml",
		"--report", "./profiles/report.json",
		"--top-functions", "12",
		"--label", "pull-request",
	})
	if err != nil {
		t.Fatalf("parseFlagsFromArgs() returned error: %v", err)
	}

	if cfg.ReportPath != "./profiles/report.json" {
		t.Fatalf("report path = %q", cfg.ReportPath)
	}
	if cfg.TopFunctions != 12 {
		t.Fatalf("top functions = %d", cfg.TopFunctions)
	}
	if cfg.Metadata.Repository != "owner/service" || cfg.Metadata.Revision != "abc123" {
		t.Fatalf("metadata = %+v", cfg.Metadata)
	}
}

func TestValidateConfig(t *testing.T) {
	cfg := defaultConfig()
	cfg.TargetURL = "http://localhost:8080"
	cfg.SkipLoad = true

	if err := validateConfig(cfg); err != nil {
		t.Fatalf("valid config returned error: %v", err)
	}
}

func TestRunSnapshotAdapter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pprof index"))
	})
	mux.HandleFunc("/debug/pprof/heap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("profile"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	cfg := Config{Config: proficiency.DefaultConfig()}
	cfg.TargetURL = server.URL
	cfg.SkipLoad = true
	cfg.ProfileTypes = "heap"
	cfg.TopFunctions = 0
	cfg.Duration = time.Second
	cfg.OutputDir = t.TempDir()
	cfg.ReportPath = filepath.Join(cfg.OutputDir, "report.json")

	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("run() returned error: %v", err)
	}

	if _, err := os.Stat(cfg.ReportPath); err != nil {
		t.Fatalf("report was not written: %v", err)
	}
}

func TestErrorExitCode(t *testing.T) {
	if got := errorExitCode(&proficiency.GateError{ThresholdViolations: 1}); got != exitGateErr {
		t.Fatalf("gate exit code = %d", got)
	}
	if got := errorExitCode(errors.New("runtime failure")); got != exitRuntimeErr {
		t.Fatalf("runtime exit code = %d", got)
	}
}
