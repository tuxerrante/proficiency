package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateConfig(t *testing.T) {
	// Create a temp OpenAPI spec file for tests that need one.
	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte("openapi: 3.0.0"), 0o600); err != nil {
		t.Fatal(err)
	}

	validBase := Config{
		TargetURL:    "http://localhost:8080",
		OpenAPIPath:  specPath,
		Duration:     30 * time.Second,
		Concurrency:  10,
		RPS:          100,
		ProfileTypes: "cpu,heap,block",
	}

	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{
			name:   "valid default config",
			modify: func(_ *Config) {},
		},
		{
			name:    "missing target",
			modify:  func(c *Config) { c.TargetURL = "" },
			wantErr: "--target is required",
		},
		{
			name:    "missing openapi with load",
			modify:  func(c *Config) { c.OpenAPIPath = "" },
			wantErr: "--openapi is required when load generation is enabled",
		},
		{
			name: "skip-load without openapi is valid",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
			},
		},
		{
			name:    "openapi file not found",
			modify:  func(c *Config) { c.OpenAPIPath = "/nonexistent/spec.yaml" },
			wantErr: "OpenAPI spec file not found",
		},
		{
			name:    "sample-interval requires skip-load",
			modify:  func(c *Config) { c.SampleInterval = 2 * time.Second },
			wantErr: "--sample-interval requires --skip-load",
		},
		{
			name:    "negative duration",
			modify:  func(c *Config) { c.Duration = -1 },
			wantErr: "--duration must be positive",
		},
		{
			name:    "zero concurrency",
			modify:  func(c *Config) { c.Concurrency = 0 },
			wantErr: "--concurrency must be positive",
		},
		{
			name:    "zero rps",
			modify:  func(c *Config) { c.RPS = 0 },
			wantErr: "--rps must be positive",
		},
		{
			name: "sample-interval below minimum",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 100 * time.Millisecond
			},
			wantErr: "--sample-interval must be at least 500ms",
		},
		{
			name: "sample-count without sample-interval",
			modify: func(c *Config) {
				c.SampleCount = 5
			},
			wantErr: "--sample-count requires --sample-interval",
		},
		{
			name: "valid watch mode",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 2 * time.Second
				c.SampleCount = 5
				c.ProfileTypes = "goroutine,heap"
			},
		},
		{
			name:    "invalid profile type",
			modify:  func(c *Config) { c.ProfileTypes = "foobar" },
			wantErr: "invalid --profile-types",
		},
		{
			name: "cpu incompatible with sample-interval",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 2 * time.Second
				c.SampleCount = 3
				c.ProfileTypes = "cpu,heap"
			},
			wantErr: "CPU profiles are incompatible with --sample-interval",
		},
		{
			name: "goroutine only in watch mode is valid",
			modify: func(c *Config) {
				c.SkipLoad = true
				c.OpenAPIPath = ""
				c.SampleInterval = 1 * time.Second
				c.SampleCount = 10
				c.ProfileTypes = "goroutine"
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBase
			tc.modify(&cfg)

			err := validateConfig(cfg)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
