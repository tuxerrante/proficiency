# Test App for Proficiency Manual Testing

This directory contains a simple Go HTTP service designed to test the Proficiency tool's ability to generate load and collect pprof profiles.

## 📋 Table of Contents

- [Purpose](#purpose)
- [Architecture](#architecture)
- [Running the Test](#running-the-test)
- [Verification](#verification)

---

## Purpose

This test application serves as a **proof-of-concept** to validate that:

1. ✅ Proficiency can parse an OpenAPI 3.0 spec
2. ✅ Load generation works against real HTTP endpoints
3. ✅ pprof profiles (CPU, heap) are successfully collected
4. ✅ The entire workflow runs end-to-end without errors

**Task Context**: This was developed as part of the manual testing task to validate Proficiency's core functionality before proceeding with automated analysis features.

---

## Architecture

### Components

```
test-app/
├── main.go              # HTTP service with 6 endpoints + pprof
├── docs/
│   └── swagger.yaml     # OpenAPI 3.0 spec (generated via swag)
├── go.mod               # Module dependencies
└──  run-test.sh          # Automated test script
```

### Service Details

**Base URL**: `http://localhost:6060`

**Endpoints** 

| Method | Path                    | Description                | Simulated Load         |
| ------ | ----------------------- | -------------------------- | ---------------------- |
| GET    | `/pets`                 | List all pets              | 10ms CPU work          |
| POST   | `/pets`                 | Create a pet               | 100KB memory alloc     |
| GET    | `/pets/{id}`            | Get pet by ID              | 5ms CPU work           |
| DELETE | `/pets/{id}`            | Delete pet                 | Instant                |
| GET    | `/pets/{id}/photos`     | Get pet photos (stub)      | Instant                |
| GET    | `/health`               | Health check               | Instant                |
| GET    | `/debug/pprof/`         | pprof index (auto-exposed) | -                      |
| GET    | `/debug/pprof/profile`  | CPU profile                | -                      |
| GET    | `/debug/pprof/heap`     | Heap snapshot              | -                      |

**Simulated Workload**:
- `simulateWork()`: Recursively calculates Fibonacci(20) to generate CPU activity
- `simulateMemoryWork()`: Allocates 100KB slices to create memory pressure

---

### OpenAPI Generation

#### Attempted Approach: swaggo/swag

Initially tried to auto-generate OpenAPI 3.0 from code annotations:

```bash
# Install swag
go install github.com/swaggo/swag/cmd/swag@latest

# Generate docs
swag init --output ./docs --outputTypes yaml
```

**Result**: Generated `docs/swagger.yaml` with OpenAPI 3.0.3 spec.

**Challenge Encountered**: swag generates valid OpenAPI but with some quirks:
- Empty `externalDocs` field that fails validation
- Requires detailed annotations for every handler

**Resolution**: Used [`run-test.sh`](run-test.sh) to post-process the generated YAML:
```bash
sed -i.bak '/^externalDocs:/,/^  url: ""$/d' docs/swagger.yaml
```

### Test Automation Script

Created [`run-test.sh`](run-test.sh) to orchestrate the entire test:

**Workflow**:
1. Generate OpenAPI spec with swag
2. Fix empty externalDocs issue
3. Build test service binary
4. Start service in background (PID captured)
5. Verify service health
6. Build Proficiency CLI
7. Run Proficiency with generated YAML
8. Cleanup: kill service, display results

---

## Running the Test

### Prerequisites

1. **Go 1.25+** installed
2. **swag** installed:
   ```bash
   go install github.com/swaggo/swag/cmd/swag@latest
   ```
3. Generate OpenAPI 3.1 spec:
    ```bash
    swag init --output ./docs --outputTypes yaml --v3.1
    ```
### Manual Testing (Alternative)

If you want to test components individually:

#### 1. Start Service Manually

```bash
cd test-app
go run main.go
```

#### 2. Test Endpoints

```bash
# Health check
curl http://localhost:6060/health

# List pets
curl http://localhost:6060/pets | jq .

# Get specific pet
curl http://localhost:6060/pets/1 | jq .

# Create pet
curl -X POST http://localhost:6060/pets \
  -H "Content-Type: application/json" \
  -d '{"name":"Rex","tag":"dog"}' | jq .

# Delete pet
curl -X DELETE http://localhost:6060/pets/1

# Check pprof
curl http://localhost:6060/debug/pprof/
```

#### 3. Run Proficiency Manually

```bash
cd ..
./proficiency \
  --swagger ./test-app/docs/swagger.yaml \
  --target http://localhost:6060 \
  --duration 10s \
  --concurrency 3 \
  --rps 20
```

---

## Verification

### 1. Check Generated Profiles

```bash
# Verify files exist
ls -lh ../profiles/

# Analyze CPU profile
go tool pprof -http=:8080 ../profiles/cpu_*.pprof
```

**Expected Hotspots in CPU Profile**:
- `main.fibonacci` (recursive calls in simulateWork)
- `main.simulateWork` (called by listPets, getPet)
- HTTP handler functions (`main.petsHandler`, etc.)

**What to Look For**:
- **Flat %**: Direct CPU time in function
- **Cum %**: Cumulative time including callees
- Top functions should show `fibonacci`, `simulateWork`, `petsHandler`

### 2. Analyze Heap Profile

```bash
go tool pprof -http=:8080 ../profiles/heap_*.pprof
```

**Expected Allocations**:
- `main.simulateMemoryWork` (100KB slice allocations)
- `encoding/json.Encoder` (from response serialization)
- HTTP request/response buffers

### 3. Validate OpenAPI Spec

```bash
# Check it's valid OpenAPI 3.0
head -n 5 test-app/docs/swagger.yaml
```

### 4. Endpoint Coverage

Verify all endpoints are hit:

```bash
# In proficiency output, look for:
Parsed 6 endpoints from ./test-app/docs/swagger.yaml
  GET /pets
  POST /pets
  GET /pets/{id}
  DELETE /pets/{id}
  GET /pets/{id}/photos
  GET /health
```

---

## Summary

This test app validates Proficiency's core functionality:

✅ **OpenAPI Parsing**: Reads and validates OpenAPI 3.0 specs  
✅ **Load Generation**: Generates configurable HTTP load (RPS, concurrency, duration)  
✅ **pprof Integration**: Collects CPU and heap profiles via HTTP