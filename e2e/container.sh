#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/e2e/docker-compose.yml"
output_dir="$repo_root/e2e/container-output"
export PROFICIENCY_UID
export PROFICIENCY_GID
PROFICIENCY_UID="$(id -u)"
PROFICIENCY_GID="$(id -g)"

cleanup() {
  docker compose -f "$compose_file" down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup
rm -rf "$output_dir"
mkdir -p "$output_dir"

docker compose -f "$compose_file" up --build --abort-on-container-exit --exit-code-from proficiency

test -s "$output_dir/report.json"
grep -q '"schemaVersion": "v1"' "$output_dir/report.json"
grep -q '"totalRequests":' "$output_dir/report.json"
