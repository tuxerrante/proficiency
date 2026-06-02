// Package main provides the CLI entry point for the proficiency tool.
// It orchestrates OpenAPI parsing, load generation, and profile collection.
//
// File layout:
//   - main.go    Entry point, signal handling, exit codes
//   - config.go  Config struct, flag parsing, validation
//   - run.go     Profiling workflow (load, watch, snapshot modes)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Exit codes.
const (
	exitOK         = 0
	exitConfigErr  = 1
	exitRuntimeErr = 2
)

// main parses flags, validates configuration, and runs the profiling workflow.
//
// SIGNAL HANDLING:
// - SIGINT/SIGTERM: Graceful shutdown, saves partial profiles if possible.
func main() {
	cfg := parseFlags()

	if cfg.Version {
		fmt.Printf("proficiency version %s\n", Version)
		os.Exit(exitOK)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		flag.Usage()
		os.Exit(exitConfigErr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(exitRuntimeErr)
	}
}
