# External Merge Delegation via Signals

GitOps Promoter normally merges pull requests directly via the SCM API when all promotion
conditions are met. This page describes an alternative mode where GitOps Promoter **delegates
the actual merge** to an external system — such as [Prow](https://docs.prow.k8s.io/) or
[Tide](https://docs.prow.k8s.io/docs/components/core/tide/) — by posting comments and/or
labels to the PR.

## When to Use This

Use merge signal delegation when:

- Your repository is governed by a merge bot (e.g. Prow/Tide on Kubernetes or OpenShift
  infrastructure) that requires specific comments like `/lgtm` and `/approve` before merging.
- Granting the GitOps Promoter service account direct merge permissions conflicts with your
  organization's security policy or branch-protection rules.

When signals are configured, GitOps Promoter acts as the **gate keeper** (posting/retracting
signals based on commit-status outcomes) while the **merge bot** retains authority over the
final merge action.

## Configuration

Add `mergeComments` and/or `mergeLabels` to your `ChangeTransferPolicy`:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ChangeTransferPolicy
metadata:
  name: my-policy
spec:
  autoMerge: true
  mergeComments:
    - /lgtm
    - /approve
  mergeLabels:
    - lgtm
    - approved
  proposedCommitStatuses:
    - key: ci-passed
  # ... other fields
```

### Fields

| Field | Type | Description |
|---|---|---|
| `mergeComments` | `[]string` | Comments to post on the PR when all promotion conditions are met. Each string is posted as a separate PR comment. Requires `autoMerge: true`. |
| `mergeLabels` | `[]string` | Labels to add to the PR when all promotion conditions are met. Requires `autoMerge: true`. |

Both fields may be combined. Either field alone is sufficient to enable signal delegation.

## Behavior

### When conditions are met

When `autoMerge: true` and all configured `proposedCommitStatuses` report `success`:

1. GitOps Promoter posts each comment in `mergeComments` to the PR (once — duplicates are skipped).
2. GitOps Promoter applies each label in `mergeLabels` to the PR (idempotent).
3. The SCM comment IDs and applied labels are tracked in `PullRequest.status` for later retraction.
4. **No direct merge is performed.** The PR remains open for the external bot to merge.

### When conditions regress

If a commit status later transitions away from `success` (or `autoMerge` is disabled):

1. GitOps Promoter deletes previously posted comments.
2. GitOps Promoter removes previously applied labels.
3. Status tracking fields are cleared.

This prevents the external bot from merging a PR whose CI has since failed.

### After the bot merges

When the external bot merges the PR, the PR is closed by the SCM. GitOps Promoter detects
this via its existing `ExternallyMergedOrClosed` mechanism and proceeds with the promotion
flow as normal.

## Limitations

- Merge signal delegation is currently supported for **GitHub only**. Configuring
  `mergeComments` or `mergeLabels` on a GitLab, Gitea, Forgejo, Bitbucket, or Azure DevOps
  repository will cause an error during signal posting.
- Comment-level idempotency relies on status persistence: if the controller restarts between
  posting a comment and writing its ID to status, a duplicate comment may be posted on the
  next reconcile. This is inherent to level-driven controllers and is a known limitation.

## Example: Prow/Tide integration

For a Kubernetes-style repository where Tide merges PRs with the `lgtm` and `approved` labels:

```yaml
spec:
  autoMerge: true
  mergeLabels:
    - lgtm
    - approved
  proposedCommitStatuses:
    - key: ci/prow/e2e
    - key: ci/prow/unit
```

For a Prow-based repository that responds to bot commands:

```yaml
spec:
  autoMerge: true
  mergeComments:
    - /lgtm
    - /approve
  proposedCommitStatuses:
    - key: tide/merge-blocker
```
