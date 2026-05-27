// Package main provides a stress test HTTP server with intentional
// performance inefficiencies for E2E testing of proficiency.
//
// Endpoints:
//   - GET /stress/cpu      - CPU-intensive math operations
//   - GET /stress/memory   - Large heap allocations
//   - GET /stress/db       - Unbatched SQLite inserts (I/O overhead)
//   - GET /health          - Readiness probe
//   - /debug/pprof/*       - Standard pprof endpoints
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Enable block and mutex profiling so contention appears in profiles.
	runtime.SetBlockProfileRate(1)
	runtime.SetMutexProfileFraction(1)

	db, err := sql.Open("sqlite", "stress.db")
	if err != nil {
		logger.Error("failed to open DB", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	// Disable WAL and force synchronous writes to maximize I/O overhead.
	if _, err := db.Exec("PRAGMA journal_mode = DELETE; PRAGMA synchronous = FULL;"); err != nil {
		logger.Error("failed to set PRAGMA", "error", err)
	}

	mux := http.NewServeMux()

	// pprof endpoints on the main mux (same port as the API).
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, map[string]any{"status": "ok"})
	})

	mux.HandleFunc("GET /stress/cpu", handleCPUStress)
	mux.HandleFunc("GET /stress/memory", handleMemoryStress)
	mux.HandleFunc("GET /stress/db", handleDBStress(db))

	server := &http.Server{
		Addr:        ":8080",
		Handler:     mux,
		ReadTimeout: 30 * time.Second,
	}

	// Also expose pprof on the standard :6060 via DefaultServeMux.
	go func() {
		logger.Info("pprof server listening", "addr", ":6060")
		pprofServer := &http.Server{
			Addr:        ":6060",
			Handler:     nil, // DefaultServeMux where pprof registers itself.
			ReadTimeout: 60 * time.Second,
		}
		if err := pprofServer.ListenAndServe(); err != nil {
			logger.Error("pprof server failed", "error", err)
		}
	}()

	logger.Info("API server listening", "addr", ":8080")
	if err := server.ListenAndServe(); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// handleCPUStress generates CPU load via repeated trigonometric calculations.
// The math.Tan * math.Atan loop is deliberately expensive and will appear
// prominently in CPU profiles.
func handleCPUStress(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	iterations := parseIntParam(r, "iterations", 1_000_000)

	var val float64
	for i := range iterations {
		val += math.Tan(float64(i)) * math.Atan(float64(i))
	}

	respondJSON(w, map[string]any{
		"duration_ms": time.Since(start).Milliseconds(),
		"result":      val,
	})
}

// handleMemoryStress allocates a large byte slice on the heap and touches
// every page to ensure physical allocation. Visible in heap profiles as
// a flat allocation of size_mb megabytes.
func handleMemoryStress(w http.ResponseWriter, r *http.Request) {
	sizeMB := parseIntParam(r, "size_mb", 10)

	data := make([]byte, sizeMB*1024*1024)
	// Touch pages to force physical allocation (not just virtual).
	for i := 0; i < len(data); i += 4096 {
		data[i] = 1
	}

	respondJSON(w, map[string]any{
		"allocated_mb": sizeMB,
		"status":       "ok",
		"check":        data[0],
	})
}

// handleDBStress performs unbatched SQLite inserts — each INSERT runs its own
// transaction (lock → write → sync → unlock). This is intentionally
// inefficient and shows up in CPU profiles as repeated database/sql and
// SQLite function calls without batching.
func handleDBStress(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rows := parseIntParam(r, "rows", 20)

		tableName := fmt.Sprintf("logs_%d", time.Now().UnixNano())
		_, err := db.Exec(fmt.Sprintf(
			"CREATE TABLE %s (id INTEGER PRIMARY KEY, data TEXT, created_at TIMESTAMP)", tableName))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _, _ = db.Exec(fmt.Sprintf("DROP TABLE %s", tableName)) }()

		stmt, err := db.Prepare(fmt.Sprintf(
			"INSERT INTO %s (data, created_at) VALUES (?, ?)", tableName))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = stmt.Close() }()

		// Individual inserts without a wrapping transaction — intentionally bad.
		for i := range rows {
			if _, err := stmt.Exec(fmt.Sprintf("payload-data-%d", i), time.Now()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		var count int
		_ = db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", tableName)).Scan(&count)

		respondJSON(w, map[string]any{
			"duration_ms":   time.Since(start).Milliseconds(),
			"rows_inserted": count,
			"status":        "real_db_io_performed",
		})
	}
}

func parseIntParam(r *http.Request, name string, defaultVal int) int {
	s := r.URL.Query().Get(name)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return defaultVal
	}
	return v
}

func respondJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}
