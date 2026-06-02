package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuxerrante/proficiency/internal/profile"
)

func TestValidateConfig(t *testing.T) {
	// Create a temp OpenAPI spec file for tests that need one.
	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte("openapi: 3.0.0"), 0o600); err != nil {
		t.Fatal(err)
	}

	validBase := Config{
		TargetURL:    "http://localhost:8080",
		OpenAPIPath:  specPath,
		Duration:     30 * time.Second,
		Concurrency:  10,
		RPS:          100,
		ProfileTypes: "cpu,heap,block",
	}

	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{
			name:   "valid default config",
			modify: func(_ *Config) {},
		},
		{
			name:    "missing target",
			modify:  func(c *Config) { c.TargetURL = "" },
			wantErr: "--target is required",
		},
		{
			name:    "missing openapi with load",
			modify:  func(c *Config) { c.OpenAPIPath = "" },
			wantErr: "--openapi is required when load generation is enabled",
		},
		{
			name: "skip-load without openapi is valid",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
			},
		},
		{
			name:    "openapi file not found",
			modify:  func(c *Config) { c.OpenAPIPath = "/nonexistent/spec.yaml" },
			wantErr: "OpenAPI spec file not found",
		},
		{
			name:    "sample-interval requires skip-load",
			modify:  func(c *Config) { c.SampleInterval = 2 * time.Second },
			wantErr: "--sample-interval requires --skip-load",
		},
		{
			name:    "negative duration",
			modify:  func(c *Config) { c.Duration = -1 },
			wantErr: "--duration must be positive",
		},
		{
			name:    "zero concurrency",
			modify:  func(c *Config) { c.Concurrency = 0 },
			wantErr: "--concurrency must be positive",
		},
		{
			name:    "zero rps",
			modify:  func(c *Config) { c.RPS = 0 },
			wantErr: "--rps must be positive",
		},
		{
			name: "sample-interval below minimum",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 100 * time.Millisecond
			},
			wantErr: "--sample-interval must be at least 500ms",
		},
		{
			name: "sample-count without sample-interval",
			modify: func(c *Config) {
				c.SampleCount = 5
			},
			wantErr: "--sample-count requires --sample-interval",
		},
		{
			name: "valid watch mode",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 2 * time.Second
				c.SampleCount = 5
				c.ProfileTypes = "goroutine,heap"
			},
		},
		{
			name:    "invalid profile type",
			modify:  func(c *Config) { c.ProfileTypes = "foobar" },
			wantErr: "invalid --profile-types",
		},
		{
			name: "cpu incompatible with sample-interval",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 2 * time.Second
				c.SampleCount = 3
				c.ProfileTypes = "cpu,heap"
			},
			wantErr: "CPU profiles are incompatible with --sample-interval",
		},
		{
			name: "goroutine only in watch mode is valid",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 1 * time.Second
				c.SampleCount = 10
				c.ProfileTypes = "goroutine"
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBase
			tc.modify(&cfg)

			err := validateConfig(cfg)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// fakePprofData returns minimal non-empty bytes that the collector accepts
// as profile data. Real pprof parsing is not done by run/runSkipLoad, so
// any non-empty payload works.
var fakePprofData = []byte("fake-pprof-profile-data")

// newPprofServer creates an httptest server that responds to pprof endpoints.
// If apiHandler is non-nil it is also registered to handle non-pprof paths.
func newPprofServer(t *testing.T, apiHandler http.Handler) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pprof index"))
	})
	mux.HandleFunc("/debug/pprof/heap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fakePprofData)
	})
	mux.HandleFunc("/debug/pprof/block", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fakePprofData)
	})
	mux.HandleFunc("/debug/pprof/goroutine", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fakePprofData)
	})
	mux.HandleFunc("/debug/pprof/profile", func(w http.ResponseWriter, r *http.Request) {
		// Honour "seconds" param with a short sleep so CPU collection
		// doesn't return instantly (the collector constructs the URL with it).
		_ = r.URL.Query().Get("seconds")
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(fakePprofData)
	})

	if apiHandler != nil {
		mux.Handle("/", apiHandler)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// --- Tests for run() ---------------------------------------------------------

func TestRun_SkipLoad(t *testing.T) {
	srv := newPprofServer(t, nil)
	outDir := t.TempDir()

	cfg := Config{
		TargetURL:    srv.URL,
		SkipLoad:     true,
		Duration:     5 * time.Second,
		Concurrency:  1,
		RPS:          1,
		OutputDir:    outDir,
		CPUDuration:  1 * time.Second,
		ProfileTypes: "heap",
	}

	ctx := context.Background()
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run() returned error: %v", err)
	}

	// Verify at least one profile file was written.
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected profile files in output dir, got none")
	}
}

func TestRun_WatchMode(t *testing.T) {
	srv := newPprofServer(t, nil)
	outDir := t.TempDir()

	cfg := Config{
		TargetURL:      srv.URL,
		SkipLoad:       true,
		Duration:       5 * time.Second,
		Concurrency:    1,
		RPS:            1,
		OutputDir:      outDir,
		CPUDuration:    1 * time.Second,
		SampleInterval: 500 * time.Millisecond,
		SampleCount:    2,
		ProfileTypes:   "goroutine",
	}

	ctx := context.Background()
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run() returned error: %v", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 goroutine samples, got %d files", len(entries))
	}
}

func TestRun_WithLoad(t *testing.T) {
	// The httptest server must handle BOTH the API path and pprof paths.
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := newPprofServer(t, apiHandler)

	// Write a minimal OpenAPI spec to a temp file.
	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	specContent := `openapi: "3.0.0"
info:
  title: Test
  version: "1.0"
paths:
  /test:
    get:
      operationId: test
      responses:
        "200":
          description: OK
`
	if err := os.WriteFile(specPath, []byte(specContent), 0o600); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()

	cfg := Config{
		TargetURL:    srv.URL,
		OpenAPIPath:  specPath,
		SkipLoad:     false,
		Duration:     2 * time.Second,
		Concurrency:  1,
		RPS:          1,
		OutputDir:    outDir,
		CPUDuration:  1 * time.Second,
		ProfileTypes: "heap",
	}

	ctx := context.Background()
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run() returned error: %v", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected profile files in output dir, got none")
	}
}

// --- Tests for lower-level helpers -------------------------------------------

func TestRunSkipLoad(t *testing.T) {
	srv := newPprofServer(t, nil)
	outDir := t.TempDir()

	collectorCfg := profile.CollectorConfig{
		TargetURL:   srv.URL,
		OutputDir:   outDir,
		CPUDuration: 1 * time.Second,
	}

	collector, err := profile.NewCollector(collectorCfg)
	if err != nil {
		t.Fatalf("creating collector: %v", err)
	}

	cfg := Config{
		ProfileTypes: "heap",
		OutputDir:    outDir,
	}

	ctx := context.Background()
	profiles, err := runSkipLoad(ctx, cfg, collector, []profile.Type{profile.ProfileHeap})
	if err != nil {
		t.Fatalf("runSkipLoad() returned error: %v", err)
	}

	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Type != profile.ProfileHeap {
		t.Fatalf("expected heap profile, got %s", profiles[0].Type)
	}
}

func TestRunWatchMode(t *testing.T) {
	srv := newPprofServer(t, nil)
	outDir := t.TempDir()

	collectorCfg := profile.CollectorConfig{
		TargetURL:   srv.URL,
		OutputDir:   outDir,
		CPUDuration: 1 * time.Second,
	}

	collector, err := profile.NewCollector(collectorCfg)
	if err != nil {
		t.Fatalf("creating collector: %v", err)
	}

	cfg := Config{
		ProfileTypes:   "goroutine",
		SampleInterval: 500 * time.Millisecond,
		SampleCount:    2,
		Duration:       5 * time.Second,
	}

	ctx := context.Background()
	profiles, err := runWatchMode(ctx, cfg, collector, []profile.Type{profile.ProfileGoroutine})
	if err != nil {
		t.Fatalf("runWatchMode() returned error: %v", err)
	}

	if len(profiles) < 2 {
		t.Fatalf("expected at least 2 goroutine samples, got %d", len(profiles))
	}
	for _, p := range profiles {
		if p.Type != profile.ProfileGoroutine {
			t.Fatalf("expected goroutine profile, got %s", p.Type)
		}
	}
}

func TestPrintAnalysisHints(t *testing.T) {
	tests := []struct {
		name         string
		profileTypes []profile.Type
		wantContains []string
	}{
		{
			name:         "cpu hints",
			profileTypes: []profile.Type{profile.ProfileCPU},
			wantContains: []string{"cpu_*.pprof", "CPU interactive CLI", "CPU web UI"},
		},
		{
			name:         "heap hints",
			profileTypes: []profile.Type{profile.ProfileHeap},
			wantContains: []string{"heap_*.pprof", "heap flamegraph"},
		},
		{
			name:         "block hints",
			profileTypes: []profile.Type{profile.ProfileBlock},
			wantContains: []string{"block_*.pprof", "I/O blocking"},
		},
		{
			name:         "goroutine hints",
			profileTypes: []profile.Type{profile.ProfileGoroutine},
			wantContains: []string{"goroutine_*.pprof", "goroutine stacks"},
		},
		{
			name:         "multiple types",
			profileTypes: []profile.Type{profile.ProfileCPU, profile.ProfileHeap},
			wantContains: []string{"cpu_*.pprof", "heap_*.pprof"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Capture stdout by redirecting os.Stdout.
			old := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Stdout = w

			printAnalysisHints("/tmp/profiles", tc.profileTypes)

			_ = w.Close()
			os.Stdout = old

			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			output := buf.String()

			for _, want := range tc.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output missing expected string %q\ngot:\n%s", want, output)
				}
			}
		})
	}
}

// TestPrintAnalysisHints_OutputDir verifies the output directory placeholder
// appears correctly in the printed hints.
func TestPrintAnalysisHints_OutputDir(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	printAnalysisHints("/custom/dir", []profile.Type{profile.ProfileHeap})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "/custom/dir") {
		t.Errorf("expected output to contain /custom/dir, got:\n%s", output)
	}
}
