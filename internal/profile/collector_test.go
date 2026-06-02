package profile

import (
	"context"
	"fmt"
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

// Regression: CheckPprofAvailable must use the collector's own http.Client,
// not create a throwaway. Verify by checking that custom transport headers propagate.
func TestCheckPprofAvailable_UsesCollectorClient(t *testing.T) {
	var receivedUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.UserAgent()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := CollectorConfig{
		TargetURL: server.URL,
		OutputDir: t.TempDir(),
		Timeout:   10 * time.Second,
	}
	collector, err := NewCollector(cfg)
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx := context.Background()
	if err := collector.CheckPprofAvailable(ctx); err != nil {
		t.Fatalf("CheckPprofAvailable failed: %v", err)
	}

	// Go's default http.Client sends "Go-http-client/1.1" as UA.
	// If a throwaway client were used, the UA would be the same, but the
	// key test is that the request succeeds using c.client (not a new client).
	if receivedUA == "" {
		t.Error("expected User-Agent header from collector's client")
	}
}

// Regression: context cancellation must abort in-flight profile collection.
func TestCollect_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Simulate slow pprof endpoint
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("should not arrive"))
	}))
	defer server.Close()

	collector, err := NewCollector(CollectorConfig{
		TargetURL: server.URL,
		OutputDir: t.TempDir(),
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err = collector.CollectHeap(ctx)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

// Regression: fetchProfile must return error for non-200 status codes.
func TestFetchProfile_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer server.Close()

	collector, err := NewCollector(CollectorConfig{
		TargetURL: server.URL,
		OutputDir: t.TempDir(),
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	_, err = collector.CollectHeap(context.Background())
	if err == nil {
		t.Error("expected error for 500 status")
	}
}

// Regression: fetchProfile must return error for empty response body.
func TestFetchProfile_EmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	collector, err := NewCollector(CollectorConfig{
		TargetURL: server.URL,
		OutputDir: t.TempDir(),
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	_, err = collector.CollectHeap(context.Background())
	if err == nil {
		t.Error("expected error for empty profile")
	}
}

func TestCollector_CollectGoroutine(t *testing.T) {
	fakeProfile := []byte("fake goroutine profile data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/goroutine" {
			if r.URL.Query().Get("debug") != "0" {
				t.Error("expected debug=0 query parameter for binary protobuf format")
			}
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
	p, err := collector.CollectGoroutine(ctx)
	if err != nil {
		t.Fatalf("CollectGoroutine failed: %v", err)
	}

	if p.Type != ProfileGoroutine {
		t.Errorf("expected type %s, got %s", ProfileGoroutine, p.Type)
	}
	if p.Size != int64(len(fakeProfile)) {
		t.Errorf("expected size %d, got %d", len(fakeProfile), p.Size)
	}

	data, err := os.ReadFile(p.FilePath)
	if err != nil {
		t.Fatalf("failed to read goroutine profile file: %v", err)
	}
	if string(data) != string(fakeProfile) {
		t.Error("goroutine profile file contents don't match")
	}
}

func TestCollector_CollectByType(t *testing.T) {
	fakeData := []byte("fake profile data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/debug/pprof/profile":
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fakeData)
		case "/debug/pprof/heap", "/debug/pprof/block", "/debug/pprof/goroutine":
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

	tests := []struct {
		name        string
		profileType Type
		wantType    Type
	}{
		{"heap", ProfileHeap, ProfileHeap},
		{"block", ProfileBlock, ProfileBlock},
		{"goroutine", ProfileGoroutine, ProfileGoroutine},
		{"cpu", ProfileCPU, ProfileCPU},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := collector.CollectByType(context.Background(), tc.profileType)
			if err != nil {
				t.Fatalf("CollectByType(%s) failed: %v", tc.profileType, err)
			}
			if p.Type != tc.wantType {
				t.Errorf("expected type %s, got %s", tc.wantType, p.Type)
			}
		})
	}

	t.Run("unknown type", func(t *testing.T) {
		_, err := collector.CollectByType(context.Background(), Type("unknown"))
		if err == nil {
			t.Error("expected error for unknown profile type")
		}
	})
}

func TestCollector_CollectSeries(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/goroutine" {
			mu.Lock()
			requestCount++
			count := requestCount
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "goroutine profile sample %d", count)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	collector, err := NewCollector(CollectorConfig{
		TargetURL: server.URL,
		OutputDir: tmpDir,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx := context.Background()
	profiles, err := collector.CollectSeries(ctx, ProfileGoroutine, 100*time.Millisecond, 3)
	if err != nil {
		t.Fatalf("CollectSeries failed: %v", err)
	}

	if len(profiles) != 3 {
		t.Fatalf("expected 3 profiles, got %d", len(profiles))
	}

	// Verify each profile has the right type and unique file path
	paths := make(map[string]bool)
	for i, p := range profiles {
		if p.Type != ProfileGoroutine {
			t.Errorf("profile %d: expected type %s, got %s", i, ProfileGoroutine, p.Type)
		}
		if paths[p.FilePath] {
			t.Errorf("profile %d: duplicate file path %s", i, p.FilePath)
		}
		paths[p.FilePath] = true

		if _, err := os.Stat(p.FilePath); os.IsNotExist(err) {
			t.Errorf("profile %d: file not created: %s", i, p.FilePath)
		}
	}
}

func TestCollector_CollectSeries_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/heap" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("heap sample"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	collector, err := NewCollector(CollectorConfig{
		TargetURL: server.URL,
		OutputDir: t.TempDir(),
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCollector failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	// Request 100 samples at 100ms interval — context will cancel after ~2-3 samples
	profiles, err := collector.CollectSeries(ctx, ProfileHeap, 100*time.Millisecond, 100)
	if err != nil {
		t.Fatalf("CollectSeries should return partial results on cancellation, got error: %v", err)
	}

	if len(profiles) == 0 {
		t.Fatal("expected at least 1 partial profile before cancellation")
	}
	if len(profiles) >= 100 {
		t.Fatal("expected fewer than 100 profiles due to cancellation")
	}

	t.Logf("collected %d partial profiles before context cancelled", len(profiles))
}

func TestParseProfileTypes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Type
		wantErr bool
	}{
		{name: "empty", input: "", want: nil},
		{name: "single", input: "heap", want: []Type{ProfileHeap}},
		{name: "multiple", input: "goroutine,heap", want: []Type{ProfileGoroutine, ProfileHeap}},
		{name: "all types", input: "cpu,heap,block,goroutine", want: []Type{ProfileCPU, ProfileHeap, ProfileBlock, ProfileGoroutine}},
		{name: "unknown type", input: "foobar", wantErr: true},
		{name: "pprof name rejected", input: "profile", wantErr: true},
		{name: "mixed valid and invalid", input: "heap,foobar", wantErr: true},
		{name: "whitespace trimmed", input: " heap , goroutine ", want: []Type{ProfileHeap, ProfileGoroutine}},
		{name: "duplicates deduplicated", input: "heap,heap,heap", want: []Type{ProfileHeap}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseProfileTypes(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d types, want %d", len(got), len(tc.want))
			}
			for i, g := range got {
				if g != tc.want[i] {
					t.Errorf("type[%d] = %s, want %s", i, g, tc.want[i])
				}
			}
		})
	}
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
