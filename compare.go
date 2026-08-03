package proficiency

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RegressionMetric identifies a comparable report measurement.
type RegressionMetric string

// Supported regression metrics.
const (
	RegressionLatency    RegressionMetric = "latency"
	RegressionErrorRate  RegressionMetric = "error-rate"
	RegressionThroughput RegressionMetric = "throughput"
	RegressionCPU        RegressionMetric = "cpu"
	RegressionAlloc      RegressionMetric = "alloc"
	RegressionBlock      RegressionMetric = "block"
	RegressionGoroutine  RegressionMetric = "goroutine"
)

// Units used by regression rule noise floors.
const (
	RegressionUnitMicroseconds = "microseconds"
	RegressionUnitRPS          = "requests-per-second"
)

const (
	outcomeImproved     = "improved"
	outcomeUnchanged    = "unchanged"
	outcomeRegressed    = "regressed"
	outcomeNew          = "new"
	outcomeRemoved      = "removed"
	outcomeUnmeasurable = "unmeasurable"
	changeUnitPercent   = "percent"
	changeUnitPoints    = "percentage-points"
)

// RegressionRule defines the maximum tolerated degradation for one metric.
// Latency and throughput also require an absolute noise floor.
type RegressionRule struct {
	Metric            RegressionMetric `json:"metric"`
	Limit             float64          `json:"limit"`
	MinimumChange     float64          `json:"minimumChange,omitempty"`
	MinimumChangeUnit string           `json:"minimumChangeUnit,omitempty"`
}

// Comparison describes the deterministic difference between two reports.
type Comparison struct {
	Baseline    ReportIdentity     `json:"baseline"`
	Current     ReportIdentity     `json:"current"`
	Rules       []RegressionRule   `json:"rules"`
	Passed      bool               `json:"passed"`
	Metrics     []ComparisonMetric `json:"metrics"`
	Regressions []ComparisonMetric `json:"regressions"`
}

// ReportIdentity is the source metadata needed to identify a compared report.
type ReportIdentity struct {
	Timestamp   string   `json:"timestamp"`
	ToolVersion string   `json:"toolVersion"`
	Metadata    Metadata `json:"metadata,omitzero"`
}

// ComparisonMetric is one load or profile measurement delta. Change is
// positive for degradation and negative for improvement. Outcome is purely
// directional; WithinLimit records the configured gate result separately.
type ComparisonMetric struct {
	Metric            RegressionMetric `json:"metric"`
	Key               string           `json:"key"`
	Baseline          float64          `json:"baseline"`
	Current           float64          `json:"current"`
	Change            float64          `json:"change"`
	Unit              string           `json:"unit"`
	AbsoluteChange    float64          `json:"absoluteChange"`
	AbsoluteUnit      string           `json:"absoluteUnit"`
	Outcome           string           `json:"outcome"`
	Limit             *float64         `json:"limit,omitempty"`
	MinimumChange     *float64         `json:"minimumChange,omitempty"`
	MinimumChangeUnit string           `json:"minimumChangeUnit,omitempty"`
	WithinLimit       *bool            `json:"withinLimit,omitempty"`
}

// ParseRegressionRules parses comma-separated rules. Latency and throughput
// require absolute noise floors:
//
//	latency:10:200us,throughput:10:5rps,error-rate:1,cpu:5
func ParseRegressionRules(value string) ([]RegressionRule, error) {
	if strings.TrimSpace(value) == "" {
		return []RegressionRule{}, nil
	}

	seen := make(map[RegressionMetric]struct{})
	rules := make([]RegressionRule, 0)
	for part := range strings.SplitSeq(value, ",") {
		fields := strings.Split(strings.TrimSpace(part), ":")
		if len(fields) < 2 || len(fields) > 3 {
			return nil, fmt.Errorf("invalid regression rule %q", part)
		}

		metric := RegressionMetric(strings.TrimSpace(fields[0]))
		if !supportedRegressionMetric(metric) {
			return nil, fmt.Errorf("unknown regression metric %q, supported: latency, error-rate, throughput, cpu, alloc, block, goroutine", metric)
		}
		if _, duplicate := seen[metric]; duplicate {
			return nil, fmt.Errorf("duplicate regression metric %q", metric)
		}

		limit, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid limit %q for %s: %w", fields[1], metric, err)
		}

		rule := RegressionRule{Metric: metric, Limit: limit}
		switch metric {
		case RegressionLatency:
			if len(fields) != 3 {
				return nil, errorsForRequiredFloor(metric, "for example latency:10:200us")
			}
			floor, parseErr := time.ParseDuration(strings.TrimSpace(fields[2]))
			if parseErr != nil {
				return nil, fmt.Errorf("invalid latency noise floor %q: %w", fields[2], parseErr)
			}
			rule.MinimumChange = float64(floor.Microseconds())
			rule.MinimumChangeUnit = RegressionUnitMicroseconds
		case RegressionThroughput:
			if len(fields) != 3 {
				return nil, errorsForRequiredFloor(metric, "for example throughput:10:5rps")
			}
			floorValue := strings.TrimSuffix(strings.TrimSpace(fields[2]), "rps")
			if floorValue == strings.TrimSpace(fields[2]) {
				return nil, fmt.Errorf("throughput noise floor %q must end with rps", fields[2])
			}
			rule.MinimumChange, err = strconv.ParseFloat(floorValue, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid throughput noise floor %q: %w", fields[2], err)
			}
			rule.MinimumChangeUnit = RegressionUnitRPS
		case RegressionErrorRate, RegressionCPU, RegressionAlloc, RegressionBlock, RegressionGoroutine:
			if len(fields) != 2 {
				return nil, fmt.Errorf("%s rules do not accept an absolute noise floor", metric)
			}
		}
		if err := validateRegressionRule(rule); err != nil {
			return nil, err
		}

		seen[metric] = struct{}{}
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Metric < rules[j].Metric
	})
	return rules, nil
}

// CompareReports compares stable aggregate load metrics and recorded profile
// bottlenecks. It does not require the raw pprof files to remain available.
func CompareReports(baseline, current Report, rules []RegressionRule) (Comparison, error) {
	if baseline.SchemaVersion != ReportSchemaVersion {
		return Comparison{}, fmt.Errorf("unsupported baseline schema version %q", baseline.SchemaVersion)
	}
	if current.SchemaVersion != ReportSchemaVersion {
		return Comparison{}, fmt.Errorf("unsupported current schema version %q", current.SchemaVersion)
	}

	rulesByMetric := make(map[RegressionMetric]RegressionRule, len(rules))
	for _, rule := range rules {
		if err := validateRegressionRule(rule); err != nil {
			return Comparison{}, err
		}
		if _, duplicate := rulesByMetric[rule.Metric]; duplicate {
			return Comparison{}, fmt.Errorf("duplicate regression metric %q", rule.Metric)
		}
		rulesByMetric[rule.Metric] = rule
	}

	result := Comparison{
		Baseline:    reportIdentity(baseline),
		Current:     reportIdentity(current),
		Rules:       make([]RegressionRule, len(rules)),
		Passed:      true,
		Metrics:     []ComparisonMetric{},
		Regressions: []ComparisonMetric{},
	}
	copy(result.Rules, rules)
	result.Metrics = append(result.Metrics, compareLoad(baseline.LoadStats, current.LoadStats, rulesByMetric)...)
	result.Metrics = append(result.Metrics, compareFunctions(baseline.Analysis, current.Analysis, rulesByMetric)...)
	sort.Slice(result.Metrics, func(i, j int) bool {
		if result.Metrics[i].Metric == result.Metrics[j].Metric {
			return result.Metrics[i].Key < result.Metrics[j].Key
		}
		return result.Metrics[i].Metric < result.Metrics[j].Metric
	})

	for _, metric := range result.Metrics {
		if metric.WithinLimit != nil && !*metric.WithinLimit {
			result.Regressions = append(result.Regressions, metric)
		}
	}
	result.Passed = len(result.Regressions) == 0
	return result, nil
}

func compareLoad(
	baseline *ReportLoad,
	current *ReportLoad,
	rules map[RegressionMetric]RegressionRule,
) []ComparisonMetric {
	if baseline == nil || current == nil {
		return nil
	}

	errorRateChange := current.ErrorRatePercent - baseline.ErrorRatePercent
	result := []ComparisonMetric{
		buildDelta(
			RegressionErrorRate,
			"overall",
			baseline.ErrorRatePercent,
			current.ErrorRatePercent,
			errorRateChange,
			changeUnitPoints,
			errorRateChange,
			changeUnitPoints,
			rules,
		),
	}

	switch {
	case baseline.RequestsPerSecond > 0:
		absoluteChange := baseline.RequestsPerSecond - current.RequestsPerSecond
		relativeChange := absoluteChange / baseline.RequestsPerSecond * 100
		result = append(result, buildDelta(
			RegressionThroughput,
			"overall",
			baseline.RequestsPerSecond,
			current.RequestsPerSecond,
			relativeChange,
			changeUnitPercent,
			absoluteChange,
			RegressionUnitRPS,
			rules,
		))
	case current.RequestsPerSecond != 0:
		result = append(result, unmeasurableMetric(
			RegressionThroughput,
			"overall",
			baseline.RequestsPerSecond,
			current.RequestsPerSecond,
			changeUnitPercent,
			RegressionUnitRPS,
		))
	}

	baselineEndpoints := endpointMap(baseline.Endpoints)
	currentEndpoints := endpointMap(current.Endpoints)
	for _, key := range sortedUnion(baselineEndpoints, currentEndpoints) {
		baselineEndpoint, baselineOK := baselineEndpoints[key]
		currentEndpoint, currentOK := currentEndpoints[key]
		switch {
		case !baselineOK:
			result = append(result, incomparableMetric(
				RegressionLatency,
				key,
				0,
				float64(currentEndpoint.AvgMicros),
				changeUnitPercent,
				RegressionUnitMicroseconds,
				outcomeNew,
			))
		case !currentOK:
			result = append(result, incomparableMetric(
				RegressionLatency,
				key,
				float64(baselineEndpoint.AvgMicros),
				0,
				changeUnitPercent,
				RegressionUnitMicroseconds,
				outcomeRemoved,
			))
		case baselineEndpoint.AvgMicros > 0:
			absoluteChange := float64(currentEndpoint.AvgMicros - baselineEndpoint.AvgMicros)
			relativeChange := absoluteChange / float64(baselineEndpoint.AvgMicros) * 100
			result = append(result, buildDelta(
				RegressionLatency,
				key,
				float64(baselineEndpoint.AvgMicros),
				float64(currentEndpoint.AvgMicros),
				relativeChange,
				changeUnitPercent,
				absoluteChange,
				RegressionUnitMicroseconds,
				rules,
			))
		default:
			result = append(result, unmeasurableMetric(
				RegressionLatency,
				key,
				float64(baselineEndpoint.AvgMicros),
				float64(currentEndpoint.AvgMicros),
				changeUnitPercent,
				RegressionUnitMicroseconds,
			))
		}
	}
	return result
}

func compareFunctions(
	baseline []ProfileAnalysis,
	current []ProfileAnalysis,
	rules map[RegressionMetric]RegressionRule,
) []ComparisonMetric {
	baselineFunctions := functionMap(baseline)
	currentFunctions := functionMap(current)
	result := make([]ComparisonMetric, 0)

	for _, key := range sortedUnion(baselineFunctions, currentFunctions) {
		metric, function, _ := strings.Cut(key, "\x00")
		regressionMetric := RegressionMetric(metric)
		baselineValue, baselineOK := baselineFunctions[key]
		currentValue, currentOK := currentFunctions[key]
		switch {
		case !baselineOK:
			result = append(result, incomparableMetric(
				regressionMetric,
				function,
				0,
				currentValue,
				changeUnitPoints,
				changeUnitPoints,
				outcomeNew,
			))
		case !currentOK:
			result = append(result, incomparableMetric(
				regressionMetric,
				function,
				baselineValue,
				0,
				changeUnitPoints,
				changeUnitPoints,
				outcomeRemoved,
			))
		default:
			change := currentValue - baselineValue
			result = append(result, buildDelta(
				regressionMetric,
				function,
				baselineValue,
				currentValue,
				change,
				changeUnitPoints,
				change,
				changeUnitPoints,
				rules,
			))
		}
	}
	return result
}

func buildDelta(
	metric RegressionMetric,
	key string,
	baseline float64,
	current float64,
	change float64,
	unit string,
	absoluteChange float64,
	absoluteUnit string,
	rules map[RegressionMetric]RegressionRule,
) ComparisonMetric {
	result := ComparisonMetric{
		Metric:         metric,
		Key:            key,
		Baseline:       baseline,
		Current:        current,
		Change:         change,
		Unit:           unit,
		AbsoluteChange: absoluteChange,
		AbsoluteUnit:   absoluteUnit,
		Outcome:        directionalOutcome(change),
	}

	if rule, ok := rules[metric]; ok {
		limit := rule.Limit
		withinLimit := change <= limit || absoluteChange <= rule.MinimumChange
		result.Limit = &limit
		result.WithinLimit = &withinLimit
		if rule.MinimumChange > 0 {
			minimumChange := rule.MinimumChange
			result.MinimumChange = &minimumChange
			result.MinimumChangeUnit = rule.MinimumChangeUnit
		}
	}
	return result
}

func incomparableMetric(
	metric RegressionMetric,
	key string,
	baseline float64,
	current float64,
	unit string,
	absoluteUnit string,
	outcome string,
) ComparisonMetric {
	return ComparisonMetric{
		Metric:       metric,
		Key:          key,
		Baseline:     baseline,
		Current:      current,
		Unit:         unit,
		AbsoluteUnit: absoluteUnit,
		Outcome:      outcome,
	}
}

func unmeasurableMetric(
	metric RegressionMetric,
	key string,
	baseline float64,
	current float64,
	unit string,
	absoluteUnit string,
) ComparisonMetric {
	return incomparableMetric(
		metric,
		key,
		baseline,
		current,
		unit,
		absoluteUnit,
		outcomeUnmeasurable,
	)
}

func directionalOutcome(change float64) string {
	switch {
	case change < 0:
		return outcomeImproved
	case change > 0:
		return outcomeRegressed
	default:
		return outcomeUnchanged
	}
}

func validateRegressionRule(rule RegressionRule) error {
	if !supportedRegressionMetric(rule.Metric) {
		return fmt.Errorf("unsupported regression metric %q", rule.Metric)
	}
	if rule.Limit < 0 {
		return fmt.Errorf("limit for %s cannot be negative", rule.Metric)
	}

	switch rule.Metric {
	case RegressionLatency:
		if rule.MinimumChange <= 0 || rule.MinimumChangeUnit != RegressionUnitMicroseconds {
			return errorsForRequiredFloor(rule.Metric, "use a positive microsecond floor")
		}
	case RegressionThroughput:
		if rule.MinimumChange <= 0 || rule.MinimumChangeUnit != RegressionUnitRPS {
			return errorsForRequiredFloor(rule.Metric, "use a positive requests-per-second floor")
		}
	case RegressionErrorRate, RegressionCPU, RegressionAlloc, RegressionBlock, RegressionGoroutine:
		if rule.MinimumChange != 0 || rule.MinimumChangeUnit != "" {
			return fmt.Errorf("%s rules do not accept an absolute noise floor", rule.Metric)
		}
	}
	return nil
}

func errorsForRequiredFloor(metric RegressionMetric, detail string) error {
	return fmt.Errorf("%s regression rules require an absolute noise floor: %s", metric, detail)
}

func endpointMap(endpoints []ReportEndpointStats) map[string]ReportEndpointStats {
	result := make(map[string]ReportEndpointStats, len(endpoints))
	for _, endpoint := range endpoints {
		result[endpoint.Endpoint] = endpoint
	}
	return result
}

func functionMap(items []ProfileAnalysis) map[string]float64 {
	result := make(map[string]float64)
	for _, item := range items {
		if !supportedRegressionMetric(RegressionMetric(item.ProfileType)) {
			continue
		}
		for _, function := range item.Functions {
			result[item.ProfileType+"\x00"+function.Function] = function.Percentage
		}
	}
	return result
}

func sortedUnion[T any](left, right map[string]T) []string {
	keys := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func reportIdentity(report Report) ReportIdentity {
	return ReportIdentity{
		Timestamp:   report.Timestamp.UTC().Format(time.RFC3339Nano),
		ToolVersion: report.ToolVersion,
		Metadata:    report.Metadata,
	}
}

func supportedRegressionMetric(metric RegressionMetric) bool {
	switch metric {
	case RegressionLatency,
		RegressionErrorRate,
		RegressionThroughput,
		RegressionCPU,
		RegressionAlloc,
		RegressionBlock,
		RegressionGoroutine:
		return true
	default:
		return false
	}
}
