#!/bin/bash
# hack/release-line.sh
# Usage: ./hack/release-line.sh <version>
#
# Validates a stable release version against the local git tags and prints
# shell assignments describing its release line. Accepts X.Y.Z or vX.Y.Z.
# Fetch tags before running this script.

set -euo pipefail

if [[ $# -ne 1 || -z "${1:-}" ]]; then
  echo "Usage: $0 <version>" >&2
  exit 1
fi

version_number=$1
version_number=${version_number#v}
if [[ ! "$version_number" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "::error::Version must have the form X.Y.Z (optional v prefix) without leading zeroes." >&2
  exit 1
fi

IFS=. read -r major minor patch <<< "$version_number"
version="v${version_number}"
release_branch="release-${major}.${minor}"
release_label="cherry-pick/${major}.${minor}"

if git rev-parse --verify --quiet "refs/tags/${version}" >/dev/null; then
  echo "::error::Tag ${version} already exists." >&2
  exit 1
fi

# Git globs also match suffixes such as v1.2.0-rc1, so keep only stable tags.
stable_tags() {
  git tag --list "$1" --sort=-v:refname | grep -E '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || true
}

latest_tag=$(stable_tags 'v[0-9]*.[0-9]*.[0-9]*' | head -n 1)
latest_series_tag=""
if [[ "$patch" == "0" ]]; then
  if [[ -n "$latest_tag" && "$(printf '%s\n' "${latest_tag#v}" "$version_number" | sort -V | tail -n 1)" != "$version_number" ]]; then
    echo "::error::${version} must be newer than the latest tag, ${latest_tag}." >&2
    exit 1
  fi
else
  latest_series_tag=$(stable_tags "v${major}.${minor}.*" | head -n 1)
  if [[ -z "$latest_series_tag" ]]; then
    echo "::error::Cannot patch ${major}.${minor}: no existing v${major}.${minor}.Z release was found." >&2
    exit 1
  fi
  latest_patch=${latest_series_tag##*.}
  if (( patch <= latest_patch )); then
    echo "::error::Patch version ${patch} must be greater than ${latest_patch} (${latest_series_tag})." >&2
    exit 1
  fi
fi

emit() {
  printf '%s=%q\n' "$1" "$2"
}
emit version "$version"
emit version_number "$version_number"
emit major "$major"
emit minor "$minor"
emit patch "$patch"
emit release_branch "$release_branch"
emit release_label "$release_label"
emit latest_tag "$latest_tag"
emit latest_series_tag "$latest_series_tag"
