package proficiency

import (
	"testing"
	"time"
)

func TestParseRegressionRules(t *testing.T) {
	rules, err := ParseRegressionRules("cpu:5,latency:10:10us,error-rate:1")
	if err != nil {
		t.Fatalf("ParseRegressionRules() returned error: %v", err)
	}
	if len(rules) != 3 || rules[0].Metric != RegressionCPU {
		t.Fatalf("rules = %+v", rules)
	}

	for _, input := range []string{
		"latency",
		"latency:10",
		"throughput:10:5",
		"unknown:1",
		"cpu:-1",
		"cpu:1:1",
		"cpu:1,cpu:2",
	} {
		if _, err := ParseRegressionRules(input); err == nil {
			t.Fatalf("ParseRegressionRules(%q) unexpectedly succeeded", input)
		}
	}
}

func TestCompareReports(t *testing.T) {
	baseline := comparisonFixture("base", 100, 2, 100, 20)
	current := comparisonFixture("current", 125, 4, 80, 28)
	rules, err := ParseRegressionRules("latency:10:10us,error-rate:1,throughput:10:5rps,cpu:5")
	if err != nil {
		t.Fatal(err)
	}

	comparison, err := CompareReports(baseline, current, rules)
	if err != nil {
		t.Fatalf("CompareReports() returned error: %v", err)
	}
	if comparison.Passed {
		t.Fatal("comparison unexpectedly passed")
	}
	if len(comparison.Regressions) != 4 {
		t.Fatalf("regressions = %d, want 4: %+v", len(comparison.Regressions), comparison.Regressions)
	}
}

func TestCompareReportsWithinLimits(t *testing.T) {
	baseline := comparisonFixture("base", 100, 2, 100, 20)
	current := comparisonFixture("current", 125, 2.5, 95, 22)
	rules, err := ParseRegressionRules("latency:10:30us,error-rate:1,throughput:10:10rps,cpu:5")
	if err != nil {
		t.Fatal(err)
	}

	comparison, err := CompareReports(baseline, current, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Passed || len(comparison.Regressions) != 0 {
		t.Fatalf("comparison = %+v", comparison)
	}

	for _, metric := range comparison.Metrics {
		if metric.Metric == RegressionLatency {
			if metric.Outcome != outcomeRegressed {
				t.Fatalf("latency outcome = %q, want directional regression", metric.Outcome)
			}
			if metric.WithinLimit == nil || !*metric.WithinLimit {
				t.Fatalf("latency metric = %+v, want noise-floor pass", metric)
			}
		}
	}
}

func TestCompareReportsZeroBaselineIsVisible(t *testing.T) {
	baseline := comparisonFixture("base", 0, 0, 0, 20)
	current := comparisonFixture("current", 5, 0, 10, 20)

	comparison, err := CompareReports(baseline, current, nil)
	if err != nil {
		t.Fatal(err)
	}

	var foundLatency bool
	for _, metric := range comparison.Metrics {
		if metric.Metric == RegressionLatency {
			foundLatency = true
			if metric.Outcome != outcomeUnmeasurable {
				t.Fatalf("latency metric = %+v", metric)
			}
		}
	}
	if !foundLatency {
		t.Fatal("zero baseline latency was omitted")
	}
}

func TestCompareReportsMissingLoadStatsRemainVisible(t *testing.T) {
	snapshot := Report{
		SchemaVersion: ReportSchemaVersion,
		Timestamp:     time.Now(),
	}
	loadReport := comparisonFixture("load", 100, 2, 50, 20)

	for _, test := range []struct {
		name     string
		baseline Report
		current  Report
		outcome  string
	}{
		{name: "load added", baseline: snapshot, current: loadReport, outcome: outcomeNew},
		{name: "load removed", baseline: loadReport, current: snapshot, outcome: outcomeRemoved},
	} {
		t.Run(test.name, func(t *testing.T) {
			comparison, err := CompareReports(test.baseline, test.current, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(comparison.Metrics) != 4 {
				t.Fatalf("metrics = %+v", comparison.Metrics)
			}
			for _, metric := range comparison.Metrics {
				if metric.Outcome != test.outcome {
					t.Fatalf("metric = %+v", metric)
				}
			}
		})
	}
}

func comparisonFixture(label string, latencyMicros int64, errorRate, throughput, cpu float64) Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Timestamp:     time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC),
		ToolVersion:   "v0.2.0",
		Metadata:      Metadata{Label: label},
		LoadStats: &ReportLoad{
			ErrorRatePercent:  errorRate,
			RequestsPerSecond: throughput,
			Endpoints: []ReportEndpointStats{
				{Endpoint: "GET /pets", AvgMicros: latencyMicros},
			},
		},
		Analysis: []ProfileAnalysis{
			{
				ProfileType: "cpu",
				Functions: []FunctionStat{
					{Function: "main.hot", Percentage: cpu},
				},
			},
		},
	}
}
