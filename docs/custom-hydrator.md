# Building a Custom Hydrator

GitOps Promoter is designed to work with any hydration system that follows a simple contract. This page documents the
requirements for building a custom hydrator that integrates with GitOps Promoter.

## Overview

A hydrator is a tool that watches a "DRY" (Don't Repeat Yourself) branch for new commits and transforms them into
environment-specific "hydrated" commits on proposed branches. GitOps Promoter then handles promoting these hydrated
commits through your environments via Pull Requests.

## The Contract

Your hydrator must fulfill these requirements:

### 1. Watch the DRY Branch

Monitor the configured DRY branch for new commits. When a new commit arrives, trigger hydration for each environment.

### 2. Push to Proposed Branches

For each environment, push the hydrated content to the corresponding proposed branch. The proposed branch name must be
the environment's active branch name with a `-next` suffix.

| Active Branch | Proposed Branch |
|---------------|-----------------|
| `environment/development` | `environment/development-next` |
| `environment/staging` | `environment/staging-next` |
| `environment/production` | `environment/production-next` |

> **Important**: The `-next` suffix convention is hard-coded in GitOps Promoter and cannot be changed.

### 3. Include `hydrator.metadata` File

Each hydrated commit **must** include a `hydrator.metadata` file at the root of the repository. This JSON file tells
GitOps Promoter which DRY commit was used to produce the hydrated content.

#### Required Fields

```json
{
  "drySha": "abc123def456789..."
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `drySha` | string | **Yes** | The full SHA of the DRY branch commit that was hydrated |

#### Optional Fields

The following fields are optional but recommended for a better user experience in the GitOps Promoter UI:

```json
{
  "drySha": "abc123def456789...",
  "repoURL": "https://github.com/org/repo",
  "author": "Jane Developer <jane@example.com>",
  "date": "2024-01-15T10:30:00Z",
  "subject": "feat: add new feature",
  "body": "This commit adds a new feature that...\n\nSigned-off-by: Jane Developer",
  "references": [
    {
      "commit": {
        "sha": "def789abc123...",
        "repoURL": "https://github.com/org/other-repo",
        "author": "John Developer <john@example.com>",
        "date": "2024-01-14T09:00:00Z",
        "subject": "chore: update dependency"
      }
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `repoURL` | string | URL of the DRY repository (used for creating links in the UI) |
| `author` | string | Author of the DRY commit in git format (`Name <email>`) |
| `date` | string | ISO 8601 timestamp of the DRY commit |
| `subject` | string | Subject line of the DRY commit message |
| `body` | string | Body of the DRY commit message (excluding subject) |
| `references` | array | Additional commits that contributed to this hydration (e.g., from other repositories) |

### 4. Duplicate Detection (Recommended)

To avoid unnecessary commits when manifests haven't changed, your hydrator should detect when the hydrated output
is identical to what's already on the proposed branch. If nothing has changed, don't push a new commit.

This prevents GitOps Promoter from creating Pull Requests for changes that have no effect.

## Example Implementations

### Minimal Example

A minimal hydrator that copies files and adds metadata:

```bash
#!/bin/bash
DRY_BRANCH="main"
ENVIRONMENTS=("development" "staging" "production")

# Get the latest DRY commit
git checkout $DRY_BRANCH
git pull origin $DRY_BRANCH
DRY_SHA=$(git rev-parse HEAD)

for ENV in "${ENVIRONMENTS[@]}"; do
  PROPOSED_BRANCH="environment/${ENV}-next"
  
  # Checkout proposed branch
  git checkout $PROPOSED_BRANCH || git checkout -b $PROPOSED_BRANCH
  
  # Copy/transform files (your hydration logic here)
  cp -r manifests/${ENV}/* .
  
  # Create hydrator.metadata
  cat > hydrator.metadata << EOF
{
  "drySha": "${DRY_SHA}",
  "repoURL": "https://github.com/org/repo",
  "author": "$(git show -s --format='%an <%ae>' ${DRY_SHA})",
  "date": "$(git show -s --format='%aI' ${DRY_SHA})",
  "subject": "$(git show -s --format='%s' ${DRY_SHA})"
}
EOF
  
  # Commit and push
  git add -A
  git commit -m "Hydrate from ${DRY_SHA}"
  git push origin $PROPOSED_BRANCH
done
```

### With Helm Template

```bash
#!/bin/bash
DRY_SHA=$(git rev-parse HEAD)
ENV=$1  # e.g., "production"

# Render Helm chart with environment-specific values
helm template my-app ./chart \
  --values ./chart/values-${ENV}.yaml \
  --output-dir ./rendered

# Move rendered manifests to root
mv ./rendered/my-app/templates/* .
rm -rf ./rendered

# Create metadata file
cat > hydrator.metadata << EOF
{
  "drySha": "${DRY_SHA}",
  "repoURL": "https://github.com/org/repo"
}
EOF
```

### With Kustomize

```bash
#!/bin/bash
DRY_SHA=$(git rev-parse HEAD)
ENV=$1  # e.g., "staging"

# Build with kustomize
kustomize build ./overlays/${ENV} > manifests.yaml

# Create metadata file
cat > hydrator.metadata << EOF
{
  "drySha": "${DRY_SHA}",
  "repoURL": "https://github.com/org/repo"
}
EOF
```

### Advanced: With Git Notes

This example shows a complete hydrator that uses git notes to optimize hydration:

1. First, check the git note on the current proposed branch commit - if the `drySha` matches, skip entirely (saves rendering time)
2. If no match, render manifests and compare against what's on the proposed branch
3. If manifests changed: create a new commit with `hydrator.metadata` and a git note
4. If manifests are identical: only update the git note (no new commit needed)

```bash
#!/bin/bash
set -e

DRY_SHA=$(git rev-parse HEAD)
ENV=$1  # e.g., "production"
PROPOSED_BRANCH="environment/${ENV}-next"
REPO_URL="https://github.com/org/repo"
NOTES_REF="refs/notes/hydrator.metadata"

# Fetch the proposed branch and notes
git fetch origin ${PROPOSED_BRANCH} 2>/dev/null || {
  echo "Proposed branch doesn't exist yet, will create it"
  BRANCH_EXISTS=false
}
BRANCH_EXISTS=${BRANCH_EXISTS:-true}

if [ "${BRANCH_EXISTS}" = "true" ]; then
  # Get the current hydrated commit SHA
  HYDRATED_SHA=$(git rev-parse origin/${PROPOSED_BRANCH})
  
  # Fetch and check the git note - if drySha matches, we can skip entirely
  git fetch origin ${NOTES_REF}:${NOTES_REF} 2>/dev/null || true
  EXISTING_NOTE=$(git notes --ref=${NOTES_REF} show ${HYDRATED_SHA} 2>/dev/null || echo "{}")
  EXISTING_DRY_SHA=$(echo "${EXISTING_NOTE}" | jq -r '.drySha // ""')
  
  if [ "${EXISTING_DRY_SHA}" = "${DRY_SHA}" ]; then
    echo "Already hydrated ${ENV} from ${DRY_SHA:0:7}, skipping"
    exit 0
  fi
fi

#
# Note didn't match - need to render and check for changes
#
echo "Rendering manifests for ${ENV} from ${DRY_SHA:0:7}"

# Render manifests
NEW_MANIFESTS=$(mktemp)
kustomize build ./overlays/${ENV} > ${NEW_MANIFESTS}
# Or for Helm:
# helm template my-app ./chart --values ./chart/values-${ENV}.yaml > ${NEW_MANIFESTS}

# Get current manifests from proposed branch for comparison
CURRENT_MANIFESTS=$(mktemp)
if [ "${BRANCH_EXISTS}" = "true" ]; then
  git show origin/${PROPOSED_BRANCH}:manifests.yaml > ${CURRENT_MANIFESTS} 2>/dev/null || true
fi

# Compare rendered output
if [ "${BRANCH_EXISTS}" = "true" ] && diff -q ${NEW_MANIFESTS} ${CURRENT_MANIFESTS} > /dev/null 2>&1; then
  #
  # No changes to manifests - just update the git note
  #
  echo "No manifest changes for ${ENV}, updating git note only"
  
  git notes --ref=${NOTES_REF} add -f -m "{\"drySha\": \"${DRY_SHA}\"}" ${HYDRATED_SHA}
  git push origin ${NOTES_REF}
  
  echo "Updated git note on ${HYDRATED_SHA} with drySha ${DRY_SHA}"
else
  #
  # Manifests changed - create new commit with metadata and note
  #
  echo "Manifests changed for ${ENV}, creating new hydrated commit"
  
  # Checkout proposed branch
  git checkout ${PROPOSED_BRANCH} 2>/dev/null || \
    git checkout -b ${PROPOSED_BRANCH} origin/${PROPOSED_BRANCH} 2>/dev/null || \
    git checkout --orphan ${PROPOSED_BRANCH}
  
  # Clear existing files and copy new manifests
  git rm -rf . 2>/dev/null || true
  cp ${NEW_MANIFESTS} manifests.yaml
  
  # Create hydrator.metadata with full commit info
  cat > hydrator.metadata << EOF
{
  "drySha": "${DRY_SHA}",
  "repoURL": "${REPO_URL}",
  "author": "$(git show -s --format='%an <%ae>' ${DRY_SHA})",
  "date": "$(git show -s --format='%aI' ${DRY_SHA})",
  "subject": $(git show -s --format='%s' ${DRY_SHA} | jq -Rs .),
  "body": $(git show -s --format='%b' ${DRY_SHA} | jq -Rs .)
}
EOF
  
  # Commit
  git add -A
  git commit -m "Hydrate ${ENV} from ${DRY_SHA:0:7}"
  
  HYDRATED_SHA=$(git rev-parse HEAD)
  
  # Add git note to the new commit
  git notes --ref=${NOTES_REF} add -f -m "{\"drySha\": \"${DRY_SHA}\"}" ${HYDRATED_SHA}
  
  # Push branch and notes together
  git push origin ${PROPOSED_BRANCH} ${NOTES_REF}
  
  echo "Created hydrated commit ${HYDRATED_SHA}"
fi

rm -f ${NEW_MANIFESTS} ${CURRENT_MANIFESTS}
```

## Existing Hydrators

### Argo CD Source Hydrator

The [Argo CD Source Hydrator](https://argo-cd.readthedocs.io/en/stable/user-guide/source-hydrator/) is a built-in
feature of Argo CD that implements this contract. It supports Helm, Kustomize, and other Argo CD-supported config
management tools.

See the [Argo CD tutorial](tutorial-argocd-apps.md) for an example of using Argo CD's Source Hydrator with GitOps
Promoter.

## Best Practices

1. **Idempotency**: Running hydration multiple times with the same input should produce the same output.

2. **Atomic Commits**: Each hydrated commit should represent a complete, valid state. Don't push partial changes.

3. **Meaningful Commit Messages**: Include the DRY SHA in your hydrated commit messages for traceability.

