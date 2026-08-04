package proficiency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(specPath, []byte("openapi: 3.0.0"), 0o600); err != nil {
		t.Fatal(err)
	}

	valid := DefaultConfig()
	valid.TargetURL = "http://localhost:8080"
	valid.OpenAPIPath = specPath

	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{name: "load mode", modify: func(*Config) {}},
		{
			name: "snapshot mode",
			modify: func(cfg *Config) {
				cfg.SkipLoad = true
				cfg.OpenAPIPath = ""
			},
		},
		{name: "missing target", modify: func(cfg *Config) { cfg.TargetURL = "" }, wantErr: "--target"},
		{name: "missing spec", modify: func(cfg *Config) { cfg.OpenAPIPath = "" }, wantErr: "--openapi"},
		{name: "zero duration", modify: func(cfg *Config) { cfg.Duration = 0 }, wantErr: "--duration"},
		{name: "zero concurrency", modify: func(cfg *Config) { cfg.Concurrency = 0 }, wantErr: "--concurrency"},
		{name: "zero rps", modify: func(cfg *Config) { cfg.RPS = 0 }, wantErr: "--rps"},
		{name: "zero timeout", modify: func(cfg *Config) { cfg.RequestTimeout = 0 }, wantErr: "request timeout"},
		{name: "zero cpu duration", modify: func(cfg *Config) { cfg.CPUDuration = 0 }, wantErr: "--cpu-duration"},
		{name: "negative top functions", modify: func(cfg *Config) { cfg.TopFunctions = -1 }, wantErr: "--top-functions"},
		{
			name: "cpu watch mode",
			modify: func(cfg *Config) {
				cfg.SkipLoad = true
				cfg.OpenAPIPath = ""
				cfg.SampleInterval = time.Second
			},
			wantErr: "CPU profiles are incompatible",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.modify(&cfg)
			err := cfg.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}
