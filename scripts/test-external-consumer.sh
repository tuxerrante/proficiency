#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
consumer_dir="$(mktemp -d 2>/dev/null || mktemp -d -t proficiency-consumer)"

cleanup() {
  rm -rf "$consumer_dir"
}
trap cleanup EXIT

cat > "$consumer_dir/go.mod" <<EOF
module example.com/proficiency-consumer

go 1.27.0

require github.com/tuxerrante/proficiency v0.0.0

replace github.com/tuxerrante/proficiency => $repo_root
EOF

cat > "$consumer_dir/proficiency_test.go" <<'EOF'
package consumer

import (
	"testing"

	"github.com/tuxerrante/proficiency"
)

func TestPublicAPI(t *testing.T) {
	config := proficiency.DefaultConfig()
	if config.TopFunctions == 0 {
		t.Fatal("expected analysis to be enabled by default")
	}

	rules, err := proficiency.ParseRegressionRules("latency:10:200us,cpu:5")
	if err != nil {
		t.Fatalf("parse regression rules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %d", len(rules))
	}
}
EOF

(
  cd "$consumer_dir"
  go mod tidy
  go test ./...
)

install_dir="$consumer_dir/bin"
mkdir -p "$install_dir"
(
  cd "$repo_root"
  GOBIN="$install_dir" go install ./cmd/proficiency
)
"$install_dir/proficiency" --version
