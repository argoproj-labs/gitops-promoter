#!/bin/sh
# hack/bump-docs-manifests.sh
# Usage: ./hack/bump-docs-manifests.sh <new_version>
# Bumps manifest versions in docs/getting-started.md and docs/integrating-with-argocd/

set -e

if [ -z "$1" ]; then
  echo "Usage: $0 <new_version>"
  exit 1
fi

NEW_VERSION="$1"

# Portable sed replacement: write to temp file, then move back
sed_replace() {
  local pattern="$1"
  local file="$2"
  local tmpfile="${file}.tmp.$$"
  sed "$pattern" "$file" > "$tmpfile" && mv "$tmpfile" "$file"
}

# Replace all occurrences of vX.Y.Z with the new version (vNEW_VERSION)
sed_replace "s/v[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}/v$NEW_VERSION/g" docs/getting-started.md
sed_replace "s/v[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}/v$NEW_VERSION/g" docs/integrating-with-argocd/tutorial.md
sed_replace "s/download\/v[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}/download\/v$NEW_VERSION/g" docs/integrating-with-argocd/index.md
sed_replace "s/gitops-promoter_[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}_checksums\.txt/gitops-promoter_${NEW_VERSION}_checksums.txt/g" docs/integrating-with-argocd/index.md
echo "Bumped manifest versions to v$NEW_VERSION in docs."
sed_replace "s/.*/v$NEW_VERSION/" cmd/demo/config/promoter_version
sed_replace "s/download\/v[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}/download\/v$NEW_VERSION/g" cmd/demo/config/argocd-extension.yaml
sed_replace "s/gitops-promoter_[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}_checksums\.txt/gitops-promoter_${NEW_VERSION}_checksums.txt/g" cmd/demo/config/argocd-extension.yaml
sed_replace "s|gitops-promoter/releases/download/v[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}/|gitops-promoter/releases/download/v$NEW_VERSION/|g" cmd/demo/config/config.yaml
echo "Bumped manifest versions to v$NEW_VERSION in demo."

