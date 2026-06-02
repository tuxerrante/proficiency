package analysis

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	pprofProfile "github.com/google/pprof/profile"
	"github.com/tuxerrante/proficiency/internal/profile"
)

// createTestProfile builds a valid pprof protobuf file with the given function
// names and sample values, returning the path to the temporary file.
func createTestProfile(t *testing.T, funcs map[string]int64, sampleType string) string {
	t.Helper()

	p := &pprofProfile.Profile{
		SampleType: []*pprofProfile.ValueType{{Type: sampleType, Unit: "count"}},
	}

	for name, value := range funcs {
		fn := &pprofProfile.Function{ID: uint64(len(p.Function) + 1), Name: name}
		p.Function = append(p.Function, fn)

		loc := &pprofProfile.Location{
			ID:   uint64(len(p.Location) + 1),
			Line: []pprofProfile.Line{{Function: fn}},
		}
		p.Location = append(p.Location, loc)

		p.Sample = append(p.Sample, &pprofProfile.Sample{
			Location: []*pprofProfile.Location{loc},
			Value:    []int64{value},
		})
	}

	path := filepath.Join(t.TempDir(), "test.pprof")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := p.Write(f); err != nil {
		t.Fatal(err)
	}

	return path
}

// createAllocProfile builds a pprof file with two sample types (alloc_objects
// and alloc_space), mimicking a real heap profile. The provided values are
// assigned to the alloc_space column (index 1).
func createAllocProfile(t *testing.T, funcs map[string]int64) string {
	t.Helper()

	p := &pprofProfile.Profile{
		SampleType: []*pprofProfile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
	}

	for name, value := range funcs {
		fn := &pprofProfile.Function{ID: uint64(len(p.Function) + 1), Name: name}
		p.Function = append(p.Function, fn)

		loc := &pprofProfile.Location{
			ID:   uint64(len(p.Location) + 1),
			Line: []pprofProfile.Line{{Function: fn}},
		}
		p.Location = append(p.Location, loc)

		// alloc_objects=0, alloc_space=value
		p.Sample = append(p.Sample, &pprofProfile.Sample{
			Location: []*pprofProfile.Location{loc},
			Value:    []int64{0, value},
		})
	}

	path := filepath.Join(t.TempDir(), "alloc.pprof")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := p.Write(f); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestParseThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "empty", input: "", want: 0},
		{name: "single cpu", input: "cpu:30", want: 1},
		{name: "multiple", input: "cpu:30,alloc:50,block:20", want: 3},
		{name: "decimal percentage", input: "cpu:12.5", want: 1},
		{name: "invalid format", input: "cpu30", wantErr: true},
		{name: "invalid percentage", input: "cpu:abc", wantErr: true},
		{name: "goroutine type", input: "goroutine:10", want: 1},
		{name: "unknown type", input: "foobar:10", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseThresholds(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d thresholds, want %d", len(got), tt.want)
			}
		})
	}
}

func TestParseThresholds_Values(t *testing.T) {
	t.Parallel()

	thresholds, err := ParseThresholds("cpu:30,alloc:50.5")
	if err != nil {
		t.Fatal(err)
	}

	if thresholds[0].Type != CPU || thresholds[0].Percentage != 30 {
		t.Errorf("threshold[0] = %+v, want cpu:30", thresholds[0])
	}
	if thresholds[1].Type != Alloc || thresholds[1].Percentage != 50.5 {
		t.Errorf("threshold[1] = %+v, want alloc:50.5", thresholds[1])
	}
}

func TestCheckThresholds_NilProfiles(t *testing.T) {
	t.Parallel()

	violations, err := CheckThresholds(nil, []Threshold{{Type: CPU, Percentage: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Errorf("expected no violations for nil profiles, got %d", len(violations))
	}
}

func TestCheckThresholds_EmptyThresholds(t *testing.T) {
	t.Parallel()

	violations, err := CheckThresholds(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if violations != nil {
		t.Errorf("expected nil violations for nil thresholds, got %v", violations)
	}
}

func TestCheckThresholds_NoMatchingProfile(t *testing.T) {
	t.Parallel()

	profiles := []*profile.CollectedProfile{
		{Type: profile.ProfileHeap, FilePath: "/nonexistent"},
	}
	thresholds := []Threshold{{Type: CPU, Percentage: 30}}

	violations, err := CheckThresholds(profiles, thresholds)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Errorf("expected no violations when profile type doesn't match, got %d", len(violations))
	}
}

func TestTopFunctions(t *testing.T) {
	t.Parallel()

	path := createTestProfile(t, map[string]int64{
		"main.hotFunc":  70,
		"main.warmFunc": 20,
		"main.coldFunc": 10,
	}, "cpu")

	stats, err := topFunctions(path, CPU)
	if err != nil {
		t.Fatalf("topFunctions returned error: %v", err)
	}

	if len(stats) != 3 {
		t.Fatalf("expected 3 functions, got %d", len(stats))
	}

	// Sort by percentage descending for deterministic assertions.
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].percentage > stats[j].percentage
	})

	wantNames := []string{"main.hotFunc", "main.warmFunc", "main.coldFunc"}
	wantPcts := []float64{70.0, 20.0, 10.0}

	for i, s := range stats {
		if s.name != wantNames[i] {
			t.Errorf("stats[%d].name = %q, want %q", i, s.name, wantNames[i])
		}
		if s.percentage != wantPcts[i] {
			t.Errorf("stats[%d].percentage = %f, want %f", i, s.percentage, wantPcts[i])
		}
	}
}

func TestTopFunctions_EmptyProfile(t *testing.T) {
	t.Parallel()

	// Profile with all zero values -> total is 0, should return nil.
	path := createTestProfile(t, map[string]int64{
		"main.zeroFunc": 0,
	}, "cpu")

	stats, err := topFunctions(path, CPU)
	if err != nil {
		t.Fatalf("topFunctions returned error: %v", err)
	}

	if stats != nil {
		t.Errorf("expected nil stats for zero-total profile, got %v", stats)
	}
}

func TestTopFunctions_AllocProfile(t *testing.T) {
	t.Parallel()

	// alloc_space is at index 1; topFunctions should pick it up via the
	// alloc_space search logic.
	path := createAllocProfile(t, map[string]int64{
		"runtime.mallocgc": 800,
		"main.allocator":   200,
	})

	stats, err := topFunctions(path, Alloc)
	if err != nil {
		t.Fatalf("topFunctions returned error: %v", err)
	}

	if len(stats) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(stats))
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].percentage > stats[j].percentage
	})

	if stats[0].name != "runtime.mallocgc" {
		t.Errorf("top function = %q, want runtime.mallocgc", stats[0].name)
	}

	if stats[0].percentage != 80.0 {
		t.Errorf("top percentage = %f, want 80.0", stats[0].percentage)
	}
}

func TestTopFunctions_BlockProfile(t *testing.T) {
	t.Parallel()

	path := createTestProfile(t, map[string]int64{
		"sync.(*Mutex).Lock": 400,
		"main.worker":        600,
	}, "contentions")

	stats, err := topFunctions(path, Block)
	if err != nil {
		t.Fatalf("topFunctions returned error: %v", err)
	}

	if len(stats) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(stats))
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].percentage > stats[j].percentage
	})

	if stats[0].name != "main.worker" {
		t.Errorf("top function = %q, want main.worker", stats[0].name)
	}

	if stats[0].percentage != 60.0 {
		t.Errorf("top percentage = %f, want 60.0", stats[0].percentage)
	}
}

func TestCheckThresholds_WithRealProfiles(t *testing.T) {
	t.Parallel()

	cpuPath := createTestProfile(t, map[string]int64{
		"main.hotFunc":  60,
		"main.warmFunc": 30,
		"main.coldFunc": 10,
	}, "cpu")

	profiles := []*profile.CollectedProfile{
		{Type: profile.ProfileCPU, FilePath: cpuPath},
	}

	// Threshold at 25% should catch hotFunc (60%) and warmFunc (30%).
	thresholds := []Threshold{{Type: CPU, Percentage: 25}}

	violations, err := CheckThresholds(profiles, thresholds)
	if err != nil {
		t.Fatalf("CheckThresholds returned error: %v", err)
	}

	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %+v", len(violations), violations)
	}

	// Sort violations by percentage descending for stable assertions.
	sort.Slice(violations, func(i, j int) bool {
		return violations[i].Percentage > violations[j].Percentage
	})

	if violations[0].Function != "main.hotFunc" {
		t.Errorf("violation[0].Function = %q, want main.hotFunc", violations[0].Function)
	}

	if violations[0].Percentage != 60.0 {
		t.Errorf("violation[0].Percentage = %f, want 60.0", violations[0].Percentage)
	}

	if violations[1].Function != "main.warmFunc" {
		t.Errorf("violation[1].Function = %q, want main.warmFunc", violations[1].Function)
	}

	if violations[0].Threshold.Type != CPU {
		t.Errorf("violation threshold type = %q, want cpu", violations[0].Threshold.Type)
	}
}

func TestCheckThresholds_NoViolations(t *testing.T) {
	t.Parallel()

	cpuPath := createTestProfile(t, map[string]int64{
		"main.hotFunc":  60,
		"main.warmFunc": 30,
		"main.coldFunc": 10,
	}, "cpu")

	profiles := []*profile.CollectedProfile{
		{Type: profile.ProfileCPU, FilePath: cpuPath},
	}

	// Threshold at 70% - no function reaches it.
	thresholds := []Threshold{{Type: CPU, Percentage: 70}}

	violations, err := CheckThresholds(profiles, thresholds)
	if err != nil {
		t.Fatalf("CheckThresholds returned error: %v", err)
	}

	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d: %+v", len(violations), violations)
	}
}

func TestCheckThresholds_AllocProfile(t *testing.T) {
	t.Parallel()

	allocPath := createAllocProfile(t, map[string]int64{
		"runtime.mallocgc": 800,
		"main.allocator":   200,
	})

	profiles := []*profile.CollectedProfile{
		{Type: profile.ProfileHeap, FilePath: allocPath},
	}

	// 15% threshold catches both (80% and 20%).
	thresholds := []Threshold{{Type: Alloc, Percentage: 15}}

	violations, err := CheckThresholds(profiles, thresholds)
	if err != nil {
		t.Fatalf("CheckThresholds returned error: %v", err)
	}

	if len(violations) != 2 {
		t.Fatalf("expected 2 violations for alloc profile, got %d: %+v", len(violations), violations)
	}
}

func TestCheckThresholds_MultipleProfileTypes(t *testing.T) {
	t.Parallel()

	cpuPath := createTestProfile(t, map[string]int64{
		"main.cpuHog": 90,
		"main.idle":   10,
	}, "cpu")

	allocPath := createAllocProfile(t, map[string]int64{
		"main.leaker": 500,
		"main.frugal": 500,
	})

	profiles := []*profile.CollectedProfile{
		{Type: profile.ProfileCPU, FilePath: cpuPath},
		{Type: profile.ProfileHeap, FilePath: allocPath},
	}

	thresholds := []Threshold{
		{Type: CPU, Percentage: 50},   // catches cpuHog (90%)
		{Type: Alloc, Percentage: 40}, // catches both (50% each)
	}

	violations, err := CheckThresholds(profiles, thresholds)
	if err != nil {
		t.Fatalf("CheckThresholds returned error: %v", err)
	}

	if len(violations) != 3 {
		t.Fatalf("expected 3 violations across CPU and alloc, got %d: %+v", len(violations), violations)
	}
}

func TestCheckThresholds_InvalidFilePath(t *testing.T) {
	t.Parallel()

	profiles := []*profile.CollectedProfile{
		{Type: profile.ProfileCPU, FilePath: "/nonexistent/profile.pb.gz"},
	}
	thresholds := []Threshold{{Type: CPU, Percentage: 10}}

	_, err := CheckThresholds(profiles, thresholds)
	if err == nil {
		t.Fatal("expected error for nonexistent profile file, got nil")
	}
}
