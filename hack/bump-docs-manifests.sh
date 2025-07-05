#!/bin/sh
# hack/bump-docs-manifests.sh
# Usage: ./hack/bump-docs-manifests.sh <new_version>
# Bumps manifest versions in docs/getting-started.md and docs/tutorial-argocd-apps.md

set -e

if [ -z "$1" ]; then
  echo "Usage: $0 <new_version>"
  exit 1
fi

NEW_VERSION="$1"

# Use platform-independent sed (BSD/macOS and GNU/Linux)
sed_inplace() {
  if sed --version 2>/dev/null | grep -q GNU; then
    sed -i "$@"
  else
    sed -i '' "$@"
  fi
}

# Example: replace all occurrences of vX.Y.Z with the new version (vNEW_VERSION)
sed_inplace "s/v[0-9]\+\.[0-9]\+\.[0-9]\+/v$NEW_VERSION/g" docs/getting-started.md
git add docs/getting-started.md

sed_inplace "s/v[0-9]\+\.[0-9]\+\.[0-9]\+/v$NEW_VERSION/g" docs/tutorial-argocd-apps.md
git add docs/tutorial-argocd-apps.md

echo "Bumped manifest versions to v$NEW_VERSION in docs."

