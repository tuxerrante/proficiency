package main

import (
	"context"
	"os"

	"github.com/tuxerrante/proficiency"
)

func run(ctx context.Context, cfg Config) error {
	cfg.ToolVersion = Version
	cfg.Output = os.Stdout
	cfg.ErrorOutput = os.Stderr
	_, err := proficiency.Run(ctx, cfg.Config)
	return err
}
