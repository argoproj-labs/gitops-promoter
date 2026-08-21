# Finalizers

GitOps Promoter uses Kubernetes `metadata.finalizers` to enforce ordering and external cleanup (for example, closing a pull request in your Git provider) before objects disappear from the cluster. Finalizers are normal Kubernetes machinery; the promoter controllers add and remove them during reconciliation.

> [!WARNING]
> Clearing finalizers by hand (`kubectl edit` / `patch` to strip `metadata.finalizers`) should be a **last resort**. It tells the API server “forget this object’s cleanup obligations,” not “run the cleanup successfully.” Use the guidance below before removing anything.

## Finalizer reference

All promoter-defined finalizer strings live in the API package as constants (see [`api/v1alpha1/constants.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/api/v1alpha1/constants.go)). They are summarized here.

| Finalizer string | Kind(s) | Purpose |
| ---------------- | ------- | ------- |
| `pullrequest.promoter.argoproj.io/finalizer` | `PullRequest` | Blocks removal of the `PullRequest` CR until the controller has closed (or otherwise reconciled) the corresponding pull request in the SCM, when a real SCM ID exists. |
| `changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer` | `PullRequest` | Ensures the owning `ChangeTransferPolicy` can observe pull request status (for example ID and state) on the CR and record the promotion-history git note for the merge commit before the `PullRequest` is deleted, so promotion state and history stay consistent. |
| `changetransferpolicy.promoter.argoproj.io/finalizer` | `ChangeTransferPolicy` | On policy deletion, forces a reconcile pass that strips the CTP-owned finalizer from related `PullRequest`s (and related cleanup) before the policy object can finish deleting. |
| `gitrepository.promoter.argoproj.io/finalizer` | `GitRepository` | Prevents deleting a `GitRepository` while non-deleting `PullRequest`s still reference that repository. |
| `scmprovider.promoter.argoproj.io/finalizer` | `ScmProvider` | Prevents deleting an `ScmProvider` while `GitRepository`s in the same namespace still reference it. |
| `clusterscmprovider.promoter.argoproj.io/finalizer` | `ClusterScmProvider` | Same dependency idea as `ScmProvider`, for cluster-scoped SCM configuration. |
| `scmprovider.promoter.argoproj.io/secret-finalizer` | `Secret` | Placed on the credentials `Secret` referenced by an `ScmProvider` so the secret cannot be removed while the provider still exists (or until the controller clears it when safe). |
| `clusterscmprovider.promoter.argoproj.io/secret-finalizer` | `Secret` | Same pattern for secrets referenced by a `ClusterScmProvider`. |

No separate finalizer constant is defined for `PromotionStrategy`; RBAC may still mention `promotionstrategies/finalizers` for generic metadata updates. Behavior you care about for promotions is mostly on `ChangeTransferPolicy` and `PullRequest` as in the table above.

## Promotion history git notes

The `changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer` exists so the ChangeTransferPolicy controller can write a **promotion-history git note** on the merge commit before the `PullRequest` CR is deleted. That note is the durable record of what was promoted (pull request metadata, gate phases, dry/hydrated SHAs) when the SCM rewrites or strips the merge commit message — for example after a squash merge or a merge performed in the SCM UI.

Notes are stored at `refs/notes/promoter.history` on the Git repository. During reconciliation the controller reads them (falling back to commit-message trailers on older merges) and rebuilds `ChangeTransferPolicy.status.history`.

### Supported path: controller-initiated merge

When the promoter merges the pull request (`autoMerge: true`, the default), the SCM merge call includes `spec.mergeSha` as a required head match. If the proposed branch has moved since the last reconcile, the SCM rejects the merge and the controller refreshes `PullRequest.spec` before retrying. The snapshot in `spec.commit.message` (trailers) and `spec.mergeSha` therefore matches what actually merged.

In this path, history is accurate and `status.history[].mergeCommitSnapshotMismatch` stays false.

### External merge (SCM UI, Tide, or another bot)

When a pull request is merged or closed outside the controller, the PullRequest controller disambiguates merged vs closed via SCM `Get` by ID, sets `status.mergeCommitSha` when merged, and finalization writes the promotion-history note on that commit. The promoter may still be reading a **snapshot** of `PullRequest.spec` that lagged behind the real proposed branch tip:

1. The hydrator advances the proposed branch (new dry/hydrated SHAs).
2. The ChangeTransferPolicy has not yet reconciled and updated `spec.commit.message` / `spec.mergeSha`.
3. A user or bot merges on the SCM (merging the **current** proposed head, not the stale snapshot).

This is a **merge commit snapshot mismatch**: hydrator metadata on the SCM-reported merge commit disagrees with the promoter's last snapshot. The controller corrects proposed dry/hydrated SHAs from the merge commit, but **commit statuses in the note still reflect the earlier revision** and may not match the gates that applied to what actually merged.

Check `status.history[].mergeCommitSnapshotMismatch` on the ChangeTransferPolicy. When `true`, treat snapshot-derived commit statuses in that entry as potentially stale. The controller also emits [PromotionHistoryNoteMergeCommitSnapshotMismatch](../monitoring/events.md#changetransferpolicy) when it detects and corrects this during note writing.

> [!TIP]
> Prefer letting the promoter merge pull requests it manages. If you use `autoMerge: false` with Prow/Tide or similar, see [Dynamic Pull Request Labels](../advanced-usage/pull-request-labels.md#prow--tide-example) and the mismatch caveats below.

### How the merge commit is identified

The merge commit comes from **`PullRequest.status.mergeCommitSha`**, populated by the PullRequest controller from the SCM when the PR is no longer open (`Get` by `status.id`). CTP finalization attaches the promotion-history note to that commit — it does not walk git history to locate it.

At note write time, the controller compares the proposed dry SHA in the snapshot (`spec.commit.message` trailers) with hydrator metadata **on that merge commit**. When they differ, proposed SHAs in the note are corrected and `mergeCommitSnapshotMismatch` is set.

### Regular merge vs squash

| | Regular merge (`--no-ff`) | Squash merge |
| --- | --- | --- |
| SCM `mergeCommitSha` | Merge commit on active (often has second parent) | Squash commit on active (single parent) |
| Correct proposed hydrated SHA from git | Yes — second parent of merge commit when present | **No** — not stored in parent graph |
| Correct proposed dry SHA from git | Yes — `hydrator.metadata` on merge commit | Yes — `hydrator.metadata` on squash commit |
| External merge + snapshot mismatch | Note written on SCM SHA; proposed SHAs corrected; `mergeCommitSnapshotMismatch: true` | Same if SCM reports merge SHA and metadata is readable; commit statuses may still be stale |

Squash merges performed in the SCM UI also typically carry **no promoter trailers** in the commit message. The git note is the only way to reconstruct history for those merges; if finalization cannot locate the squash commit, that promotion leaves no history entry.

### Note write failures and the finalizer

If writing or pushing the note fails, the controller emits [PromotionHistoryNoteFailed](../monitoring/events.md#changetransferpolicy) and **keeps** the CTP finalizer so finalization retries. Do not strip the finalizer to “unstick” deletion unless you accept losing that history entry.

Inspect the note on a merge commit (after fetching the ref):

```bash
git fetch origin '+refs/notes/promoter.history:refs/notes/promoter.history'
git notes --ref=promoter.history show <merge-commit-sha>
```

## Risks of manually removing finalizers

Removing a finalizer **does not run** the controller logic that would have run on a normal delete. Effects depend on which finalizer you strip:

- **`PullRequest` (`pullrequest.promoter.argoproj.io/finalizer`)**  
  **Risk:** The Kubernetes object is gone while the real pull request may still be **open** in GitHub/GitLab/etc. You lose a single place to drive closure and can strand automation or humans on a live PR.

- **`PullRequest` (`changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer`)**  
  **Risk:** The `ChangeTransferPolicy` may never record the final PR identity/state from that object, and the [promotion-history git note](#promotion-history-git-notes) for the merge commit may never be written. Downstream status, history, or “externally closed” handling can be wrong or racy, and history for merges performed on the SCM (e.g. squash merges) can be permanently lost.

- **`ChangeTransferPolicy` (`changetransferpolicy.promoter.argoproj.io/finalizer`)**  
  **Risk:** The policy CR can be removed from etcd while related `PullRequest`s still carry the CTP finalizer or are not cleaned up the way the controller expects. You can leave policies “gone” but PR objects stuck terminating or inconsistent with Git.

- **`GitRepository` / `ScmProvider` / `ClusterScmProvider`**  
  **Risk:** You delete configuration or repo metadata while `PullRequest` or `GitRepository` objects still depend on it. Controllers may error, leak logical references, or leave PRs pointing at repositories or providers that no longer exist in the API.

- **`Secret` (SCM provider secret finalizers)**  
  **Risk:** Credentials disappear while `ScmProvider` / `ClusterScmProvider` / `GitRepository` still reference them, causing failing reconciles and hard-to-debug SCM auth errors.

In all cases, prefer fixing the **underlying** problem (permissions, SCM outage, bad spec, stuck reconcile) so the controller can clear finalizers itself.

## Reporting a bug: finalizer stuck

If a resource stays in `Terminating` for a long time with a promoter finalizer that never clears:

1. **Confirm which finalizer**  
   `kubectl get <kind> <name> -n <namespace> -o jsonpath='{.metadata.finalizers}'`  
   or `kubectl describe` and copy the `Finalizers` list.

2. **Capture controller signal** (adjust deployment name/namespace to your install):  
   - Logs from the **gitops-promoter** controller manager around the time deletion was requested.  
   - `kubectl get events -n <namespace> --field-selector involvedObject.name=<resource-name>` (and controller namespace if different).

3. **Resource state**  
   A redacted `kubectl get <kind> <name> -n <namespace> -o yaml` (remove secrets and tokens). Note `metadata.deletionTimestamp`, `metadata.generation`, and relevant **status** (for `PullRequest`: ID, state, conditions).

4. **Versioning**  
   Promoter / Helm chart version, Kubernetes version, and (if relevant) which SCM provider (GitHub, GitLab, …).

5. **Open an issue**  
   On [argoproj-labs/gitops-promoter](https://github.com/argoproj-labs/gitops-promoter/issues), include the finalizer string, the steps that led to delete (or scale-down), and whether removing the finalizer was required as an emergency workaround.

That gives maintainers enough to distinguish “controller never saw delete,” “SCM call failing,” “dependency ordering,” and “genuine bug in finalizer removal.”
