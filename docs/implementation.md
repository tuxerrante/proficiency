# Implementation details

## Package layout

```text
config.go, run.go       Public configuration and orchestration API
report.go               Versioned report model and durable JSON I/O
cmd/proficiency/        Flags, signals, version injection, and exit codes
internal/analysis/      pprof function ranking and absolute thresholds
internal/load/          Rate-limited HTTP load generation
internal/openapi/       OpenAPI parsing and request synthesis
internal/profile/       HTTP pprof collection
e2e/testserver/         Independent stress-target Go module
```

## Public orchestration

`proficiency.Run(context.Context, Config)` is the single workflow used by the
CLI and imported callers:

1. Validate configuration.
2. Parse the OpenAPI document unless load generation is disabled.
3. Verify pprof availability.
4. Run load and profile collection in parallel, or collect snapshot/watch
   profiles.
5. Analyze the collected pprof files.
6. Evaluate absolute profile thresholds.
7. Build the versioned report.
8. Atomically persist the report.
9. Return `*GateError` if a configured threshold failed.

The report is returned alongside `GateError`, and is written before the error
is returned. Operational failures such as an unreadable OpenAPI document or
unreachable pprof endpoint return without a success-shaped report.

## OpenAPI and load generation

`kin-openapi` provides OpenAPI 3 validation and external-reference resolution.
Endpoints are exercised round-robin by a fixed worker pool coordinated through
`golang.org/x/time/rate`.

JSON request bodies use the first available source in this order:

1. media-type example
2. named example, sorted by key
3. schema example
4. schema default
5. deterministic type placeholders

Only JSON media types are synthesized. Unsupported body formats are skipped
instead of guessed.

## Profile collection

Profiles are fetched over the standard `/debug/pprof/` HTTP surface and saved
with mode `0600`.

During load:

- CPU collection starts immediately.
- heap and goroutine snapshots start at 80% of the load window.
- block snapshots start at 90% of the load window.

Each collection goroutine has a context-controlled termination path and a
buffered result slot. A load failure cancels and drains all profile workers.
If every requested collection fails, the run fails instead of emitting an
empty success report.

## Analysis and gates

`internal/analysis` parses pprof protobufs with
`github.com/google/pprof/profile`, aggregates flat values by function, and
sorts by percentage descending then function name.

`--fail-on` configures absolute function-share limits within the current run.
The report is still written before a failed gate returns `GateError`.

## Report I/O

The `v1` schema is documented in
[report-schema.md](report-schema.md). Writers use a temporary file, flush it,
and atomically rename it into place. Readers enforce a 16 MiB limit and reject
unsupported schema versions or trailing JSON values.

## Delivery surfaces

### CLI

`cmd/proficiency` is deliberately thin. It binds flags to the public `Config`,
injects the build version, supplies process writers, and maps errors to exit
codes.

### Go package

External modules import `github.com/tuxerrante/proficiency` and call `Run` or
the report I/O helpers directly.

## Validation layers

| Layer               | Command               |
| ------------------- | --------------------- |
| Unit and race tests | `go test -race ./...` |
| Lint and coverage   | `make coverage`       |
| Repository E2E      | `make e2e`            |

## Deliberate trade-offs

- Reports store top-N bottlenecks, not complete pprof samples. Raw profiles
  remain available for detailed investigation.
- Increasing `--top-functions` improves report detail at the cost of larger
  artifacts and more analysis work.
