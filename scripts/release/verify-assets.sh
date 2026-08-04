#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: verify-assets.sh <vMAJOR.MINOR.PATCH> [asset-dir]}"
asset_dir="${2:-dist}"

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Version must use vMAJOR.MINOR.PATCH format" >&2
  exit 1
fi

asset_dir="$(cd "$asset_dir" && pwd)"
expected=(
  "proficiency_${version}_linux_amd64.tar.gz"
  "proficiency_${version}_linux_arm64.tar.gz"
  "proficiency_${version}_darwin_amd64.tar.gz"
  "proficiency_${version}_darwin_arm64.tar.gz"
)

for asset in "${expected[@]}" checksums.txt; do
  if [[ ! -s "$asset_dir/$asset" ]]; then
    echo "Missing or empty release asset: $asset" >&2
    exit 1
  fi
done

(
  cd "$asset_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c checksums.txt
  else
    shasum -a 256 -c checksums.txt
  fi
)

for asset in "${expected[@]}"; do
  if [[ "$(tar -tzf "$asset_dir/$asset")" != "proficiency" ]]; then
    echo "Archive $asset must contain exactly the proficiency binary" >&2
    exit 1
  fi
done

case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) host_asset="proficiency_${version}_darwin_arm64.tar.gz" ;;
  Darwin/x86_64) host_asset="proficiency_${version}_darwin_amd64.tar.gz" ;;
  Linux/aarch64 | Linux/arm64) host_asset="proficiency_${version}_linux_arm64.tar.gz" ;;
  Linux/x86_64 | Linux/amd64) host_asset="proficiency_${version}_linux_amd64.tar.gz" ;;
  *) host_asset="" ;;
esac

if [[ -n "$host_asset" ]]; then
  temp_dir="$(mktemp -d 2>/dev/null || mktemp -d -t proficiency-assets)"
  trap 'rm -rf "$temp_dir"' EXIT
  tar -xzf "$asset_dir/$host_asset" -C "$temp_dir"
  if [[ "$("$temp_dir/proficiency" --version)" != "proficiency version $version" ]]; then
    echo "Host release binary does not report $version" >&2
    exit 1
  fi
fi

echo "Release assets verified for $version"
