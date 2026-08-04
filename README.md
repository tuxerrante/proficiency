# Proficiency

[![CI](https://github.com/tuxerrante/proficiency/actions/workflows/ci.yml/badge.svg)](https://github.com/tuxerrante/proficiency/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/tuxerrante/c40d872af91b3f8cae7757a85dc2f581/raw/coverage.json)](https://github.com/tuxerrante/proficiency)
[![Go Reference](https://pkg.go.dev/badge/github.com/tuxerrante/proficiency.svg)](https://pkg.go.dev/github.com/tuxerrante/proficiency)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13076/badge)](https://www.bestpractices.dev/projects/13076)

**Catch Go API performance regressions before they merge.**

Proficiency reads your OpenAPI document, generates controlled load, collects
native Go pprof profiles, and writes a versioned report that can be compared
between commits.

## GitHub Action quick start

Your API needs an OpenAPI document and `/debug/pprof/` enabled:

```go
import _ "net/http/pprof"
```

Then add one profiling step after starting the service:

```yaml
- name: Profile API
  id: proficiency
  uses: tuxerrante/proficiency@v0
  with:
    openapi-path: api/openapi.yaml
    target-url: http://localhost:8080
    duration: 10s

- name: Upload profiling evidence
  if: always()
  uses: actions/upload-artifact@v4
  with:
    name: proficiency-report
    path: |
      ${{ steps.proficiency.outputs.report-path }}
      ${{ steps.proficiency.outputs.output-dir }}/*.pprof
```

This produces:

- a stable JSON report for artifacts and automation
- CPU, heap, and block profiles for `go tool pprof`
- a non-zero exit when configured performance gates fail

## CLI

Install the latest release:

```bash
go install github.com/tuxerrante/proficiency/cmd/proficiency@latest
```

Profile a service:

```bash
proficiency \
  --openapi ./api/openapi.yaml \
  --target http://localhost:8080 \
  --duration 10s \
  --concurrency 5 \
  --rps 50 \
  --report ./profiles/report.json \
  --label baseline
```

Proficiency saves the requested pprof files and records:

- run configuration and source revision metadata
- request counts, error rate, throughput, and per-endpoint latency
- the highest flat-cost functions in each collected profile
- profile threshold violations
- an optional comparison with a previous report

### Compare a pull request with a baseline

Store a successful main-branch report as an artifact, download it in a pull
request job, and pass it back to Proficiency:

```bash
proficiency \
  --openapi ./api/openapi.yaml \
  --target http://localhost:8080 \
  --report ./profiles/pr.json \
  --baseline ./baseline/main.json \
  --fail-on-regression 'latency:10:200us,error-rate:1,throughput:10:5rps,cpu:5,alloc:5' \
  --label pull-request
```

Regression rules use:

| Metric       | Change measured                                   |
| ------------ | ------------------------------------------------- |
| `latency`    | Relative increase plus absolute microsecond floor |
| `error-rate` | Increase in overall error-rate percentage points  |
| `throughput` | Relative decrease plus absolute RPS floor         |
| `cpu`        | Increase in function flat-share percentage points |
| `alloc`      | Increase in function flat-share percentage points |
| `block`      | Increase in function flat-share percentage points |
| `goroutine`  | Increase in function flat-share percentage points |

Latency and throughput rules require an absolute noise floor. A latency rule
such as `latency:10:200us` fails only when latency increases by more than both
10% and 200 microseconds. `throughput:10:5rps` similarly requires both a 10%
drop and more than 5 requests per second of absolute loss.

The report is written before Proficiency exits non-zero for a failed threshold
or regression gate, so CI can always upload the evidence.

See [the report schema contract](docs/report-schema.md) for field and
compatibility details.

## Go package

The root module is importable. Start from `DefaultConfig`, then call `Run`:

```go
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"github.com/tuxerrante/proficiency"
)

func main() {
	cfg := proficiency.DefaultConfig()
	cfg.OpenAPIPath = "./api/openapi.yaml"
	cfg.TargetURL = "http://localhost:8080"
	cfg.Duration = 10 * time.Second
	cfg.ReportPath = "./profiles/report.json"
	cfg.Output = os.Stdout
	cfg.ErrorOutput = os.Stderr

	report, err := proficiency.Run(context.Background(), cfg)
	var gateErr *proficiency.GateError
	if err != nil && !errors.As(err, &gateErr) {
		log.Fatal(err)
	}

	log.Printf("report schema=%s profiles=%d", report.SchemaVersion, len(report.Profiles))
	if gateErr != nil {
		os.Exit(3)
	}
}
```

`ReadReport`, `WriteReport`, `ParseRegressionRules`, and `CompareReports` are
also exported for workflows that compare stored artifacts without running a
new profile.

## GitHub Action with regression gates

The action is composite rather than container-based so it can reach a service
bound to the runner's `localhost`. Released action versions download a
checksum-verified binary.

```yaml
name: profile

on:
  pull_request:

jobs:
  proficiency:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod

      - name: Start API
        run: |
          go run ./cmd/api &
          for attempt in $(seq 1 30); do
            curl --fail --silent http://localhost:8080/health && break
            sleep 1
          done

      - name: Profile API
        id: proficiency
        uses: tuxerrante/proficiency@v0
        with:
          openapi-path: api/openapi.yaml
          target-url: http://localhost:8080
          duration: 10s
          report-path: profiles/report.json
          baseline-report: baseline/main.json
          fail-on-regression: latency:10:200us,error-rate:1,throughput:10:5rps,cpu:5
          label: pull-request

      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: proficiency-report
          path: |
            ${{ steps.proficiency.outputs.report-path }}
            ${{ steps.proficiency.outputs.output-dir }}/*.pprof
```

Use `@v0` for automatic compatible updates. For maximum supply-chain
hardening, pin the action to the full commit SHA corresponding to a release.

## Container image

Build and run the standalone image when the target is reachable through the
selected Docker networking mode. This host-network example is Linux-specific:

```bash
docker build --build-arg VERSION=dev -t proficiency:dev .
docker run --rm \
  --network host \
  --user "$(id -u):$(id -g)" \
  -v "$PWD:/work" \
  proficiency:dev \
  --openapi /work/api/openapi.yaml \
  --target http://localhost:8080 \
  --report /work/profiles/report.json
```

The GitHub Action intentionally does not use this image because hosted Actions
runners do not provide a portable host-network contract for Docker actions.

## Other modes

Collect profiles without generating load:

```bash
proficiency \
  --target http://localhost:8080 \
  --skip-load \
  --profile-types heap,goroutine \
  --report ./profiles/snapshot.json
```

Collect a time series:

```bash
proficiency \
  --target http://localhost:8080 \
  --skip-load \
  --sample-interval 2s \
  --sample-count 10 \
  --profile-types heap,goroutine \
  --report ./profiles/watch.json
```

## Development

```bash
make test           # format, lint, race tests, coverage
make e2e            # repository E2E tests against the stress server
make container-test # isolated Docker Compose integration
make external-test  # temporary third-party module import + go install
```

The purpose-built target in `e2e/testserver` is a separate Go module with CPU,
allocation, database, and request-body workloads. No other public
`tuxerrante` Go repository currently provides the combination of a standalone
HTTP API, pprof, and OpenAPI needed for a stable external CI dependency, so the
consumer test is generated ephemerally instead of cloning a drifting project.

## License

See [LICENSE](LICENSE).
