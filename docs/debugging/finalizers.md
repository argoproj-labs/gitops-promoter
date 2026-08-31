# Finalizers

GitOps Promoter uses Kubernetes `metadata.finalizers` to enforce ordering and external cleanup (for example, closing a pull request in your Git provider) before objects disappear from the cluster. Finalizers are normal Kubernetes machinery; the promoter controllers add and remove them during reconciliation.

> [!WARNING]
> Clearing finalizers by hand (`kubectl edit` / `patch` to strip `metadata.finalizers`) should be a **last resort**. It tells the API server “forget this object’s cleanup obligations,” not “run the cleanup successfully.” Use the guidance below before removing anything.

## Finalizer reference

All promoter-defined finalizer strings live in the API package as constants (see [`api/v1alpha1/constants.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/api/v1alpha1/constants.go)). They are summarized here.

| Finalizer string | Kind(s) | Purpose |
| ---------------- | ------- | ------- |
| `pullrequest.promoter.argoproj.io/finalizer` | `PullRequest` | Blocks removal of the `PullRequest` CR until the SCM pull request has reached a terminal outcome, when a real SCM ID exists: either the controller closed it, or the SCM reported it merged/closed (recording `status.mergedTargetSha` for a merge). |
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

Notes are stored at `refs/notes/promoter.history` on the Git repository. During reconciliation the controller reads them and rebuilds `ChangeTransferPolicy.status.history`.

The promoter still writes the same trailers into every managed pull request's commit message, so when a commit has no readable note the controller falls back to the commit-message trailers. That covers merges predating the notes feature. See [Git Trailers](git-trailers.md) for what each trailer records and [which values can differ between the note and the commit message](git-trailers.md#note-versus-commit-message).

### Supported path: controller-initiated merge

When the promoter merges the pull request (`autoMerge: true`, the default), the SCM merge call includes `spec.mergeSha` as a required head match. If the proposed branch has moved since the last reconcile, the SCM rejects the merge and the controller refreshes `PullRequest.spec` before retrying. The snapshot in `spec.commit.message` (trailers) and `spec.mergeSha` therefore matches what actually merged.

In this path, history is accurate and `status.history[].mergeCommitSnapshotMismatch` stays false.

### External merge (SCM UI, Tide, or another bot)

When a pull request is merged or closed outside the controller, the PullRequest controller disambiguates merged vs closed via SCM `Get` by ID, sets `status.mergedTargetSha` when merged, and finalization writes the promotion-history note on that commit. The promoter may still be reading a **snapshot** of `PullRequest.spec` that lagged behind the real proposed branch tip:

1. The hydrator advances the proposed branch (new dry/hydrated SHAs).
2. The ChangeTransferPolicy has not yet reconciled and updated `spec.commit.message` / `spec.mergeSha`.
3. A user or bot merges on the SCM (merging the **current** proposed head, not the stale snapshot).

This is a **merge commit snapshot mismatch**: hydrator metadata on the SCM-reported merge commit disagrees with the promoter's last snapshot. The controller corrects the proposed **dry** SHA from the merge commit's hydrator metadata and, for a regular merge commit with a second parent, the proposed **hydrated** SHA as well. The gate phases in the note are **not** corrected — they still reflect the earlier revision and may not match the gates that applied to what actually merged.

Check `status.history[].mergeCommitSnapshotMismatch` on the ChangeTransferPolicy. When `true`, these fields in that entry are potentially stale:

| Field | Why |
| --- | --- |
| `status.history[].proposed.commitStatuses` | Gate phases read from the snapshot trailers; never reconstructed from the merge commit. |
| `status.history[].active.commitStatuses` | Same snapshot trailers, same caveat. |
| `status.history[].proposed.hydrated` | Reconstructed from the merge commit's second parent on a regular merge, but a squash commit has no second parent, so the snapshot trailer value is kept. |

`status.history[].active.dry` and `status.history[].active.hydrated` are read back from the merge commit itself, so they describe what actually merged either way. The controller also emits [PromotionHistoryNoteMergeCommitSnapshotMismatch](../monitoring/events.md#changetransferpolicy) when it detects and corrects this during note writing.

> [!TIP]
> Prefer letting the promoter merge pull requests it manages. If you use `autoMerge: false` with Prow/Tide or similar, see [Dynamic Pull Request Labels](../advanced-usage/pull-request-labels.md#prow--tide-example) and the mismatch caveats below.

### How the merge commit is identified

The merge commit comes from **`PullRequest.status.mergedTargetSha`**, populated by the PullRequest controller from the SCM. When the promoter performs the merge itself and the provider returns the SHA in the merge response (GitHub, GitLab, Bitbucket Cloud), it is recorded immediately. Otherwise — external merges, and providers whose merge response omits the SHA (Gitea, Forgejo, Azure DevOps) — it is recovered by a `Get` by `status.id` once the PR is no longer open. CTP finalization attaches the promotion-history note to that commit.

That `Get` also runs during deletion. Deleting a `PullRequest` with `kubectl` can race an external merge the controller has not observed yet, so deletion finalization asks the SCM by `status.id` and keeps `pullrequest.promoter.argoproj.io/finalizer` until status records a terminal outcome. Without that hold the resource would disappear on the first deletion reconcile with `status.state: open`, and the merge SHA — and therefore the history note — would be lost.

At note write time, the controller compares the proposed dry SHA in the snapshot (`spec.commit.message` trailers) with hydrator metadata **on that merge commit**. When they differ, proposed SHAs in the note are corrected and `mergeCommitSnapshotMismatch` is set.

### Regular merge vs squash

Both merge styles use the same **`PullRequest.status.mergedTargetSha`** reported by the SCM (GitHub `merge_commit_sha`, GitLab `squash_commit_sha` or `merge_commit_sha`, and so on), whether it came from the merge response or a later `Get`. CTP finalization writes the promotion-history git note on that commit for either style — there is no git history walk to locate it.

What still differs is **what can be reconstructed from git at that commit**:

| | Regular merge (`--no-ff`) | Squash merge |
| --- | --- | --- |
| SCM-reported `mergedTargetSha` | Merge commit on active | Squash commit on active |
| Promotion-history note written | Yes, when SCM reports merged + SHA | Yes, when SCM reports merged + SHA |
| `status.history[].proposed.hydrated` recoverable from git | Yes — second parent of the merge commit | **No** — a squash commit has one parent, so the snapshot trailer value is kept |
| `status.history[].active.dry` recoverable from git | Yes — `hydrator.metadata` on the merge commit | Yes — `hydrator.metadata` on the squash commit (when present) |
| External merge + snapshot mismatch | Note on SCM SHA; the note's dry SHA corrected; `proposed.hydrated` corrected when a second parent exists; `mergeCommitSnapshotMismatch: true` when the dry SHA differed | Note on SCM SHA; the note's dry SHA corrected when metadata is readable; `proposed.hydrated` **not** corrected; `proposed.commitStatuses` and `active.commitStatuses` may still be stale |

Squash commits on the SCM also typically carry **no promoter trailers** in the commit message itself — the git note (written at finalization using `mergedTargetSha`) is what preserves PR metadata and gate snapshots for history. A history entry is **missing** only when finalization never gets a merge SHA (for example `status.state=unknown` after the PR record is gone on the SCM, or the PR was closed without merging).

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
  **Risk:** The Kubernetes object is gone while the real pull request may still be **open** in GitHub/GitLab/etc. You lose a single place to drive closure and can strand automation or humans on a live PR. It also skips the SCM lookup that records `status.mergedTargetSha`, so if the pull request had merged, the [promotion-history note](#promotion-history-git-notes) for that merge is lost.

- **`PullRequest` (`changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer`)**  
  **Risk:** The `ChangeTransferPolicy` may never record the final PR identity/state from that object, and the [promotion-history git note](#promotion-history-git-notes) for the merge commit may never be written. Downstream status, history, or “externally closed” handling can be wrong or racy. History is lost when finalization never obtained `mergedTargetSha` (for example the SCM PR record was deleted before `Get` could run).

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
