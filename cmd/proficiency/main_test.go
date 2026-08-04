package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		"--baseline", "./baseline.json",
		"--fail-on-regression", "latency:10:200us,cpu:5",
		"--top-functions", "12",
		"--label", "pull-request",
	})
	if err != nil {
		t.Fatalf("parseFlagsFromArgs() returned error: %v", err)
	}

	if cfg.ReportPath != "./profiles/report.json" {
		t.Fatalf("report path = %q", cfg.ReportPath)
	}
	if cfg.BaselinePath != "./baseline.json" {
		t.Fatalf("baseline path = %q", cfg.BaselinePath)
	}
	if cfg.FailOnRegression != "latency:10:200us,cpu:5" {
		t.Fatalf("regression rules = %q", cfg.FailOnRegression)
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

	cfg.FailOnRegression = "cpu:5"
	err := validateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "--baseline") {
		t.Fatalf("expected baseline validation error, got %v", err)
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

	if err := run(context.Background(), cfg, "test"); err != nil {
		t.Fatalf("run() returned error: %v", err)
	}

	if _, err := os.Stat(cfg.ReportPath); err != nil {
		t.Fatalf("report was not written: %v", err)
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name          string
		injected      string
		moduleVersion string
		sourceBuild   bool
		want          string
	}{
		{name: "release ldflags win", injected: "v0.2.1", moduleVersion: "v0.2.0", sourceBuild: true, want: "v0.2.1"},
		{name: "go install module version", injected: developmentVersion, moduleVersion: "v0.2.1", want: "v0.2.1"},
		{name: "tagged source checkout", injected: developmentVersion, moduleVersion: "v0.2.0", sourceBuild: true, want: developmentVersion},
		{name: "local devel build", injected: developmentVersion, moduleVersion: "(devel)", sourceBuild: true, want: developmentVersion},
		{name: "missing build info", injected: "", moduleVersion: "", want: developmentVersion},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveVersion(test.injected, test.moduleVersion, test.sourceBuild); got != test.want {
				t.Fatalf("resolveVersion() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestErrorExitCode(t *testing.T) {
	if got := errorExitCode(&proficiency.GateError{Regressions: 1}); got != exitGateErr {
		t.Fatalf("gate exit code = %d", got)
	}
	if got := errorExitCode(errors.New("runtime failure")); got != exitRuntimeErr {
		t.Fatalf("runtime exit code = %d", got)
	}
}
