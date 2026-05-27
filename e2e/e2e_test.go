//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxerrante/proficiency/internal/load"
	"github.com/tuxerrante/proficiency/internal/openapi"
	"github.com/tuxerrante/proficiency/internal/profile"
)

// TestParallelProfiling verifies that CPU and heap profiles are collected
// concurrently with load generation, not sequentially.
func TestParallelProfiling(t *testing.T) {
	serverBin := buildTestServer(t)
	cmd := startTestServer(t, serverBin)
	defer func() { _ = cmd.Process.Kill() }()

	waitForServer(t, "http://localhost:8080/health")

	parser := openapi.NewParser()
	ctx := context.Background()
	endpoints, err := parser.ParseFile(ctx, filepath.Join("openapi.yaml"))
	if err != nil {
		t.Fatalf("failed to parse spec: %v", err)
	}

	if len(endpoints) == 0 {
		t.Fatal("no endpoints parsed from spec")
	}

	profileDir := t.TempDir()
	collector, err := profile.NewCollector(profile.CollectorConfig{
		TargetURL:   "http://localhost:8080",
		OutputDir:   profileDir,
		CPUDuration: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create collector: %v", err)
	}

	if err := collector.CheckPprofAvailable(ctx); err != nil {
		t.Fatalf("pprof not available: %v", err)
	}

	// Run load + profiling concurrently and measure wall time.
	var (
		wg                      sync.WaitGroup
		loadStats               *load.Stats
		loadErr                 error
		cpuProfile, heapProfile *profile.CollectedProfile
		cpuErr, heapErr         error
	)

	start := time.Now()

	wg.Add(1)
	go func() {
		defer wg.Done()
		cpuProfile, cpuErr = collector.CollectCPU(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runner := load.NewRunner(load.Config{
			Concurrency: 5,
			RPS:         50,
			Duration:    10 * time.Second,
			Timeout:     5 * time.Second,
		})
		loadStats, loadErr = runner.Run(ctx, "http://localhost:8080", endpoints)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Collect heap at 80% through the load.
		time.Sleep(8 * time.Second)
		heapProfile, heapErr = collector.CollectHeap(ctx)
	}()

	wg.Wait()
	elapsed := time.Since(start)

	// --- Assertions ---

	if loadErr != nil {
		t.Fatalf("load test failed: %v", loadErr)
	}
	if cpuErr != nil {
		t.Fatalf("CPU profile collection failed: %v", cpuErr)
	}
	if heapErr != nil {
		t.Fatalf("heap profile collection failed: %v", heapErr)
	}

	// Parallel execution: wall time should be ~10s (max of load/CPU duration),
	// not ~20s (sequential load + CPU).
	if elapsed > 15*time.Second {
		t.Errorf("parallel execution took %v; expected ~10s (not sequential ~20s)", elapsed)
	}
	t.Logf("parallel execution completed in %v", elapsed)

	// Load test produced requests.
	if loadStats.TotalRequests == 0 {
		t.Error("load test made zero requests")
	}
	t.Logf("load: %d requests (%d success, %d errors)",
		loadStats.TotalRequests, loadStats.SuccessCount, loadStats.ErrorCount)

	// Profiles are non-empty.
	if cpuProfile.Size == 0 {
		t.Error("CPU profile is empty")
	}
	if heapProfile.Size == 0 {
		t.Error("heap profile is empty")
	}
	t.Logf("CPU profile: %s (%d bytes)", cpuProfile.FilePath, cpuProfile.Size)
	t.Logf("heap profile: %s (%d bytes)", heapProfile.FilePath, heapProfile.Size)

	// Analyze CPU profile for expected functions.
	analyzeCPUProfile(t, cpuProfile.FilePath)
	analyzeHeapProfile(t, heapProfile.FilePath)
}

// TestProfilesDuringLoad verifies profiles contain functions that are only
// active during load generation (i.e., profiling happened while load was running).
func TestProfilesDuringLoad(t *testing.T) {
	serverBin := buildTestServer(t)
	cmd := startTestServer(t, serverBin)
	defer func() { _ = cmd.Process.Kill() }()

	waitForServer(t, "http://localhost:8080/health")

	profileDir := t.TempDir()
	collector, err := profile.NewCollector(profile.CollectorConfig{
		TargetURL:   "http://localhost:8080",
		OutputDir:   profileDir,
		CPUDuration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create collector: %v", err)
	}

	ctx := context.Background()

	// Hit only the CPU endpoint during profiling.
	cpuEndpoints := []openapi.Endpoint{
		{Method: "GET", Path: "/stress/cpu"},
	}

	var wg sync.WaitGroup
	var cpuProfile *profile.CollectedProfile
	var cpuErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		cpuProfile, cpuErr = collector.CollectCPU(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runner := load.NewRunner(load.Config{
			Concurrency: 3,
			RPS:         30,
			Duration:    5 * time.Second,
			Timeout:     5 * time.Second,
		})
		_, _ = runner.Run(ctx, "http://localhost:8080", cpuEndpoints)
	}()

	wg.Wait()

	if cpuErr != nil {
		t.Fatalf("CPU profile failed: %v", cpuErr)
	}

	// The CPU profile MUST contain math functions from the /stress/cpu handler
	// because profiling ran concurrently with load hitting that endpoint.
	out, err := exec.Command("go", "tool", "pprof", "-top", cpuProfile.FilePath).CombinedOutput()
	if err != nil {
		t.Fatalf("go tool pprof failed: %v\n%s", err, out)
	}

	output := string(out)
	t.Logf("CPU profile (during /stress/cpu load):\n%s", output)

	// math.Tan or math.Atan should appear since the CPU endpoint uses them.
	if !strings.Contains(output, "math") {
		t.Error("expected math functions in CPU profile (profiling may not have captured load)")
	}
}

func buildTestServer(t *testing.T) string {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "testserver")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "testserver"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build test server: %v\n%s", err, out)
	}

	return binPath
}

func startTestServer(t *testing.T, binPath string) *exec.Cmd {
	t.Helper()

	cmd := exec.Command(binPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test server: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		// Remove stress.db created by the test server.
		os.Remove("stress.db")
	})

	return cmd
}

func waitForServer(t *testing.T, healthURL string) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	for i := range 30 {
		resp, err := client.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return
		}
		if i == 29 {
			t.Fatal("test server did not become ready within 15s")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func analyzeCPUProfile(t *testing.T, path string) {
	t.Helper()

	out, err := exec.Command("go", "tool", "pprof", "-top", path).CombinedOutput()
	if err != nil {
		t.Logf("Warning: could not analyze CPU profile: %v", err)
		return
	}

	output := string(out)
	t.Logf("CPU profile top:\n%s", output)

	if !strings.Contains(output, "math") {
		t.Log("Note: math functions not prominent in CPU profile (may need higher load)")
	}
}

func analyzeHeapProfile(t *testing.T, path string) {
	t.Helper()

	out, err := exec.Command("go", "tool", "pprof", "-top", "-alloc_space", path).CombinedOutput()
	if err != nil {
		t.Logf("Warning: could not analyze heap profile: %v", err)
		return
	}

	t.Logf("Heap profile top (alloc_space):\n%s", string(out))
}
