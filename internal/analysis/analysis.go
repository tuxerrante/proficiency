package analysis

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	pprofProfile "github.com/google/pprof/profile"
	"github.com/tuxerrante/proficiency/internal/profile"
)

// ProfileType maps to pprof profile types for threshold evaluation.
type ProfileType string

const (
	CPU       ProfileType = "cpu"
	Alloc     ProfileType = "alloc"
	Block     ProfileType = "block"
	Goroutine ProfileType = "goroutine"
)

// Threshold defines a pass/fail gate for a profile type.
type Threshold struct {
	Type       ProfileType
	Percentage float64
}

// Violation records a function that exceeded its threshold.
type Violation struct {
	Function   string
	Percentage float64
	Threshold  Threshold
}

var profileTypeToCollectorType = map[ProfileType]profile.Type{
	CPU:       profile.ProfileCPU,
	Alloc:     profile.ProfileHeap,
	Block:     profile.ProfileBlock,
	Goroutine: profile.ProfileGoroutine,
}

// ParseThresholds parses a comma-separated threshold string like "cpu:30,alloc:50".
func ParseThresholds(s string) ([]Threshold, error) {
	if s == "" {
		return nil, nil
	}

	var thresholds []Threshold
	for part := range strings.SplitSeq(s, ",") {
		typ, pctStr, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("invalid threshold format %q, expected type:percentage", part)
		}

		pct, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid percentage %q in threshold %q: %w", pctStr, part, err)
		}

		pt := ProfileType(typ)
		switch pt {
		case CPU, Alloc, Block, Goroutine:
		default:
			return nil, fmt.Errorf("unknown profile type %q, supported: cpu, alloc, block, goroutine", typ)
		}

		thresholds = append(thresholds, Threshold{Type: pt, Percentage: pct})
	}

	return thresholds, nil
}

// CheckThresholds evaluates collected profiles against thresholds.
// Returns violations for functions exceeding their threshold.
func CheckThresholds(profiles []*profile.CollectedProfile, thresholds []Threshold) ([]Violation, error) {
	if len(thresholds) == 0 {
		return nil, nil
	}

	var violations []Violation

	for _, thresh := range thresholds {
		collectorType, ok := profileTypeToCollectorType[thresh.Type]
		if !ok {
			continue
		}

		var matched *profile.CollectedProfile
		for _, p := range profiles {
			if p.Type == collectorType {
				matched = p
				break
			}
		}

		if matched == nil {
			continue
		}

		funcs, err := topFunctions(matched.FilePath, thresh.Type)
		if err != nil {
			return nil, fmt.Errorf("analyzing %s profile: %w", thresh.Type, err)
		}

		for _, f := range funcs {
			if f.percentage > thresh.Percentage {
				violations = append(violations, Violation{
					Function:   f.name,
					Percentage: f.percentage,
					Threshold:  thresh,
				})
			}
		}
	}

	return violations, nil
}

type funcStat struct {
	name       string
	percentage float64
}

func topFunctions(path string, pt ProfileType) ([]funcStat, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from our own CollectedProfile, not user input
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	prof, err := pprofProfile.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parsing profile %s: %w", path, err)
	}

	valueIdx := 0
	if pt == Alloc && len(prof.SampleType) > 1 {
		for i, st := range prof.SampleType {
			if st.Type == "alloc_space" {
				valueIdx = i
				break
			}
		}
	}

	var total int64
	for _, s := range prof.Sample {
		if valueIdx < len(s.Value) {
			total += s.Value[valueIdx]
		}
	}

	if total == 0 {
		return nil, nil
	}

	flatByFunc := make(map[string]int64)
	for _, s := range prof.Sample {
		if len(s.Location) > 0 && valueIdx < len(s.Value) {
			loc := s.Location[0]
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				name := loc.Line[0].Function.Name
				flatByFunc[name] += s.Value[valueIdx]
			}
		}
	}

	var stats []funcStat
	for name, flat := range flatByFunc {
		pct := float64(flat) / float64(total) * 100
		stats = append(stats, funcStat{name: name, percentage: pct})
	}

	return stats, nil
}
