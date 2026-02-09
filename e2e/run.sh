#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
SERVER_BIN="$PROJECT_DIR/bin/testserver"
PROFICIENCY_BIN="$PROJECT_DIR/proficiency"
PROFILES_DIR="$PROJECT_DIR/e2e-profiles"
SERVER_PORT=8080

cleanup() {
    echo ""
    echo "==> Cleaning up..."
    if [[ -n "${SERVER_PID:-}" ]]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -f "$PROJECT_DIR/stress.db"
}
trap cleanup EXIT

# ---------- Build ----------
echo "==> Building E2E test server..."
mkdir -p "$PROJECT_DIR/bin"
cd "$SCRIPT_DIR/testserver" && go build -o "$SERVER_BIN" .

echo "==> Building proficiency CLI..."
cd "$PROJECT_DIR" && go build -o "$PROFICIENCY_BIN" ./cmd/proficiency

# ---------- Start test server ----------
rm -rf "$PROFILES_DIR"
mkdir -p "$PROFILES_DIR"

echo "==> Starting test server on :$SERVER_PORT..."
cd "$PROJECT_DIR" && "$SERVER_BIN" &
SERVER_PID=$!
echo "    PID=$SERVER_PID"

# Wait for health check
echo "==> Waiting for server to be ready..."
for i in $(seq 1 15); do
    if curl -sf "http://localhost:$SERVER_PORT/health" > /dev/null 2>&1; then
        echo "    Server is ready."
        break
    fi
    if [[ $i -eq 15 ]]; then
        echo "Error: test server failed to start within 15 seconds"
        exit 1
    fi
    sleep 1
done

# ---------- Run proficiency ----------
echo ""
echo "==> Running proficiency (load + profiling in parallel)..."
"$PROFICIENCY_BIN" \
    --openapi "$SCRIPT_DIR/openapi.yaml" \
    --target "http://localhost:$SERVER_PORT" \
    --duration 15s \
    --concurrency 5 \
    --rps 50 \
    --cpu-duration 15s \
    --output "$PROFILES_DIR"

# ---------- Profile analysis ----------
echo ""
echo "========================================="
echo "           PROFILE ANALYSIS"
echo "========================================="

echo ""
echo "--- CPU Profile (top functions) ---"
go tool pprof -top "$PROFILES_DIR"/cpu_*.pprof 2>/dev/null || echo "  No CPU profile found"

echo ""
echo "--- Heap Profile (top allocations by space) ---"
go tool pprof -top -alloc_space "$PROFILES_DIR"/heap_*.pprof 2>/dev/null || echo "  No heap profile found"

echo ""
echo "--- Block Profile (I/O and lock contention) ---"
go tool pprof -top "$PROFILES_DIR"/block_*.pprof 2>/dev/null || echo "  No block profile found"

echo ""
echo "========================================="
echo ""
echo "Profile files:"
ls -lh "$PROFILES_DIR"/*.pprof 2>/dev/null || echo "  No profiles found"

echo ""
echo "========================================="
echo " HOW TO ANALYZE PROFILES"
echo "========================================="
echo ""
echo "  Interactive CLI:"
echo "    go tool pprof $PROFILES_DIR/cpu_*.pprof"
echo ""
echo "  Web UI (recommended):"
echo "    go tool pprof -http=:8081 $PROFILES_DIR/cpu_*.pprof"
echo "    go tool pprof -http=:8081 $PROFILES_DIR/heap_*.pprof"
echo "    go tool pprof -http=:8081 $PROFILES_DIR/block_*.pprof"
echo ""
echo "  Flamegraph:"
echo "    go tool pprof -http=:8081 -flame $PROFILES_DIR/cpu_*.pprof"
echo ""
echo "  Compare profiles:"
echo "    go tool pprof -diff_base=old.pprof new.pprof"
echo ""
echo "========================================="
echo " EXPECTED INEFFICIENCIES IN PROFILES"
echo "========================================="
echo ""
echo "  1. CPU overhead:"
echo "     math.Tan / math.Atan in /stress/cpu handler"
echo "     (heavy trigonometric computation in tight loop)"
echo ""
echo "  2. Memory pressure:"
echo "     Large []byte allocations in /stress/memory handler"
echo "     (10 MB per request, visible in heap alloc_space)"
echo ""
echo "  3. Bad SQL utilization:"
echo "     Individual INSERT statements without batching in /stress/db"
echo "     (database/sql.(*DB).Exec, sqlite operations without transactions)"
echo ""
echo "==> E2E test complete!"
