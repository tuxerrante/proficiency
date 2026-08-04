# Proficiency

[![CI](https://github.com/tuxerrante/proficiency/actions/workflows/ci.yml/badge.svg)](https://github.com/tuxerrante/proficiency/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/tuxerrante/c40d872af91b3f8cae7757a85dc2f581/raw/coverage.json)](https://github.com/tuxerrante/proficiency)
[![Go Reference](https://pkg.go.dev/badge/github.com/tuxerrante/proficiency.svg)](https://pkg.go.dev/github.com/tuxerrante/proficiency)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13076/badge)](https://www.bestpractices.dev/projects/13076)

Proficiency generates controlled HTTP load from an OpenAPI document, collects
Go pprof profiles during that load, and writes a versioned JSON report for local
analysis and CI consumption.

The target service must expose `/debug/pprof/`. A typical service enables it
with:

```go
import _ "net/http/pprof"
```

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

The report is written before Proficiency exits non-zero for a failed profile
threshold, so CI can always upload the evidence.

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

`ReadReport` and `WriteReport` are also exported for workflows that retain or
process reports independently.

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
make test # format, lint, race tests, coverage
make e2e  # repository E2E tests against the stress server
```

## License

See [LICENSE](LICENSE).
