package profile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewCollector(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		cfg     CollectorConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: CollectorConfig{
				TargetURL: "http://localhost:6060",
				OutputDir: tmpDir,
			},
			wantErr: false,
		},
		{
			name: "missing target URL",
			cfg: CollectorConfig{
				OutputDir: tmpDir,
			},
			wantErr: true,
		},
		{
			name: "creates output dir",
			cfg: CollectorConfig{
				TargetURL: "http://localhost:6060",
				OutputDir: filepath.Join(tmpDir, "newdir"),
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCollector(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("NewCollector() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestCollector_CheckPprofAvailable(t *testing.T) {
	t.Run("pprof available", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/debug/pprof/" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("pprof index"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		cfg := CollectorConfig{
			TargetURL: server.URL,
			OutputDir: t.TempDir(),
		}
		collector, err := NewCollector(cfg)
		if err != nil {
			t.Fatalf("NewCollector failed: %v", err)
		}

		ctx := context.Background()
		if err := collector.CheckPprofAvailable(ctx); err != nil {
			t.Errorf("CheckPprofAvailable failed: %v", err)
		}
	})

	t.Run("pprof not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		cfg := CollectorConfig{
			TargetURL: server.URL,
			OutputDir: t.TempDir(),
		}
		collector, err := NewCollector(cfg)
		if err != nil {
			t.Fatalf("NewCollector failed: %v", err)
		}

		ctx := context.Background()
		err = collector.CheckPprofAvailable(ctx)
		if err == nil {
			t.Error("expected error for pprof not found")
		}
	})

	t.Run("server unreachable", func(t *testing.T) {
		cfg := CollectorConfig{
			TargetURL: "http://localhost:99999", // Invalid port
			OutputDir: t.TempDir(),
		}
		collector, err := NewCollector(cfg)
		if err != nil {
			t.Fatalf("NewCollector failed: %v", err)
		}

		ctx := context.Background()
		err = collector.CheckPprofAvailable(ctx)
		if err == nil {
			t.Error("expected error for unreachable server")
		}
	})
}

func TestCollector_CollectHeap(t *testing.T) {
	// Simulate pprof heap response
	fakeProfile := []byte("fake heap profile data for testing")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/heap" {
			w.WriteHeader(http.StatusOK)
			w.Write(fakeProfile)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cfg := CollectorConfig{
		TargetURL: server.URL,
		OutputDir: tmpDir,
		Timeout:   5 * time.Second,
	}

	collector, err := NewCollector(cfg)
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx := context.Background()
	profile, err := collector.CollectHeap(ctx)
	if err != nil {
		t.Fatalf("CollectHeap failed: %v", err)
	}

	// Verify profile metadata
	if profile.Type != ProfileHeap {
		t.Errorf("expected type %s, got %s", ProfileHeap, profile.Type)
	}

	if profile.Size != int64(len(fakeProfile)) {
		t.Errorf("expected size %d, got %d", len(fakeProfile), profile.Size)
	}

	// Verify file was created
	if _, err := os.Stat(profile.FilePath); os.IsNotExist(err) {
		t.Errorf("profile file not created: %s", profile.FilePath)
	}

	// Verify file contents
	data, err := os.ReadFile(profile.FilePath)
	if err != nil {
		t.Fatalf("failed to read profile file: %v", err)
	}

	if string(data) != string(fakeProfile) {
		t.Error("profile file contents don't match")
	}
}

func TestCollector_CollectCPU(t *testing.T) {
	// Simulate pprof CPU response (responds after delay)
	fakeProfile := []byte("fake CPU profile data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/profile" {
			// Check seconds parameter
			seconds := r.URL.Query().Get("seconds")
			if seconds == "" {
				t.Error("expected seconds parameter")
			}
			// Simulate short CPU profile collection
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			w.Write(fakeProfile)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cfg := CollectorConfig{
		TargetURL:   server.URL,
		OutputDir:   tmpDir,
		CPUDuration: 1 * time.Second, // Short for testing
		Timeout:     10 * time.Second,
	}

	collector, err := NewCollector(cfg)
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx := context.Background()
	profile, err := collector.CollectCPU(ctx)
	if err != nil {
		t.Fatalf("CollectCPU failed: %v", err)
	}

	if profile.Type != ProfileCPU {
		t.Errorf("expected type %s, got %s", ProfileCPU, profile.Type)
	}

	// Verify file was created
	if _, err := os.Stat(profile.FilePath); os.IsNotExist(err) {
		t.Errorf("profile file not created: %s", profile.FilePath)
	}
}

func TestCollector_CollectAll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/debug/pprof/profile":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("cpu profile"))
		case "/debug/pprof/heap":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("heap profile"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := CollectorConfig{
		TargetURL:   server.URL,
		OutputDir:   t.TempDir(),
		CPUDuration: 1 * time.Second,
		Timeout:     10 * time.Second,
	}

	collector, err := NewCollector(cfg)
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx := context.Background()
	profiles, err := collector.CollectAll(ctx)
	if err != nil {
		t.Fatalf("CollectAll failed: %v", err)
	}

	if len(profiles) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(profiles))
	}

	// Verify both types collected
	types := make(map[ProfileType]bool)
	for _, p := range profiles {
		types[p.Type] = true
	}

	if !types[ProfileCPU] {
		t.Error("CPU profile not collected")
	}
	if !types[ProfileHeap] {
		t.Error("heap profile not collected")
	}
}

func TestDefaultCollectorConfig(t *testing.T) {
	cfg := DefaultCollectorConfig()

	if cfg.OutputDir != "./profiles" {
		t.Errorf("expected output dir './profiles', got %s", cfg.OutputDir)
	}

	if cfg.CPUDuration != 30*time.Second {
		t.Errorf("expected CPU duration 30s, got %v", cfg.CPUDuration)
	}

	if cfg.Timeout != 60*time.Second {
		t.Errorf("expected timeout 60s, got %v", cfg.Timeout)
	}
}
