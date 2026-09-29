# Rolling Back an Environment

A [RevertActiveCommit](../crd-specs.md#revertactivecommit) puts one environment's active branch back to a version that was
promoted earlier, and stops the bad change from being promoted again until you say so. Use it when a change is already
live and you need the previous version back now, before a fix can go through the normal promotion flow.

## How it works

1. **Restore.** The controller writes a new commit on top of the active branch whose content is the chosen hydrated
   commit: the whole tree, or only `activePath` when the ChangeTransferPolicy sets one. It is an ordinary commit, not a
   force-push, so the branch history stays intact. The proposed branch is not touched. The restore commit gets a copy
   of the chosen version's [promotion-history note](../debugging/git-trailers.md) with `Promoter-restored-from` added.
   The copied pull request details, including `Pull-request-merge-time`, still describe that version's original
   promotion.
2. **Block.** The dry SHA that was live before the restore is recorded in `status.blockedDrySha`. The
   ChangeTransferPolicy will not open a promotion pull request for it.
3. **Hold.** While the RevertActiveCommit exists, nothing is auto-merged into that environment. A pull request for a
   newer dry SHA can still open and run its checks, but it waits.

The restore runs once. The spec is immutable; to restore a different version, delete the RevertActiveCommit and create a new
one. If the active branch already has the chosen version's content, nothing is written, `status.blockedDrySha` stays
empty, and the RevertActiveCommit emits an `AlreadyRestored` event; the auto-merge hold still applies while it exists.

> [!IMPORTANT]
> The restore commit is pushed directly to the active branch with the controller's git credentials; it does not go
> through a pull request. If the active branch is protected against direct pushes, the push is rejected and the
> RevertActiveCommit stays `Ready=False`. Allow the controller's identity (for example the GitHub App or deploy key) to
> bypass that protection, or roll back by other means.

## Rolling back

In the dashboard's history view, select an earlier version in the environment's column. The detail drawer shows a
**Restore this version** command you can copy. Or write the resource yourself:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: RevertActiveCommit
metadata:
  name: revert-production
  namespace: <namespace of the PromotionStrategy>
  # With multiple controller installs, copy the ChangeTransferPolicy's instance-id label:
  # labels:
  #   promoter.argoproj.io/instance-id: "<id>"
spec:
  promotionStrategyRef:
    name: <PromotionStrategy>
  # The environment branch to restore, matching spec.environments[].branch.
  branch: <environment branch>
  # The hydrated commit to restore: a previous commit on the environment's active branch.
  sha: <40- or 64-character hydrated SHA>
```

When `status.activeSha` is set and the Ready condition is `True`, the environment is running the restored version. The
restore also shows up in the environment's promotion history, marked by `restoredFrom`. A `sha` that is not in the active branch's history (for example a commit from another environment's branch or an unmerged pull request) is refused, and the Ready condition is `False` with the reason. A commit thatcarries `Promoter-restored-from` is itself a restore and is refused. That trailer is written on the restore commit only. The commit the restore moved off does not carry it, so that commit can be restored again.

> [!NOTE]
> Creating a RevertActiveCommit is the authorization boundary: anyone who can create one in the PromotionStrategy's
> namespace can restore that environment, and the push uses the controller's git credentials. Grant `create` on
> `revertactivecommits` accordingly.

## Resuming promotion

> [!WARNING]
> Deleting the RevertActiveCommit does **not** by itself put the reverted change back, and usually does not open a pull
> request for it at all. The restore commit sits on top of the reverted one, so when promotions use merge commits the
> active branch already contains the proposed commit, and no pull request opens for the reverted dry SHA until a new
> commit lands on the proposed branch. Plan to fix forward.

1. Fix forward: get a new dry commit hydrated onto the environment's proposed branch.
2. Delete the RevertActiveCommit. Auto-merge is allowed again, and the new commit promotes as usual.
