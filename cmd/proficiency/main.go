// Package main provides the CLI entry point for the proficiency tool.
//
// File layout:
//   - main.go    Entry point, signal handling, exit codes
//   - config.go  Config struct, flag parsing, validation
//   - run.go     Profiling workflow (load, watch, snapshot modes)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/tuxerrante/proficiency"
)

// Version is set at build time via -ldflags.
var Version = developmentVersion

const developmentVersion = "dev"

// Exit codes.
const (
	exitOK         = 0
	exitConfigErr  = 1
	exitRuntimeErr = 2
	exitGateErr    = 3
)

// main parses flags, validates configuration, and runs the profiling workflow.
//
// SIGNAL HANDLING:
// - SIGINT/SIGTERM: Graceful shutdown, saves partial profiles if possible.
func main() {
	cfg := parseFlags()
	version := currentVersion()

	if cfg.Version {
		fmt.Printf("proficiency version %s\n", version)
		os.Exit(exitOK)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		flag.Usage()
		os.Exit(exitConfigErr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, version); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(errorExitCode(err))
	}
}

func currentVersion() string {
	moduleVersion := ""
	moduleSum := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		moduleVersion = info.Main.Version
		moduleSum = info.Main.Sum
	}
	return resolveVersion(Version, moduleVersion, moduleSum)
}

func resolveVersion(injected, moduleVersion, moduleSum string) string {
	if injected != "" && injected != developmentVersion {
		return injected
	}
	if moduleVersion != "" && moduleVersion != "(devel)" && moduleSum != "" {
		return moduleVersion
	}
	return developmentVersion
}

func errorExitCode(err error) int {
	var gateErr *proficiency.GateError
	if errors.As(err, &gateErr) {
		return exitGateErr
	}
	return exitRuntimeErr
}
