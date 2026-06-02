# Implementation Details

This document provides implementation details, design decisions, tradeoffs, and alternatives for the Proficiency MVP. It serves as a reference for PR reviews and future development.

---

## Package Overview

```
cmd/proficiency/        CLI entry point (main.go, config.go, run.go)
internal/
  analysis/             Profile analysis and threshold checking
  openapi/              OpenAPI parsing
  load/                 HTTP load generation + live progress reporting
  profile/              pprof collection
testdata/               Test fixtures
```

---

## Package: `internal/openapi`

### Purpose

Parse OpenAPI 3.0 specifications to extract endpoint definitions for load testing.

### Key Types

- `Parser` - Stateful parser wrapping kin-openapi loader
- `Endpoint` - Extracted endpoint with method, path, parameters
- `Parameter` - Path/query/header parameter definition

### Design Decisions

#### 1. Library Choice: kin-openapi

**Chosen**: `github.com/getkin/kin-openapi`

**Rationale**:

- Native OpenAPI 3.0+ support (our primary target)
- Active maintenance (last release within 6 months)
- Validation built-in via `doc.Validate()`
- External reference resolution support

**Alternatives Considered**:

| Library             | Pros                | Cons                  | Why Not                                          |
| ------------------- | ------------------- | --------------------- | ------------------------------------------------ |
| `go-openapi/spec`   | Mature, widely used | Primarily Swagger 2.0 | Would need conversion layer for OpenAPI 3.0      |
| `pb33f/libopenapi`  | Very fast, newer    | Less mature           | Risk of breaking changes; kin-openapi sufficient |
| Custom YAML parsing | No dependencies     | Significant effort    | Reinventing the wheel                            |

#### 2. Parameter Handling Strategy

**Chosen**: Merge path-level and operation-level parameters, with operation taking precedence.

**Rationale**: OpenAPI spec allows parameter definition at both levels. Operation parameters should override path parameters with the same name (per spec).

**Tradeoff**: We don't validate for duplicate parameters at the same level—the last one wins. Could add validation warning in future.

#### 3. Path Parameter Resolution

**Chosen**: Replace `{param}` with provided values or type-appropriate defaults.

```go
// Integer parameters default to "1", strings to "test"
ResolvePath("/pets/{petId}", params, nil) // -> "/pets/1"
```

**Tradeoff**: Default values may not represent valid IDs in the target service.

**Alternative**: Could require explicit parameter values via config file, but increases complexity for MVP.

---

## Package: `internal/load`

### Purpose

Generate concurrent HTTP load against target endpoints with configurable rate limiting.

### Key Types

- `Runner` - Orchestrates concurrent workers with rate limiting
- `Config` - Concurrency, RPS, duration, timeout settings
- `Result` - Single request outcome with `IsError()` helper
- `Stats` - Aggregated results with per-endpoint latency
- `LiveCounters` - Cache-line-padded atomic counters for lock-free progress reads
- `ProgressReporter` - Ticker-driven status line printing during load tests

### Design Decisions

#### 1. Rate Limiting: Token Bucket

**Chosen**: `golang.org/x/time/rate` with token bucket algorithm

**Rationale**:

- Smooth request distribution (no thundering herd)
- Allows small bursts while maintaining average RPS
- Battle-tested by Go team
- Simple API: `limiter.Wait(ctx)`

**Alternatives Considered**:

| Approach         | Pros                    | Cons                               | Why Not                 |
| ---------------- | ----------------------- | ---------------------------------- | ----------------------- |
| `time.Ticker`    | No dependencies         | Manual burst handling, less smooth | More code, less tested  |
| `juju/ratelimit` | Leaky bucket option     | Additional dependency              | Token bucket sufficient |
| vegeta library   | Full load testing suite | Heavy dependency                   | Only need rate limiting |

**Tradeoff**: Token bucket allows bursts up to concurrency size. If precise request spacing is needed, leaky bucket would be better.

#### 2. Worker Model

**Chosen**: Fixed worker pool with shared rate limiter

```
┌─────────────────────────────────────┐
│           Rate Limiter              │
│  (global RPS across all workers)    │
└─────────────┬───────────────────────┘
              │
     ┌────────┼────────┐
     ▼        ▼        ▼
  Worker1  Worker2  Worker3
     │        │        │
     ▼        ▼        ▼
  Results Channel (buffered)
```

**Rationale**:

- Each worker makes sequential requests (simpler than async per-worker)
- Rate limiter coordinates global RPS
- Channel-based result collection avoids lock contention

**Tradeoff**: Workers may idle waiting on rate limiter if RPS < Concurrency. Acceptable for our use case where RPS >> Concurrency typically.

#### 3. Connection Pooling

**Chosen**: Sized transport with `MaxIdleConnsPerHost = Concurrency`

**Rationale**: Ensures each worker can maintain a warm connection, reducing latency variance from connection setup.

**Tradeoff**: Uses more memory and file descriptors. For very high concurrency, may need tuning.

#### 4. Endpoint Distribution

**Chosen**: Round-robin across endpoints, each worker starts at offset

**Rationale**: Simple, deterministic, good coverage.

**Alternative**: Weighted distribution based on endpoint frequency in real traffic. Could add via config, but requires traffic analysis data we don't have in MVP.

#### 5. Live Progress Reporting

**Chosen**: Atomic counters with cache-line padding, read by a `time.Ticker` goroutine

```
┌──────────┐     atomic.Add(1)     ┌──────────────┐
│ Worker 1 │──────────────────────▶│ LiveCounters  │
│ Worker 2 │──────────────────────▶│  .Requests    │
│ Worker N │──────────────────────▶│  .Errors      │
└──────────┘                       └──────┬───────┘
                                          │ atomic.Load()
                                          ▼
                                   ┌──────────────────┐
                                   │ ProgressReporter  │
                                   │ (1s Ticker → \r)  │
                                   └──────────────────┘
```

**Rationale**:

- Lock-free: atomics use single CPU instructions, no goroutine blocking
- Cache-line padding (`[56]byte`) prevents false sharing between cores
- Counters increment only after successful channel send to keep Counters == Stats
- `sync.Once` on `Stop()` prevents double-close panic

**Alternatives Considered**:

| Approach     | Pros                         | Cons                                    | Why Not                            |
| ------------ | ---------------------------- | --------------------------------------- | ---------------------------------- |
| `sync.Mutex` | Consistent multi-field reads | Serializes all workers on every request | Unnecessary contention at high RPS |
| Channel      | Natural Go concurrency       | Scheduling overhead per request         | Wrong tool for counters            |
| No progress  | Zero overhead                | Silent during long tests                | Poor UX                            |

**Tradeoff**: `\r` carriage-return status line only works on TTY. Non-TTY environments (CI, piped stderr) get garbled output. A `--no-progress` flag or `isatty` check should be added.

---

## Package: `internal/profile`

### Purpose

Collect pprof profiles from target service via HTTP.

### Key Types

- `Collector` - HTTP client for pprof endpoints
- `CollectorConfig` - Target URL, output directory, durations
- `CollectedProfile` - Metadata about saved profile

### Design Decisions

#### 1. HTTP-Based Collection

**Chosen**: Fetch profiles via `/debug/pprof/*` HTTP endpoints

**Rationale**:

- Standard Go pprof interface
- Works with any Go service exposing pprof (no code changes needed)
- Network isolation matches real-world scenarios
- Simple implementation

**Alternatives Considered**:

| Approach             | Pros                         | Cons                                  | Why Not              |
| -------------------- | ---------------------------- | ------------------------------------- | -------------------- |
| In-process profiling | Lower overhead, more precise | Requires instrumenting target         | Not external tool    |
| gRPC pprof           | Lower overhead than HTTP     | Non-standard, requires target changes | Extra complexity     |
| Agent-based          | Can profile any process      | Requires deployment, privileges       | Out of scope for MVP |

**Tradeoff**: HTTP adds latency and may miss very short-lived bottlenecks. Mitigated by collecting profiles during sustained load.

#### 2. Parallel Profile Collection

**Chosen**: Collect all profile types in parallel during load test

**Rationale**:

- CPU profiling starts immediately with the load test
- Snapshot profiles (heap, block, goroutine) are staggered late in the load window (80-90% elapsed) to capture peak-load state
- Parallel collection via goroutines maximizes the profiling window

**Tradeoff**: Concurrent profile collection may add load to the target service. Mitigated by staggering snapshots and keeping collection lightweight (HTTP GETs).

#### 3. File-Based Output

**Chosen**: Save profiles to `./profiles/<type>_<timestamp>.pprof`

**Rationale**:

- Matches pprof workflow (analyze with `go tool pprof`)
- Timestamp ensures uniqueness
- Directory configurable via flag

**Tradeoff**: No automatic cleanup. Old profiles accumulate.

**Alternative**: Write to stdout/memory for piping. Less useful for our workflow where analysis comes later.

---

## Package: `cmd/proficiency`

### Purpose

CLI entry point orchestrating the profiling workflow.

### Design Decisions

#### 1. Flag Parsing: Standard Library

**Chosen**: `flag` package

**Rationale**:

- Zero dependencies
- Familiar to Go developers
- Sufficient for flat flag set

**Alternatives Considered**:

| Library      | Pros                     | Cons               | Why Not                   |
| ------------ | ------------------------ | ------------------ | ------------------------- |
| `cobra`      | Subcommands, rich help   | Heavy dependency   | No subcommands needed yet |
| `urfave/cli` | Nice API, dotenv support | Another dependency | Overkill for MVP          |
| `pflag`      | POSIX-style flags        | Minor benefit      | Standard flag is fine     |

**Migration Path**: If we add subcommands (e.g., `proficiency analyze`), migrate to cobra.

#### 2. Workflow: Parallel Load + Profiling

**Chosen**: Parse → Verify pprof → [Load test + Live progress + Profile collection]

**Rationale**:

- Early failure on invalid spec or unreachable target
- Load test and profile collection run in parallel for efficiency
- Live progress reporter prints status every second during load
- Reporter stops explicitly before post-load output to prevent stderr interleaving

**Alternative**: Fully sequential (load then profile). Simpler but misses the ability to profile under live load.

#### 3. Signal Handling

**Chosen**: Graceful shutdown on SIGINT/SIGTERM via context cancellation

**Rationale**:

- Clean worker shutdown
- Partial results still collected
- Standard Unix behavior

---

## Testing Strategy

### Unit Test Coverage Targets

| Package | Target | Actual |
| ------- | ------ | ------ |
| openapi | >90%   | 94.7%  |
| load    | >90%   | 97.2%  |
| profile | >75%   | 78.9%  |

### Test Patterns Used

1. **Table-driven tests** for parameter variations
2. **httptest.Server** for HTTP mocking
3. **t.TempDir()** for file system isolation
4. **Context cancellation** tests for graceful shutdown

### Integration Testing

Not included in MVP. Recommended for future:

- Spin up real Go service with pprof
- Run full proficiency workflow
- Verify profile files are valid pprof format

---

## Future Considerations

### Not Implemented (Intentionally Deferred)

1. **Profile Analysis** - Issue #2 scope
2. **JSON Report Generation** - Issue #2 scope
3. **Request Body Generation** - Would require schema-aware data generation
4. **Authentication** - Bearer token support for protected APIs
5. **Swagger 2.0 Support** - kin-openapi handles this, but not tested
6. **Goroutine Profile** - Easy to add, not in acceptance criteria

### Known Limitations

1. **No request body generation** - POST/PUT endpoints hit without body
2. **Path parameters use defaults** - May cause 404s on some endpoints
3. **Single target URL** - Can't profile multi-service architectures
4. **No TLS verification skip** - Self-signed certs will fail

---

## Dependencies

| Dependency                      | Version  | Purpose         | License |
| ------------------------------- | -------- | --------------- | ------- |
| `github.com/getkin/kin-openapi` | v0.133.0 | OpenAPI parsing | MIT     |
| `golang.org/x/time`             | v0.14.0  | Rate limiting   | BSD-3   |
| `golang.org/x/term`             | v0.43.0  | TTY detection   | BSD-3   |

All are well-maintained, widely used, and have permissive licenses.
