package main

import (
	"context"
	"os"

	"github.com/tuxerrante/proficiency"
)

func run(ctx context.Context, cfg Config, version string) error {
	cfg.ToolVersion = version
	cfg.Output = os.Stdout
	cfg.ErrorOutput = os.Stderr
	_, err := proficiency.Run(ctx, cfg.Config)
	return err
}
