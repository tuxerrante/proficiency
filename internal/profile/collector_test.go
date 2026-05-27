package profile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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
				_, _ = w.Write([]byte("pprof index"))
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
			_, _ = w.Write(fakeProfile)
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
			_, _ = w.Write(fakeProfile)
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
			_, _ = w.Write([]byte("cpu profile"))
		case "/debug/pprof/heap":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("heap profile"))
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
	types := make(map[Type]bool)
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

func TestCollector_CollectBlock(t *testing.T) {
	fakeProfile := []byte("fake block profile data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/block" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fakeProfile)
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
	p, err := collector.CollectBlock(ctx)
	if err != nil {
		t.Fatalf("CollectBlock failed: %v", err)
	}

	if p.Type != ProfileBlock {
		t.Errorf("expected type %s, got %s", ProfileBlock, p.Type)
	}
	if p.Size != int64(len(fakeProfile)) {
		t.Errorf("expected size %d, got %d", len(fakeProfile), p.Size)
	}

	data, err := os.ReadFile(p.FilePath)
	if err != nil {
		t.Fatalf("failed to read block profile file: %v", err)
	}
	if string(data) != string(fakeProfile) {
		t.Error("block profile file contents don't match")
	}
}

func TestCollector_ConcurrentCollection(t *testing.T) {
	// Verify that CPU, heap, and block collection can run concurrently.
	// This is the foundation of parallel profiling during load.
	fakeData := []byte("concurrent profile data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/debug/pprof/profile":
			time.Sleep(50 * time.Millisecond) // Simulate CPU collection delay
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fakeData)
		case "/debug/pprof/heap":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fakeData)
		case "/debug/pprof/block":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fakeData)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	collector, err := NewCollector(CollectorConfig{
		TargetURL:   server.URL,
		OutputDir:   t.TempDir(),
		CPUDuration: 1 * time.Second,
		Timeout:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx := context.Background()
	start := time.Now()

	var wg sync.WaitGroup
	var cpuProfile, heapProfile, blockProfile *CollectedProfile
	var cpuErr, heapErr, blockErr error

	wg.Add(3)
	go func() {
		defer wg.Done()
		cpuProfile, cpuErr = collector.CollectCPU(ctx)
	}()
	go func() {
		defer wg.Done()
		heapProfile, heapErr = collector.CollectHeap(ctx)
	}()
	go func() {
		defer wg.Done()
		blockProfile, blockErr = collector.CollectBlock(ctx)
	}()
	wg.Wait()

	elapsed := time.Since(start)

	if cpuErr != nil {
		t.Errorf("concurrent CPU collection failed: %v", cpuErr)
	}
	if heapErr != nil {
		t.Errorf("concurrent heap collection failed: %v", heapErr)
	}
	if blockErr != nil {
		t.Errorf("concurrent block collection failed: %v", blockErr)
	}
	if cpuProfile == nil {
		t.Fatal("CPU profile is nil")
	}
	if heapProfile == nil {
		t.Fatal("heap profile is nil")
	}
	if blockProfile == nil {
		t.Fatal("block profile is nil")
	}

	// All profiles should be saved to different files.
	paths := map[string]bool{
		cpuProfile.FilePath:   true,
		heapProfile.FilePath:  true,
		blockProfile.FilePath: true,
	}
	if len(paths) != 3 {
		t.Error("CPU, heap, and block profiles should have different file paths")
	}

	t.Logf("Concurrent collection of 3 profiles completed in %v", elapsed)
}

func TestDefaultCollectorConfig(t *testing.T) {
	cfg := DefaultCollectorConfig()

	if cfg.OutputDir != defaultOutputDir {
		t.Errorf("expected output dir %q, got %s", defaultOutputDir, cfg.OutputDir)
	}

	if cfg.CPUDuration != 30*time.Second {
		t.Errorf("expected CPU duration 30s, got %v", cfg.CPUDuration)
	}

	if cfg.Timeout != 60*time.Second {
		t.Errorf("expected timeout 60s, got %v", cfg.Timeout)
	}
}
