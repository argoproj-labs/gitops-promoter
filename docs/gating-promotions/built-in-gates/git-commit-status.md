## Overview

The GitCommitStatus controller evaluates custom expressions against commit data and automatically creates CommitStatus resources that can be used as gates in your PromotionStrategy.

### How It Works

For each environment configured in a GitCommitStatus resource:

1. The controller reads the PromotionStrategy to get commit SHAs
2. The controller selects which commit to validate based on the `target` field:
   - `active` (default): Validates the currently deployed commit
   - `proposed`: Validates the commit that will be promoted
3. The controller fetches commit data (message, author, committer, trailers) from git
4. If `verification` is configured, the controller verifies the signature of every commit the promotion would add against the configured trusted keys
5. The controller evaluates your custom expression against the commit data
6. The controller creates/updates a CommitStatus with the result, always attached to the **proposed** SHA for promotion gating
7. The PromotionStrategy checks the CommitStatus before allowing promotion

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

## Verifying Commit Signatures

Set `spec.verification` to check commit signatures against a fixed set of trusted GPG public keys. The result is exposed to the expression as `Verification`, a top-level variable alongside `Commit`, so signature state can be combined with the rest of the commit data in a single rule.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitCommitStatus
metadata:
  name: signed-by-trusted-key
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  key: signature-check
  description: "Require a signature from a trusted key"
  target: proposed
  expression: 'Verification.Verified'
  verification:
    gpg:
      publicKeys:
        - armored: |
            -----BEGIN PGP PUBLIC KEY BLOCK-----
            mDMEZ1exampleKeyMaterial...
            -----END PGP PUBLIC KEY BLOCK-----
```

Export a key in the expected format with:

```bash
gpg --armor --export <fingerprint>
```

Only public key material goes in the resource, so a GitCommitStatus with `verification` is safe to keep in git alongside your other manifests.

### Trust Model

`publicKeys` is the complete trust anchor for this check. The controller builds an ephemeral, offline keyring per reconcile containing exactly those keys, and removes it afterwards — it never contacts a keyserver or reads the node's keyring. A commit signed by any key outside the list is reported as **unverified**, as are commits with an expired, revoked, or malformed signature, and unsigned commits.

`Verified` is the only field you should gate on. On any commit that did not verify, every other field is empty, because git reports the issuer a signature *claims* even when there is no key to check that claim against — the key ID on an unverified signature is chosen by whoever produced it.

### Which Commits Are Verified

Verification covers **every commit the promotion would add**, which is the range between the active branch's commit and the proposed branch's commit. A promotion moves the whole range, so verifying only its newest commit would let an unsigned commit ride in underneath a signed one.

This is independent of `spec.target`. `target` selects whose commit message the rest of the expression reads; it does not narrow which commits are verified. So whether `Commit` is one of `Verification.Commits` depends on `target`: with `target: proposed` it is the newest entry, and with `target: active` it is the commit the range starts *after*, so it is not in the list at all.

Two boundary cases are worth knowing:

- A promotion that adds no commits has nothing unsigned in it, so `Verified` is `true` and `Commits` is empty. Assert on `len(Verification.Commits)` if you want a rule that only passes on a non-empty promotion.
- An environment that has never been promoted has no previously verified boundary to walk back to, so only the proposed branch's commit is verified. Walking to the repository root instead would verify the entire history on first use.

If a commit cannot be found in the repository even after fetching its branch, the whole range is reported as unverified rather than retried indefinitely, so the gate fails closed until a new SHA appears.

### Available Signature Fields

- `Verification.Verified` (bool): Every commit in the range was signed by one of the configured keys
- `Verification.Commits` (list): One entry per commit in the range, newest first, each with:
    - `SHA` (string): The commit SHA
    - `Verified` (bool): This commit was signed by one of the configured keys
    - `Type` (string): Signature type, currently always `gpg`
    - `KeyID` (string): The signing key ID
    - `Signer` (string): The signer identity as recorded on the key (name and email)

Combine them with the other commit fields to require that every commit carries a trusted signature from a specific signer:

```yaml
expression: 'Verification.Verified && all(Verification.Commits, .Signer endsWith "<release-bot@example.com>")'
```

> **Important:** `Verification` is `nil` when `spec.verification` is not set, and referencing fields on it will fail the expression evaluation. Only reference `Verification` in resources that configure `verification`.

### Requirements

Signature verification is the only part of GitCommitStatus that touches git directly. When it is configured, the controller clones the PromotionStrategy's repository, so the referenced `GitRepository`, `ScmProvider`/`ClusterScmProvider`, and its credentials Secret must be resolvable from the GitCommitStatus's namespace — the same access the promotion controllers already need for that repository. Verification also requires the `gpg` binary, which is present in the shipped controller image.

Without `verification`, no clone happens and the controller keeps reading commit data from the PromotionStrategy status only.

## Expression Language

GitCommitStatus uses the [expr](https://github.com/expr-lang/expr) library for expression evaluation. Expressions must return a boolean value where `true` indicates validation passed.

### Available Variables

Each expression has access to two top-level variables.

`Commit` is the single commit selected by `spec.target`:

- `Commit.SHA` (string): The commit SHA being validated
- `Commit.Subject` (string): The first line of the commit message
- `Commit.Body` (string): The commit message body (everything after the subject line)
- `Commit.Author` (string): Commit author email address
- `Commit.Trailers` (map[string][]string): Git trailers parsed from commit message

`Verification` covers **all** the commits the promotion would add, not just the one `spec.target` selects. It is `nil` unless [`spec.verification`](#verifying-commit-signatures) is configured. See [Available Signature Fields](#available-signature-fields).

## Field Reference

### spec.target

Controls which commit SHA is validated by the expression.

**Values:**
- `active` (default): Validates the currently deployed commit
- `proposed`: Validates the commit that will be promoted

**Default:** `active`

The validation result is always reported on the proposed commit for promotion gating, regardless of which commit was validated.

`target` does not affect [signature verification](#which-commits-are-verified), which always covers every commit the promotion would add.

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

### spec.verification

Enables signature verification for every commit the promotion would add and exposes `Verification` to the expression. See [Verifying Commit Signatures](#verifying-commit-signatures).

**Optional**

- `verification.gpg` — required when `verification` is set
- `verification.gpg.publicKeys` — at least one entry; the complete set of keys a signature is trusted against
- `verification.gpg.publicKeys[].armored` — a non-empty ASCII-armored public key, as produced by `gpg --armor --export <fingerprint>`

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
