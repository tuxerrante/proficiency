#!/usr/bin/env bash
set -euo pipefail

action_path="${GITHUB_ACTION_PATH:?GITHUB_ACTION_PATH is required}"
runner_temp="${RUNNER_TEMP:?RUNNER_TEMP is required}"
workspace="${GITHUB_WORKSPACE:?GITHUB_WORKSPACE is required}"
input_version="${INPUT_VERSION:-}"
action_ref="${GITHUB_ACTION_REF:-}"
version="${input_version:-$action_ref}"
version="${version#refs/tags/}"
install_dir="$runner_temp/proficiency-action"
binary="$install_dir/proficiency"

mkdir -p "$install_dir"

if [[ -z "$version" ]]; then
  echo "Set version to a release tag (for example v0.2.0) or to source" >&2
  exit 1
fi
if [[ "$version" != "source" && ! "$version" =~ ^v[0-9] ]]; then
  if [[ -z "$input_version" && "$action_ref" =~ ^[0-9a-fA-F]{40}$ ]]; then
    echo "The action is pinned by commit SHA; set version to a release tag or to source" >&2
  else
    echo "Version must be a release tag beginning with v, or source" >&2
  fi
  exit 1
fi
if [[ -z "${INPUT_OUTPUT_DIR:-}" ]]; then
  echo "output-dir cannot be empty" >&2
  exit 1
fi
if [[ -z "${INPUT_REPORT_PATH:-}" ]]; then
  echo "report-path cannot be empty" >&2
  exit 1
fi

if [[ "$version" == "source" ]]; then
  (
    cd "$action_path"
    go build -trimpath -ldflags="-s -w -X main.Version=${GITHUB_ACTION_REF:-source}" \
      -o "$binary" ./cmd/proficiency
  )
else
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *)
      echo "Unsupported runner operating system: $(uname -s)" >&2
      exit 1
      ;;
  esac

  case "$(uname -m)" in
    x86_64 | amd64) arch="amd64" ;;
    arm64 | aarch64) arch="arm64" ;;
    *)
      echo "Unsupported runner architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac

  asset="proficiency_${version}_${os}_${arch}.tar.gz"
  base_url="https://github.com/tuxerrante/proficiency/releases/download/${version}"
  curl --fail --location --silent --show-error "$base_url/$asset" -o "$install_dir/$asset"
  curl --fail --location --silent --show-error "$base_url/checksums.txt" -o "$install_dir/checksums.txt"

  expected="$(awk -v asset="$asset" '$2 == asset { print $1 }' "$install_dir/checksums.txt")"
  if [[ -z "$expected" ]]; then
    echo "No checksum found for $asset" >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$install_dir/$asset" | awk '{ print $1 }')"
  else
    actual="$(shasum -a 256 "$install_dir/$asset" | awk '{ print $1 }')"
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "Checksum verification failed for $asset" >&2
    exit 1
  fi

  tar -xzf "$install_dir/$asset" -C "$install_dir" proficiency
fi

if [[ "${INPUT_WORKING_DIRECTORY:-.}" = /* ]]; then
  working_directory="$INPUT_WORKING_DIRECTORY"
else
  working_directory="$workspace/${INPUT_WORKING_DIRECTORY:-.}"
fi
cd "$working_directory"
mkdir -p "$INPUT_OUTPUT_DIR" "$(dirname "$INPUT_REPORT_PATH")"

args=(
  --openapi "$INPUT_OPENAPI_PATH"
  --target "$INPUT_TARGET_URL"
  --duration "$INPUT_DURATION"
  --concurrency "$INPUT_CONCURRENCY"
  --rps "$INPUT_RPS"
  --request-timeout "$INPUT_REQUEST_TIMEOUT"
  --cpu-duration "$INPUT_CPU_DURATION"
  --profile-types "$INPUT_PROFILE_TYPES"
  --output "$INPUT_OUTPUT_DIR"
  --report "$INPUT_REPORT_PATH"
  --top-functions "$INPUT_TOP_FUNCTIONS"
  --no-progress
)

if [[ -n "$INPUT_PPROF_TARGET" ]]; then
  args+=(--pprof-target "$INPUT_PPROF_TARGET")
fi
if [[ -n "$INPUT_FAIL_ON" ]]; then
  args+=(--fail-on "$INPUT_FAIL_ON")
fi
if [[ -n "$INPUT_BASELINE_REPORT" ]]; then
  args+=(--baseline "$INPUT_BASELINE_REPORT")
fi
if [[ -n "$INPUT_FAIL_ON_REGRESSION" ]]; then
  args+=(--fail-on-regression "$INPUT_FAIL_ON_REGRESSION")
fi
if [[ -n "$INPUT_LABEL" ]]; then
  args+=(--label "$INPUT_LABEL")
fi

set +e
"$binary" "${args[@]}"
exit_code=$?
set -e

absolute_output_dir="$(cd "$INPUT_OUTPUT_DIR" && pwd)"
report_directory="$(dirname "$INPUT_REPORT_PATH")"
report_name="$(basename "$INPUT_REPORT_PATH")"
absolute_report_path="$(cd "$report_directory" && pwd)/$report_name"
cpu_profile="$(find "$absolute_output_dir" -maxdepth 1 -type f -name 'cpu_*.pprof' -print | sort | head -n 1)"
heap_profile="$(find "$absolute_output_dir" -maxdepth 1 -type f -name 'heap_*.pprof' -print | sort | head -n 1)"
block_profile="$(find "$absolute_output_dir" -maxdepth 1 -type f -name 'block_*.pprof' -print | sort | head -n 1)"
goroutine_profile="$(find "$absolute_output_dir" -maxdepth 1 -type f -name 'goroutine_*.pprof' -print | sort | head -n 1)"

{
  echo "output-dir=$absolute_output_dir"
  echo "report-path=$absolute_report_path"
  echo "exit-code=$exit_code"
  echo "cpu-profile-path=$cpu_profile"
  echo "heap-profile-path=$heap_profile"
  echo "block-profile-path=$block_profile"
  echo "goroutine-profile-path=$goroutine_profile"
} >> "$GITHUB_OUTPUT"

exit "$exit_code"
