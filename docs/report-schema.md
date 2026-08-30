# Report schema v1

Proficiency reports are JSON documents identified by:

```json
{
  "schemaVersion": "v1"
}
```

Readers must reject unsupported schema versions. Additive fields may be added
within `v1`; existing field names, units, and meanings must not change. A
breaking field or semantic change requires a new schema version.

## Top-level fields

| Field         | Meaning                                                     |
| ------------- | ----------------------------------------------------------- |
| `timestamp`   | UTC report generation time                                  |
| `toolVersion` | CLI or package producer version                             |
| `metadata`    | Optional label, repository, revision, and ref               |
| `runConfig`   | Inputs that materially affect collection                    |
| `profiles`    | Saved pprof artifact metadata; always an array              |
| `loadStats`   | Aggregate load measurements; absent for skip-load modes     |
| `analysis`    | Ranked flat-cost functions; always an array                 |
| `thresholds`  | Per-function absolute threshold outcome                     |
| `comparison`  | Optional baseline/current deltas and regression gate result |

Durations in run configuration and profile metadata use milliseconds.
Endpoint latency values use integer microseconds to preserve short request
measurements without floating-point duration ambiguity.

Endpoint load statistics include `p50UpperBoundMicros`,
`p95UpperBoundMicros`, and `p99UpperBoundMicros`. They are nearest-rank bounds
from fixed streaming buckets spanning 100µs to 10s. Samples above 10s use the
maximum observed latency as their bound. This keeps collection memory constant
regardless of request count, without presenting bucket-level precision as an
exact percentile. Percentile bounds are informational in v1; latency regression
rules continue to evaluate endpoint averages. As with the existing min, max,
and average values, the histogram includes both successful and failed request
attempts. A finite bucket bound can be greater than `maxMicros`; it describes
the configured bucket containing the percentile, not another observed sample.

Each profile records both its collection `type` and comparison `metric`.
Heap files use `type: "heap"` and `metric: "alloc"` because the CLI collection
name and pprof allocation-analysis vocabulary intentionally differ.
`thresholds.rules` and `thresholds.violations` are also always arrays.

## Analysis

Each analysis entry identifies a profile type (`cpu`, `alloc`, `block`, or
`goroutine`) and records up to `runConfig.topFunctions` functions. Percentage
is the function's flat share of the selected pprof sample type.

The list is deterministic: percentage descending, then function name
ascending.

## Comparison

`comparison.metrics` contains both improvements and degradations. `outcome` is
always directional and does not change when a rule is configured. Change is
normalized so positive values always mean worse:

- latency: relative endpoint average-latency increase
- throughput: relative requests-per-second decrease
- error rate: percentage-point increase
- profile functions: flat-share percentage-point increase

New, removed, and zero-baseline endpoints/functions are reported but are not
gated because there is no comparable relative value. `withinLimit` is present
only when a rule applies. A metric appears in `comparison.regressions` only
when its relative limit is exceeded and, for latency/throughput, its absolute
noise floor is also exceeded.

Latency noise floors are stored in microseconds. Throughput noise floors are
stored in requests per second.

## File guarantees

- Reports are written through a temporary file and atomically renamed.
- Report files use mode `0600`; parent directories use `0750`.
- Readers reject documents larger than 16 MiB, multiple JSON values, missing
  schema versions, and unsupported schema versions.
- A report is written before a configured performance gate returns a non-zero
  process exit.
