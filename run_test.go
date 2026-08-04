package proficiency

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pprofProfile "github.com/google/pprof/profile"
	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/profile"
)

func TestRunSnapshot(t *testing.T) {
	server := newTestTarget(t, nil)
	cfg := DefaultConfig()
	cfg.TargetURL = server.URL
	cfg.SkipLoad = true
	cfg.ProfileTypes = "heap"
	cfg.TopFunctions = 0
	cfg.Duration = time.Second
	cfg.OutputDir = t.TempDir()
	cfg.ReportPath = filepath.Join(cfg.OutputDir, "report.json")
	cfg.Metadata.Label = "snapshot"
	var output bytes.Buffer
	cfg.Output = &output
	cfg.ErrorOutput = &output

	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if report.RunConfig.Mode != modeSnapshot || len(report.Profiles) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(cfg.ReportPath); err != nil {
		t.Fatalf("report file missing: %v", err)
	}
}

func TestRunWithLoad(t *testing.T) {
	server := newTestTarget(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	spec := `openapi: 3.0.0
info:
  title: test
  version: "1"
paths:
  /test:
    get:
      responses:
        "200":
          description: ok
`
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.TargetURL = server.URL
	cfg.OpenAPIPath = specPath
	cfg.ProfileTypes = "heap"
	cfg.TopFunctions = 0
	cfg.Duration = 250 * time.Millisecond
	cfg.Concurrency = 1
	cfg.RPS = 20
	cfg.OutputDir = t.TempDir()

	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if report.LoadStats == nil || report.LoadStats.TotalRequests == 0 {
		t.Fatalf("load stats = %+v", report.LoadStats)
	}
}

func TestRunWatchMode(t *testing.T) {
	server := newTestTarget(t, nil)
	cfg := DefaultConfig()
	cfg.TargetURL = server.URL
	cfg.SkipLoad = true
	cfg.ProfileTypes = "goroutine"
	cfg.TopFunctions = 0
	cfg.SampleInterval = 500 * time.Millisecond
	cfg.SampleCount = 2
	cfg.Duration = 2 * time.Second
	cfg.OutputDir = t.TempDir()

	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if report.RunConfig.Mode != modeWatch || len(report.Profiles) != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunWritesReportBeforeReturningThresholdGateError(t *testing.T) {
	profileData := thresholdProfileData(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pprof index"))
	})
	mux.HandleFunc("/debug/pprof/heap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(profileData)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	cfg := DefaultConfig()
	cfg.TargetURL = server.URL
	cfg.SkipLoad = true
	cfg.ProfileTypes = "heap"
	cfg.TopFunctions = 1
	cfg.FailOn = "alloc:0"
	cfg.Duration = time.Second
	cfg.OutputDir = t.TempDir()
	cfg.ReportPath = filepath.Join(cfg.OutputDir, "report.json")

	report, err := Run(context.Background(), cfg)
	var gateErr *GateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("Run() error = %v, want GateError", err)
	}
	if report == nil || report.Thresholds.Passed {
		t.Fatalf("report thresholds = %+v", report)
	}
	if _, err := ReadReport(cfg.ReportPath); err != nil {
		t.Fatalf("threshold report was not persisted: %v", err)
	}
}

func TestGateError(t *testing.T) {
	err := &GateError{ThresholdViolations: 2}
	var gateErr *GateError
	if !errors.As(err, &gateErr) {
		t.Fatal("GateError is not discoverable with errors.As")
	}
}

func TestOutputHelpers(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	printThresholds(
		&stdout,
		&stderr,
		[]analysis.Threshold{{Type: analysis.CPU, Percentage: 20}},
		nil,
	)
	if !strings.Contains(stdout.String(), "PASS") {
		t.Fatalf("threshold output = %q", stdout.String())
	}

	stdout.Reset()
	printThresholds(
		&stdout,
		&stderr,
		[]analysis.Threshold{{Type: analysis.CPU, Percentage: 20}},
		[]analysis.Violation{
			{
				Function:   "main.hot",
				Percentage: 30,
				Threshold:  analysis.Threshold{Type: analysis.CPU, Percentage: 20},
			},
		},
	)
	if !strings.Contains(stderr.String(), "main.hot") {
		t.Fatalf("threshold error output = %q", stderr.String())
	}

	stdout.Reset()
	printAnalysisHints(&stdout, "/profiles", []profile.Type{
		profile.ProfileCPU,
		profile.ProfileHeap,
		profile.ProfileBlock,
		profile.ProfileGoroutine,
	})
	if !strings.Contains(stdout.String(), "cpu_*.pprof") ||
		!strings.Contains(stdout.String(), "goroutine_*.pprof") {
		t.Fatalf("helper output = %q", stdout.String())
	}
	if isTerminalWriter(&stdout) {
		t.Fatal("bytes.Buffer unexpectedly detected as a terminal")
	}
}

func thresholdProfileData(t *testing.T) []byte {
	t.Helper()

	function := &pprofProfile.Function{ID: 1, Name: "main.hot"}
	location := &pprofProfile.Location{
		ID:   1,
		Line: []pprofProfile.Line{{Function: function}},
	}
	profileData := &pprofProfile.Profile{
		SampleType: []*pprofProfile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
		Function: []*pprofProfile.Function{function},
		Location: []*pprofProfile.Location{location},
		Sample: []*pprofProfile.Sample{
			{Location: []*pprofProfile.Location{location}, Value: []int64{1, 100}},
		},
	}

	var buffer bytes.Buffer
	if err := profileData.Write(&buffer); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func newTestTarget(t *testing.T, apiHandler http.Handler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pprof index"))
	})
	for _, path := range []string{"/debug/pprof/heap", "/debug/pprof/block", "/debug/pprof/goroutine"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("profile"))
		})
	}
	if apiHandler != nil {
		mux.Handle("/", apiHandler)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
