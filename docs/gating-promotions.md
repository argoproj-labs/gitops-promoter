# Gating Promotions

Most environment promotion strategies will involve enforcing some kind of "gates" between promotions.

GitOps promoter uses the [PromotionStrategy API](crd-specs.md#promotionstrategy) to configure checks that must pass
between environments. It uses the [CommitStatus API](crd-specs.md#commitstatus) to understand the state of the checks.

A "proposed commit status" is a check which must be passing on a proposed change before it can be merged. To set a 
CommitStatus to be used as a proposed commit status, set the `spec.sha` field to the commit hash of the proposed change
in the proposed (`-next`) environment branch.

An "active commit status" is a check which must be passing on an active (already merged) change before the change can be
merged for the next environment. To set a CommitStatus to be used as an active commit status, set the `spec.sha` field 
to the commit hash of the active change in the live environment branch.

## Example

The following example demonstrates how to configure a PromotionStrategy to use CommitStatuses for both a proposed and
an active commit status check.

```yaml
kind: PromotionStrategy
spec:
  activeCommitStatuses:
    - key: healthy
  environments:
    - branch: env/dev
    - branch: env/stg
    - branch: env/prd
      proposedCommitStatuses:
        - key: deployment-freeze
```

In this example, the PromotionStrategy has three environments: `env/dev`, `env/stg`, and `env/prd`. All environments
have a `healthy` active commit status check. The `env/prd` environment has an additional `deployment-freeze` proposed
commit status check.

Suppose the environment branches have been hydrated from the `main` branch and that the branches have the following
commit SHAs:

| Branch         | SHA      |
|----------------|----------|
| `main`         | `b5d8f7` |
| `env/dev`      | `a1b2c3` |
| `env/dev-next` | `d4e5f6` |
| `env/stg`      | `a7b8c9` |
| `env/stg-next` | `d0e1f2` |
| `env/prd`      | `a3b4c5` |
| `env/prd-next` | `d6e7f8` |

For a change to be promoted through all environments, the following CommitStatuses must exist:

```yaml
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: healthy
spec:
  sha: a1b2c3  # env/dev
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: healthy
spec:
  sha: a7b8c9  # env/stg
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: healthy
spec:
  sha: a3b4c5  # env/prd
  phase: success
---
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: deployment-freeze
spec:
  sha: d6e7f8  # env/prd-next
  phase: success
```

Note that all the active commit statuses have SHAs corresponding to the active environment branches, and the proposed
commit status has a SHA corresponding to the proposed (`-next`) environment branch.

Any tool wanting to gate an active commit status must create and update CommitStatuses with the appropriate SHAs for 
the respective environments' live environment branches.

Any tool wanting to gate a proposed commit status must create and update CommitStatuses with the appropriate SHAs for
the respective environments' proposed (`-next`) environment branches.

### How Active Commit Statuses Work (Implementation Details)

The PromotionStrategy controller will create a ChangeTransferPolicy for each environment. The ChangeTransferPolicy 
controller does not actually "look back" at previous environments to enforce active commit status checks. Instead, the
PromotionStrategy controller will inject a `proposedCommitStatus` to represent the active status of the previous
environment. The PromotionStrategy controller will also create and maintain a `CommitStatus` for each non-zero-index
environment, based on the aggregate active commit status check of the previous environment.

So for the above example, the stg environment's ChangeTransferPolicy CR will look like this:

```yaml
kind: ChangeTransferPolicy
spec:
  sourceBranch: env/stg-next
  targetBranch: env/stg
  activeCommitStatuses:
    # The controller will monitor this CommitStatus for the active commit SHA, but it will not enforce it. The status 
    # will be stored on the 
    - key: healthy
  proposedCommitStatuses:
    - key: healthy
    - key: promoter-previous-environment
```

Assuming the `env/dev` environment has a `healthy` active commit status check, the `promoter-previous-environment`
CommitStatus will look like this:

```yaml
kind: CommitStatus
metadata:
  labels:
    promoter.argoproj.io/commit-status: promoter-previous-environment
spec:
  sha: d0e1f2  # env/stg-next
  phase: success
```

Even though the CommitStatus is "about" the `env/dev` branch, the SHA is the SHA of the `env/stg-next` branch. This is
how the PromotionStrategy controller expresses its opinion of the proposed commit on the stg environment, i.e. that it
is acceptable because the previous environment is healthy.