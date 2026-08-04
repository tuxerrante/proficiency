package analysis

import (
	"fmt"
	"os"
	"slices"
	"sort"
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

// FunctionStat describes one function's flat contribution to a profile.
type FunctionStat struct {
	Function   string
	Percentage float64
}

// ProfileAnalysis contains the ranked functions for one collected profile.
type ProfileAnalysis struct {
	Type      ProfileType
	Functions []FunctionStat
}

var profileTypeToCollectorType = map[ProfileType]profile.Type{
	CPU:       profile.ProfileCPU,
	Alloc:     profile.ProfileHeap,
	Block:     profile.ProfileBlock,
	Goroutine: profile.ProfileGoroutine,
}

var collectorTypeToProfileType = map[profile.Type]ProfileType{
	profile.ProfileCPU:       CPU,
	profile.ProfileHeap:      Alloc,
	profile.ProfileBlock:     Block,
	profile.ProfileGoroutine: Goroutine,
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

		var matched []*profile.CollectedProfile
		for _, p := range profiles {
			if p.Type == collectorType {
				matched = append(matched, p)
			}
		}

		if len(matched) == 0 {
			continue
		}

		funcs, err := aggregateFunctions(matched, thresh.Type)
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

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Threshold.Type == violations[j].Threshold.Type {
			return violations[i].Function < violations[j].Function
		}
		return violations[i].Threshold.Type < violations[j].Threshold.Type
	})
	return violations, nil
}

// AnalyzeProfiles returns deterministic, ranked function summaries for the
// collected profiles. A limit of zero disables analysis.
func AnalyzeProfiles(profiles []*profile.CollectedProfile, limit int) ([]ProfileAnalysis, error) {
	if limit <= 0 {
		return []ProfileAnalysis{}, nil
	}

	grouped := make(map[ProfileType][]*profile.CollectedProfile)
	for _, collected := range profiles {
		profileType, ok := collectorTypeToProfileType[collected.Type]
		if !ok {
			continue
		}
		grouped[profileType] = append(grouped[profileType], collected)
	}

	profileTypes := make([]ProfileType, 0, len(grouped))
	for profileType := range grouped {
		profileTypes = append(profileTypes, profileType)
	}
	slices.Sort(profileTypes)

	result := make([]ProfileAnalysis, 0, len(profileTypes))
	for _, profileType := range profileTypes {
		stats, err := aggregateFunctions(grouped[profileType], profileType)
		if err != nil {
			return nil, fmt.Errorf("analyzing %s profile: %w", profileType, err)
		}

		sort.Slice(stats, func(i, j int) bool {
			if stats[i].percentage == stats[j].percentage {
				return stats[i].name < stats[j].name
			}
			return stats[i].percentage > stats[j].percentage
		})
		if len(stats) > limit {
			stats = stats[:limit]
		}

		functions := make([]FunctionStat, 0, len(stats))
		for _, stat := range stats {
			functions = append(functions, FunctionStat{
				Function:   stat.name,
				Percentage: stat.percentage,
			})
		}
		result = append(result, ProfileAnalysis{
			Type:      profileType,
			Functions: functions,
		})
	}

	return result, nil
}

type funcStat struct {
	name       string
	flat       int64
	percentage float64
}

func topFunctions(path string, pt ProfileType) ([]funcStat, error) {
	stats, _, err := functionValues(path, pt)
	return stats, err
}

func aggregateFunctions(profiles []*profile.CollectedProfile, profileType ProfileType) ([]funcStat, error) {
	var total int64
	flatByFunc := make(map[string]int64)
	for _, collected := range profiles {
		stats, profileTotal, err := functionValues(collected.FilePath, profileType)
		if err != nil {
			return nil, err
		}
		total += profileTotal
		for _, stat := range stats {
			flatByFunc[stat.name] += stat.flat
		}
	}
	if total == 0 {
		return nil, nil
	}

	result := make([]funcStat, 0, len(flatByFunc))
	for name, flat := range flatByFunc {
		result = append(result, funcStat{
			name:       name,
			flat:       flat,
			percentage: float64(flat) / float64(total) * 100,
		})
	}
	return result, nil
}

func functionValues(path string, pt ProfileType) ([]funcStat, int64, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from our own CollectedProfile, not user input
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	prof, err := pprofProfile.Parse(f)
	if err != nil {
		return nil, 0, fmt.Errorf("parsing profile %s: %w", path, err)
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
		return nil, 0, nil
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
		stats = append(stats, funcStat{name: name, flat: flat, percentage: pct})
	}

	return stats, total, nil
}
