# --fail-on Flag Implementation Plan

**Goal:** Add `--fail-on` flag that parses collected pprof profiles, evaluates per-function
sample percentages against thresholds, and exits non-zero when exceeded.

**Architecture:** New `internal/analysis` package using `github.com/google/pprof/profile`
for protobuf parsing. Wired into `cmd/proficiency/main.go` after profile collection.

**Tech Stack:** Go, github.com/google/pprof/profile

---

## Task 1: Add google/pprof dependency

**Files:**

- Modify: `go.mod`

- [ ] **Step 1: Add the dependency**

```bash
cd /Users/alessandroaffinito/dev/proficiency
go get github.com/google/pprof@latest
```

- [ ] **Step 2: Tidy**

```bash
go mod tidy
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add github.com/google/pprof for profile analysis"
```

---

## Task 2: Create internal/analysis package with types and threshold parsing

**Files:**

- Create: `internal/analysis/analysis.go`

- [ ] **Step 1: Write types and ParseThresholds function**

```go
package analysis

import (
    "fmt"
    "strconv"
    "strings"
)

// ProfileType maps to pprof profile types.
type ProfileType string

const (
    CPU   ProfileType = "cpu"
    Alloc ProfileType = "alloc"
    Block ProfileType = "block"
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

// ParseThresholds parses a comma-separated threshold string like "cpu:30,alloc:50".
func ParseThresholds(s string) ([]Threshold, error) {
    if s == "" {
        return nil, nil
    }

    var thresholds []Threshold
    for _, part := range strings.Split(s, ",") {
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
        case CPU, Alloc, Block:
        default:
            return nil, fmt.Errorf("unknown profile type %q, supported: cpu, alloc, block", typ)
        }

        thresholds = append(thresholds, Threshold{Type: pt, Percentage: pct})
    }

    return thresholds, nil
}
```

- [ ] **Step 2: Write tests for ParseThresholds**

Create `internal/analysis/analysis_test.go`:

```go
package analysis

import (
    "testing"
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
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/analysis/ -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/analysis/
git commit -m "feat: add analysis package with threshold parsing"
```

---

## Task 3: Add CheckThresholds function that reads profiles and evaluates

**Files:**

- Modify: `internal/analysis/analysis.go`

- [ ] **Step 1: Add CheckThresholds and helper functions**

Add to `analysis.go`:

```go
import (
    "os"

    pprofProfile "github.com/google/pprof/profile"
    "github.com/tuxerrante/proficiency/internal/profile"
)

// profileTypeToCollectorType maps analysis types to collector types.
var profileTypeToCollectorType = map[ProfileType]profile.Type{
    CPU:   profile.ProfileCPU,
    Alloc: profile.ProfileHeap,
    Block: profile.ProfileBlock,
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

        // Find the matching profile
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
            if f.Percentage > thresh.Percentage {
                violations = append(violations, Violation{
                    Function:   f.Name,
                    Percentage: f.Percentage,
                    Threshold:  thresh,
                })
            }
        }
    }

    return violations, nil
}

type funcStat struct {
    Name       string
    Percentage float64
}

// topFunctions reads a pprof file and returns per-function flat percentages.
func topFunctions(path string, pt ProfileType) ([]funcStat, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    prof, err := pprofProfile.Parse(f)
    if err != nil {
        return nil, fmt.Errorf("parsing profile %s: %w", path, err)
    }

    // Select the right sample value index based on profile type
    valueIdx := 0
    if pt == Alloc && len(prof.SampleType) > 1 {
        // Heap profiles have [alloc_objects, alloc_space, inuse_objects, inuse_space]
        // We want alloc_space (index 1)
        for i, st := range prof.SampleType {
            if st.Type == "alloc_space" {
                valueIdx = i
                break
            }
        }
    }

    // Sum total value across all samples
    var total int64
    for _, s := range prof.Sample {
        if valueIdx < len(s.Value) {
            total += s.Value[valueIdx]
        }
    }

    if total == 0 {
        return nil, nil
    }

    // Aggregate flat values per function
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
        stats = append(stats, funcStat{Name: name, Percentage: pct})
    }

    return stats, nil
}
```

- [ ] **Step 2: Add test with a real pprof fixture**

Generate a test fixture and add tests. Create `internal/analysis/testdata/` with
a small CPU profile generated from `go test -cpuprofile`.

Add to `analysis_test.go`:

```go
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
```

- [ ] **Step 3: Run tests and lint**

```bash
go test ./internal/analysis/ -v -race
golangci-lint run ./internal/analysis/
```

- [ ] **Step 4: Commit**

```bash
git add internal/analysis/
git commit -m "feat: add CheckThresholds for evaluating profiles against limits"
```

---

## Task 4: Wire --fail-on flag into main.go

**Files:**

- Modify: `cmd/proficiency/main.go`

- [ ] **Step 1: Add FailOn field to Config struct**

```go
type Config struct {
    // ... existing fields ...
    FailOn string // Threshold string, e.g. "cpu:30,alloc:50"
}
```

- [ ] **Step 2: Add flag definition in parseFlags()**

```go
fs.StringVar(&cfg.FailOn, "fail-on", "",
    "Comma-separated thresholds for CI gating (e.g. cpu:30,alloc:50). "+
        "Exit 1 if any function exceeds the threshold percentage. "+
        "Supported types: cpu, alloc, block.")
```

- [ ] **Step 3: Parse thresholds in run() and validate early**

After config validation, before profile collection:

```go
thresholds, err := analysis.ParseThresholds(cfg.FailOn)
if err != nil {
    return fmt.Errorf("invalid --fail-on value: %w", err)
}
```

- [ ] **Step 4: Evaluate thresholds after profile collection**

After the profile summary section (around line 320), before return:

```go
if len(thresholds) > 0 {
    violations, err := analysis.CheckThresholds(profiles, thresholds)
    if err != nil {
        return fmt.Errorf("threshold analysis failed: %w", err)
    }
    if len(violations) > 0 {
        fmt.Fprintf(os.Stderr, "\nFAIL: performance thresholds exceeded\n")
        for _, v := range violations {
            fmt.Fprintf(os.Stderr, "  %-40s %5.1f%%  (threshold: %.0f%%)\n",
                v.Function, v.Percentage, v.Threshold.Percentage)
        }
        return fmt.Errorf("%d threshold violation(s) detected", len(violations))
    }
    fmt.Println("\nPASS: all thresholds within limits")
}
```

- [ ] **Step 5: Add analysis import**

```go
import (
    // ... existing imports ...
    "github.com/tuxerrante/proficiency/internal/analysis"
)
```

- [ ] **Step 6: Build and test**

```bash
go build ./cmd/proficiency/
./proficiency --help 2>&1 | grep fail-on
```

Expected: `--fail-on` appears in help output.

- [ ] **Step 7: Run full test suite**

```bash
make test
```

- [ ] **Step 8: Commit**

```bash
git add cmd/proficiency/main.go
git commit -m "feat: wire --fail-on flag for CI threshold gating"
```

---

## Task 5: Full verification

- [ ] **Step 1: Run full pipeline**

```bash
make clean && make lint && make test && make build
```

- [ ] **Step 2: Test with the rh-perf E2E scenario**

```bash
# Start rh-perf's subtle-api in another terminal
cd /Users/alessandroaffinito/dev/rh-perf
go run ./stages/02-automated-profiling/cmd/subtle-api/ &
sleep 2

# Run proficiency with --fail-on
cd /Users/alessandroaffinito/dev/proficiency
./proficiency \
    --openapi /Users/alessandroaffinito/dev/rh-perf/stages/02-automated-profiling/cmd/subtle-api/openapi.yaml \
    --target http://localhost:8080 \
    --pprof-target http://localhost:6060 \
    --duration 10s --concurrency 5 --rps 50 \
    --fail-on=cpu:20
echo "Exit code: $?"

kill %1
```

Expected: exit code 1 with violation output showing the hot function.

- [ ] **Step 3: Test without --fail-on (backward compatible)**

```bash
./proficiency \
    --openapi /Users/alessandroaffinito/dev/rh-perf/stages/02-automated-profiling/cmd/subtle-api/openapi.yaml \
    --target http://localhost:8080 \
    --pprof-target http://localhost:6060 \
    --duration 5s --concurrency 2 --rps 20
echo "Exit code: $?"
```

Expected: exit code 0, no threshold analysis output.
