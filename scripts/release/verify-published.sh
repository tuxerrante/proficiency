#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: verify-published.sh <vMAJOR.MINOR.PATCH>}"
repo="${GITHUB_REPOSITORY:-tuxerrante/proficiency}"
major="${version%%.*}"

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Version must use vMAJOR.MINOR.PATCH format" >&2
  exit 1
fi

release="$(gh api "repos/${repo}/releases/tags/${version}")"
if [[ "$(jq -r .draft <<<"$release")" != "false" ||
  "$(jq -r '.immutable // false' <<<"$release")" != "true" ||
  "$(jq -r .prerelease <<<"$release")" != "false" ]]; then
  echo "Release $version is not a published immutable stable release" >&2
  exit 1
fi

temp_dir="$(mktemp -d 2>/dev/null || mktemp -d -t proficiency-release)"
trap 'rm -rf "$temp_dir"' EXIT
gh release download "$version" --repo "$repo" --dir "$temp_dir"
"$(dirname "${BASH_SOURCE[0]}")/verify-assets.sh" "$version" "$temp_dir"

install_dir="$temp_dir/install"
mkdir -p "$install_dir"
GOBIN="$install_dir" go install "github.com/tuxerrante/proficiency/cmd/proficiency@${version}"
if [[ "$("$install_dir/proficiency" --version)" != "proficiency version $version" ]]; then
  echo "Tagged go install build does not report $version" >&2
  exit 1
fi

tag_repo="$temp_dir/tags.git"
git init --bare "$tag_repo" >/dev/null
git --git-dir="$tag_repo" fetch --quiet --no-tags "https://github.com/${repo}.git" \
  "+refs/tags/$version:refs/tags/$version" \
  "+refs/tags/$major:refs/tags/$major"
if ! version_commit="$(git --git-dir="$tag_repo" rev-parse "$version^{commit}" 2>/dev/null)"; then
  echo "Immutable release tag $version is missing" >&2
  exit 1
fi
if ! major_commit="$(git --git-dir="$tag_repo" rev-parse "$major^{commit}" 2>/dev/null)"; then
  echo "Movable Action tag $major is missing" >&2
  exit 1
fi
if [[ "$version_commit" != "$major_commit" ]]; then
  echo "$major does not point to $version" >&2
  exit 1
fi

marketplace_url="https://github.com/marketplace/actions/proficiency-go-api-performance"
curl --fail --silent --show-error --location "$marketplace_url" >/dev/null

echo "Published release verified: $(jq -r .html_url <<<"$release")"
echo "Marketplace listing verified: $marketplace_url"
