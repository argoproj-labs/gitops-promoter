## Overview

The GitCommitStatus controller evaluates custom expressions against commit data and automatically creates CommitStatus resources that can be used as gates in your PromotionStrategy.

### How It Works

For each environment configured in a GitCommitStatus resource:

1. The controller reads the PromotionStrategy to get commit SHAs
2. The controller selects which commit to validate based on the `target` field:
   - `active` (default): Validates the currently deployed commit
   - `proposed`: Validates the commit that will be promoted
3. The controller fetches commit data (message, author, committer, trailers) from git
4. The controller evaluates your custom expression against the commit data
5. The controller creates/updates a CommitStatus with the result, always attached to the **proposed** SHA for promotion gating
6. The PromotionStrategy checks the CommitStatus before allowing promotion

### Validation Modes

GitCommitStatus supports two validation modes:

#### Active Mode

Validates the **currently deployed** commit. Use this when you want to validate the current environment state before allowing new changes to be promoted.

**Use cases:**
- "Don't promote if a revert commit is detected in production"
- "Ensure active commit is not missing required sign-offs"
- "Block promotions if active commit violates policy"

#### Proposed Mode

Validates the **incoming** commit that will be promoted. Use this when you want to validate the change itself.

**Use cases:**
- "Don't promote unless new commit follows naming convention"
- "Ensure proposed commit has proper JIRA ticket reference"
- "Require specific author for proposed changes"

## Example Configurations

### Basic Revert Detection (Active Mode)

In this example, we block promotions if the currently active commit is a revert:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitCommitStatus
metadata:
  name: no-revert-in-active
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: revert-check
  description: "Block promotions if active commit is a revert"
  target: active  # Targets currently deployed commit
  expression: '!(Commit.Subject startsWith "Revert" || Commit.Body startsWith "Revert")'
```

### Hydrator Version Check (Proposed Mode)

Ensure the hydration tooling version is the latest approved version before allowing promotions:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitCommitStatus
metadata:
  name: hydrator-version-check
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: hydrator-version
  description: "Verify active hydrator version is the latest"
  target: active  # Targets currently deployed commit
  expression: '"Hydrator-version" in Commit.Trailers && Commit.Trailers["Hydrator-version"][0] == "v2.1.0"'
```

### Integrating with PromotionStrategy

To use GitCommitStatus-based gating, configure your PromotionStrategy to check for the commit status key:

> **Important:** GitCommitStatus must always be configured as a `proposedCommitStatuses` in your PromotionStrategy, regardless of whether it validates the active or proposed commit. This is because the CommitStatus is always reported on the **proposed** commit SHA, which is what gates the promotion.

#### As Proposed Commit Status

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: webservice-tier-1
spec:
  gitRepositoryRef:
    name: webservice-tier-1
  proposedCommitStatuses:
    - key: commit-format  # Must match GitCommitStatus.spec.key
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
```

#### Environment-Specific Validation

You can apply different validations to different environments:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: webservice-tier-1
spec:
  gitRepositoryRef:
    name: webservice-tier-1
  environments:
    - branch: environment/development
    - branch: environment/staging
    - branch: environment/production
      proposedCommitStatuses:
        - key: production-specific-check  # Only for production
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitCommitStatus
metadata:
  name: production-gate
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: production-specific-check
  description: "Extra validation for production"
  target: proposed
  expression: '"Approved-for-production" in Commit.Trailers'
```

## Expression Language

GitCommitStatus uses the [expr](https://github.com/expr-lang/expr) library for expression evaluation. Expressions must return a boolean value where `true` indicates validation passed.

### Available Variables

Each expression has access to a `Commit` object with the following fields:

- `Commit.SHA` (string): The commit SHA being validated
- `Commit.Subject` (string): The first line of the commit message
- `Commit.Body` (string): The commit message body (everything after the subject line)
- `Commit.Author` (string): Commit author email address
- `Commit.Trailers` (map[string][]string): Git trailers parsed from commit message


## Field Reference

### spec.target

Controls which commit SHA is validated by the expression.

**Values:**
- `active` (default): Validates the currently deployed commit
- `proposed`: Validates the commit that will be promoted

**Default:** `active`

The validation result is always reported on the proposed commit for promotion gating, regardless of which commit was validated.

### spec.key

Unique identifier for this validation rule. This key is matched against the PromotionStrategy's `activeCommitStatuses` or `proposedCommitStatuses`.

**Requirements:**
- Must be lowercase alphanumeric with hyphens
- Max 63 characters
- Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`

### spec.description

Human-readable description shown in the SCM provider (GitHub, GitLab, etc.) as the commit status description. Keep this concise.

**Optional**

### spec.expression

Expression evaluated against commit data. Must return boolean.

**Required**

### Status Fields

The GitCommitStatus resource maintains detailed status information:

```yaml
status:
  environments:
    - branch: environment/development
      proposedHydratedSha: abc123def456
      activeHydratedSha: bef859def431
      targetedSha: bef859def431  # Which SHA was actually validated
      phase: success
      expressionResult: true
```

Fields:
- `branch` - The environment branch being validated
- `proposedHydratedSha` - The proposed commit SHA (where status is reported)
- `activeHydratedSha` - The active commit SHA (currently deployed)
- `targetedSha` - The commit SHA that was actually validated
- `phase` - Current validation status (`pending`, `success`, or `failure`)
- `expressionResult` - Boolean result of expression evaluation (nil if failed to evaluate)
