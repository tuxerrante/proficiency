# Architecture

Proficiency has one reusable orchestration package and thin delivery adapters.

```text
external caller / cmd/proficiency / action.yml
                    |
                    v
          github.com/tuxerrante/proficiency
          Config -> Run -> Report -> CompareReports
                    |
          +---------+---------+----------+
          |                   |          |
   internal/openapi     internal/load  internal/profile
                                        |
                                  internal/analysis
```

## Public package

The module root owns the stable external contract:

- `Config`, `DefaultConfig`, and `Run`
- versioned report types plus `ReadReport` and `WriteReport`
- report comparison and regression rules
- `GateError`, returned after evidence has been persisted

The package accepts output writers instead of writing process-global stdout or
stderr. The CLI supplies `os.Stdout` and `os.Stderr`; imported callers may use
buffers, structured adapters, or no output.

## CLI

`cmd/proficiency` only handles flags, environment metadata, version output,
signals, and process exit codes. Profiling logic must remain in the public
package so the CLI and imported API cannot diverge.

## GitHub Action

The Action is composite and runs the CLI in the runner's network namespace.
This allows it to profile a target bound to `localhost`, which a Docker
container action could not reliably reach.

Released tags provide checksum-verified archives. The explicit
`version: source` mode builds the checked-out action source and exists for
pre-release and repository CI validation; failed release downloads never
silently fall back to source.

## Containers

The root `Dockerfile` provides a standalone non-root CLI image. It is used by
the Docker Compose integration test, where the CLI and the separate
`e2e/testserver` module communicate over an isolated network. The image is not
the Action runtime because that would break the common localhost target model.

## Reports and comparisons

Raw pprof files remain the source for deep manual analysis. The JSON report
stores stable aggregate measurements and ranked bottlenecks so an external
project can version artifacts and compare CI runs without retaining every raw
profile indefinitely.

See [report-schema.md](report-schema.md) for compatibility and metric
semantics.
