#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: build-assets.sh <vMAJOR.MINOR.PATCH> [output-dir]}"
output_dir="${2:-dist}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Version must use vMAJOR.MINOR.PATCH format" >&2
  exit 1
fi

mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"

checksums="$output_dir/checksums.txt"
if [[ -e "$checksums" ]]; then
  echo "Refusing to overwrite existing checksums: $checksums" >&2
  exit 1
fi

assets=()
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  asset="proficiency_${version}_${os}_${arch}.tar.gz"
  archive="$output_dir/$asset"
  binary="$output_dir/proficiency"

  if [[ -e "$archive" ]]; then
    echo "Refusing to overwrite existing asset: $archive" >&2
    exit 1
  fi

  (
    cd "$repo_root"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -ldflags="-s -w -X main.Version=${version}" \
      -o "$binary" ./cmd/proficiency
  )
  tar -C "$output_dir" -czf "$archive" proficiency
  rm "$binary"
  assets+=("$asset")
done

(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${assets[@]}" > checksums.txt
  else
    shasum -a 256 "${assets[@]}" > checksums.txt
  fi
)

printf 'Built %d release archives in %s\n' "${#assets[@]}" "$output_dir"
