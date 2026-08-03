package proficiency

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tuxerrante/proficiency/internal/analysis"
	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/profile"
)

func TestBuildReport(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetURL = "http://localhost:8080"
	cfg.OpenAPIPath = "./openapi.yaml"
	cfg.ReportPath = "./profiles/report.json"
	cfg.Metadata = Metadata{Label: "current", Revision: "abc123"}
	cfg.ToolVersion = "v0.2.0"

	report := buildReport(
		cfg,
		[]*profile.CollectedProfile{
			{Type: profile.ProfileHeap, FilePath: "heap.pprof", Size: 20, Duration: time.Millisecond},
			{Type: profile.ProfileCPU, FilePath: "cpu.pprof", Size: 10, Duration: 2 * time.Second},
		},
		&load.Stats{
			TotalRequests: 9,
			SuccessCount:  8,
			ErrorCount:    1,
			Duration:      3 * time.Second,
			EndpointLatency: map[string]load.LatencyStats{
				"GET /z": {Count: 1, Min: time.Millisecond, Max: 3 * time.Millisecond, Avg: 2 * time.Millisecond, Total: 2 * time.Millisecond},
				"GET /a": {Count: 1, Min: time.Millisecond, Max: time.Millisecond, Avg: time.Millisecond, Total: time.Millisecond},
			},
		},
		[]analysis.ProfileAnalysis{
			{
				Type: analysis.CPU,
				Functions: []analysis.FunctionStat{
					{Function: "main.hot", Percentage: 42},
				},
			},
		},
		[]analysis.Threshold{{Type: analysis.CPU, Percentage: 30}},
		[]analysis.Violation{
			{
				Function:   "main.hot",
				Percentage: 42,
				Threshold:  analysis.Threshold{Type: analysis.CPU, Percentage: 30},
			},
		},
		time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC),
	)

	if report.SchemaVersion != ReportSchemaVersion {
		t.Fatalf("schema version = %q", report.SchemaVersion)
	}
	if report.Profiles[0].Type != "cpu" {
		t.Fatalf("profiles are not sorted: %+v", report.Profiles)
	}
	if report.LoadStats == nil || report.LoadStats.RequestsPerSecond != 3 {
		t.Fatalf("load stats = %+v", report.LoadStats)
	}
	if report.LoadStats.Endpoints[0].Endpoint != "GET /a" {
		t.Fatalf("endpoints are not sorted: %+v", report.LoadStats.Endpoints)
	}
	if len(report.Analysis) != 1 || report.Analysis[0].Functions[0].Function != "main.hot" {
		t.Fatalf("analysis = %+v", report.Analysis)
	}
	if report.Thresholds.Passed || len(report.Thresholds.Violations) != 1 {
		t.Fatalf("thresholds = %+v", report.Thresholds)
	}
}

func TestWriteAndReadReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	input := Report{
		SchemaVersion: ReportSchemaVersion,
		Timestamp:     time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC),
		ToolVersion:   "v0.2.0",
		RunConfig: ReportRunConfig{
			Mode:      modeSnapshot,
			TargetURL: "http://localhost:8080",
			PprofURL:  "http://localhost:8080",
		},
		LoadStats:  &ReportLoad{},
		Thresholds: ThresholdResult{Passed: true},
	}

	if err := WriteReport(path, input); err != nil {
		t.Fatalf("WriteReport() returned error: %v", err)
	}
	got, err := ReadReport(path)
	if err != nil {
		t.Fatalf("ReadReport() returned error: %v", err)
	}
	if got.ToolVersion != input.ToolVersion {
		t.Fatalf("tool version = %q", got.ToolVersion)
	}
	if got.Profiles == nil || got.Analysis == nil || got.LoadStats.Endpoints == nil {
		t.Fatalf("nil slices were not normalized: %+v", got)
	}
	if got.Thresholds.Rules == nil || got.Thresholds.Violations == nil {
		t.Fatalf("nil threshold slices were not normalized: %+v", got.Thresholds)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %o, want 600", info.Mode().Perm())
	}
}

func TestReadReportRejectsMissingRequiredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	payload := `{
		"schemaVersion": "v1",
		"timestamp": "2026-08-03T12:00:00Z",
		"toolVersion": "v0.2.0",
		"runConfig": {
			"mode": "snapshot",
			"targetUrl": "http://localhost:8080",
			"pprofUrl": "http://localhost:8080"
		},
		"profiles": null,
		"analysis": [],
		"thresholds": {"configured": false, "passed": true}
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadReport(path); err == nil {
		t.Fatal("ReadReport() unexpectedly accepted null profiles")
	}
}

func TestReadReportRejectsNullThresholdArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	payload := `{
		"schemaVersion": "v1",
		"timestamp": "2026-08-03T12:00:00Z",
		"toolVersion": "v0.2.0",
		"runConfig": {
			"mode": "snapshot",
			"targetUrl": "http://localhost:8080",
			"pprofUrl": "http://localhost:8080"
		},
		"profiles": [],
		"analysis": [],
		"thresholds": {
			"configured": false,
			"passed": true,
			"rules": null,
			"violations": []
		}
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadReport(path); err == nil {
		t.Fatal("ReadReport() unexpectedly accepted null threshold rules")
	}
}

func TestReadReportRejectsUnsupportedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":"v2"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadReport(path); err == nil {
		t.Fatal("ReadReport() unexpectedly accepted v2")
	}
}

func TestReadReportRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxReportSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadReport(path); err == nil {
		t.Fatal("ReadReport() unexpectedly accepted oversized input")
	}
}
