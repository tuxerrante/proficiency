package analysis

import (
	"testing"

	"github.com/tuxerrante/proficiency/internal/profile"
)

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
		{name: "unknown type", input: "goroutine:10", wantErr: true},
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
