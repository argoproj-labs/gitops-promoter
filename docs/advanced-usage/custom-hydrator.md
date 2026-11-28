# Building a Custom Hydrator

GitOps Promoter is designed to work with any hydration system that follows a simple contract. This page documents the
requirements for building a custom hydrator that integrates with GitOps Promoter.

Using a custom hydrator makes sense if you don't want to use Argo CD's Source Hydrator feature (for example, if you use
a different GitOps operator) or if you are promoting things other than Kubernetes manifests.

## Overview

A hydrator is a tool that watches a "DRY" (Don't Repeat Yourself) branch for new commits and transforms them into
environment-specific "hydrated" commits on proposed branches. GitOps Promoter then handles promoting these hydrated
commits through your environments via Pull Requests.

## The Contract

Your hydrator must fulfill these requirements:

### 1. Watch the DRY Branch

Monitor the configured DRY branch for new commits. When a new commit arrives, trigger hydration for each environment.

If the PromotionStrategy is ordered by a
[ValidatedCommitStatus](../gating-promotions/built-in-gates/validated-commit-status.md), each environment is hydrated
from its own git tag instead: `<active-branch>-tag`, or `<active-branch>-tag/<activePath>` with `activePath`. Hydrate
an environment when its tag is pushed, from the commit the tag points at, and record that commit as the DRY SHA. Do
not hydrate an environment whose tag does not exist yet. The rest of the contract is the same.

### 2. Push to Proposed Branches

For each environment, push the hydrated content to the corresponding proposed branch. The proposed branch name must be
the environment's active branch name with a `-next` suffix.

| Active Branch | Proposed Branch |
|---------------|-----------------|
| `environment/development` | `environment/development-next` |
| `environment/staging` | `environment/staging-next` |
| `environment/production` | `environment/production-next` |

> **Important**: The `-next` suffix convention is hard-coded in GitOps Promoter and cannot be changed.

When using the monorepo shared-active-branch pattern (`PromotionStrategy.spec.activePath`), proposed branches are built
as `<active-branch>-next/<activePath>`.

| Active Branch | Active Path | Proposed Branch |
|---------------|-------------|-----------------|
| `environment/development` | `app-a` | `environment/development-next/app-a` |
| `environment/staging` | `app-a` | `environment/staging-next/app-a` |

### 3. Include `hydrator.metadata` File

Each hydrated commit **must** include a `hydrator.metadata` file. This JSON file tells GitOps Promoter which DRY
commit was used to produce the hydrated content.

- Default mode (no `activePath`): put `hydrator.metadata` at the repository root.
- Shared-active-branch mode (`activePath` set): put `hydrator.metadata` at `<activePath>/hydrator.metadata`.
  GitOps Promoter reads only that path; a repository-root `hydrator.metadata` is optional and is not used for
  promotion.

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

### 5. Preserve Other Application Directories (Shared Active Branch Mode)

If you use `activePath` to share one active branch across multiple applications, hydration must be path-scoped:

1. Update only files for the current app path.
2. Do not delete or rewrite other applications' directories on the same branch.

> [!NOTE]
> Multiple `PromotionStrategy` resources on the same active branch must follow the constraints in
> [repository structure](../structuring-your-repos.md#constraints-when-multiple-promotionstrategies-share-an-active-branch).

This ensures independent PromotionStrategies can safely share the same active branch.

### 6. Re-hydrating a DRY Commit (Allowed, Not Promoted)

A hydrator may push a new hydrated commit whose `hydrator.metadata` has the same `drySha` as the commit before it. This
happens when the rendered output changes even though the DRY branch hasn't, for example after upgrading the hydrator or
its config management tools, or when a remote Helm chart or Kustomize base used by the DRY commit changes. Starting in
Argo CD 3.7, the Argo CD Source Hydrator does this.

After a re-hydration, later DRY commits that don't change that environment's manifests advance only the git note
(see [Duplicate Detection](#4-duplicate-detection-recommended)). `hydrator.metadata` keeps the re-hydrated `drySha`.

GitOps Promoter accepts this behavior. It does not promote the re-hydration by itself:

- **No PR is opened.** GitOps Promoter opens a promotion PR only when the `drySha` in `hydrator.metadata` on the
  proposed branch differs from the one on the active branch. Re-hydrating the DRY commit that is already active doesn't
  change the `drySha`, so it doesn't open a PR.
- **Note-only advances don't open a PR either.** The git note tells GitOps Promoter that the hydrator has processed a
  DRY commit, which unblocks downstream environments that wait on hydration. It never starts a promotion.
- **The re-hydrated manifests wait on the proposed branch.** They don't count as a pending promotion, so they don't block
  downstream environments.
- **The next manifest-changing DRY commit promotes them.** Once a DRY commit changes the environment's manifests, the
  hydrator writes a new `drySha`, GitOps Promoter opens a PR, and that PR includes everything on the proposed branch,
  including the re-hydrated manifests.
- **An open PR includes a re-hydration.** If the re-hydrated DRY commit is already in an open, unmerged PR, the PR picks
  up the new hydrated commit and the merge includes it.

Promotions follow the `drySha` because the DRY commit is the unit GitOps Promoter promotes and gates on. PR contents,
commit statuses, promotion history, and environment ordering all refer to it. A re-hydration has no new DRY commit. If
GitOps Promoter promoted it under the old `drySha`, every environment would already appear to have that commit, so the
ordering gates would pass immediately and the re-rendered manifests would reach all environments at the same time
instead of one environment after another.

#### Releasing Re-hydrated Manifests

To release re-hydrated manifests without waiting for an unrelated change, push a DRY commit that changes the rendered
manifests of each environment you want to release. Each environment then gets a hydrated commit with a new `drySha`,
and the change moves through your environments in the usual order.

With Kustomize, you can add or bump a label in a shared base:

```yaml
# base/kustomization.yaml
labels:
  - pairs:
      example.com/rehydrated: "2026-11-24"
```

> [!WARNING]
> Prefer `labels` (without `includeSelectors`) over `commonLabels`. `commonLabels` also changes label selectors, and
> selectors are immutable on resources such as Deployments, so changing them can make syncs fail.

A DRY commit that changes only some environments' manifests releases only those environments.

## Example Implementations

These example scripts would run on every push to the DRY branch. They could run anywhere, but a common choice would be
a CI system like GitHub Actions, since it can easily trigger on pushes and has built-in git support.

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

push_note_with_retry() {
  local commit_sha=$1
  local note_content="{\"drySha\": \"${DRY_SHA}\"}"

  for attempt in 1 2 3 4 5 6 7 8; do
    git fetch origin +${NOTES_REF}:${NOTES_REF} 2>/dev/null || true
    git notes --ref=${NOTES_REF} add -f -m "${note_content}" ${commit_sha}
    if git push origin ${NOTES_REF}; then
      return 0
    fi
    sleep 0.1
  done

  echo "Failed to push git note after retries" >&2
  return 1
}

if [ "${BRANCH_EXISTS}" = "true" ]; then
  # Get the current hydrated commit SHA
  HYDRATED_SHA=$(git rev-parse origin/${PROPOSED_BRANCH})
  
  # Fetch and check the git note - if drySha matches, we can skip entirely
  git fetch origin +${NOTES_REF}:${NOTES_REF} 2>/dev/null || true
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
  
  push_note_with_retry ${HYDRATED_SHA}
  
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

  # Push branch, then update notes ref with retry for concurrent writers
  git push origin ${PROPOSED_BRANCH}
  push_note_with_retry ${HYDRATED_SHA}
  
  echo "Created hydrated commit ${HYDRATED_SHA}"
fi

rm -f ${NEW_MANIFESTS} ${CURRENT_MANIFESTS}
```

## Existing Hydrators

### Argo CD Source Hydrator

The [Argo CD Source Hydrator](https://argo-cd.readthedocs.io/en/stable/user-guide/source-hydrator/) is a built-in
feature of Argo CD that implements this contract. It supports Helm, Kustomize, and other Argo CD-supported config
management tools.

See the [Argo CD tutorial](../integrating-with-argocd/tutorial.md) for an example of using Argo CD's Source Hydrator with GitOps
Promoter.

## Best Practices

1. **Idempotency**: Running hydration multiple times with the same input should produce the same output.

2. **Atomic Commits**: Each hydrated commit should represent a complete, valid state. Don't push partial changes.

3. **Meaningful Commit Messages**: Include the DRY SHA in your hydrated commit messages for traceability.
