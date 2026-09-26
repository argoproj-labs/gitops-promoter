# Rolling Back an Environment

A [RevertCommit](../crd-specs.md#revertcommit) puts one environment's active branch back to a version that was
promoted earlier, and stops the bad change from being promoted again until you say so. Use it when a change is already
live and you need the previous version back now, before a fix can go through the normal promotion flow.

## How it works

1. **Restore.** The controller writes a new commit on top of the active branch whose content is the chosen hydrated
   commit: the whole tree, or only `activePath` when the ChangeTransferPolicy sets one. It is an ordinary commit, not a
   force-push, so the branch history stays intact. The proposed branch is not touched.
2. **Block.** The dry SHA that was live before the restore is recorded in `status.blockedDrySha`. The
   ChangeTransferPolicy will not open a promotion pull request for it.
3. **Hold.** While the RevertCommit exists, nothing is auto-merged into that environment. A pull request for a
   newer dry SHA can still open and run its checks, but it waits.

The restore runs once. The spec is immutable; to restore a different version, delete the RevertCommit and create a new
one.

## Rolling back

In the dashboard's history view, select an earlier version in the environment's column. The detail drawer shows a
**Restore this version** command you can copy. Or write the resource yourself:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: RevertCommit
metadata:
  name: revert-production
  namespace: <namespace of the ChangeTransferPolicy>
  # With multiple controller installs, copy the ChangeTransferPolicy's instance-id label:
  # labels:
  #   promoter.argoproj.io/instance-id: "<id>"
spec:
  changeTransferPolicyRef:
    name: <ChangeTransferPolicy for the environment>
  # The hydrated commit to restore: a previous commit on the environment's active branch.
  sha: <40- or 64-character hydrated SHA>
```

When `status.activeSha` is set and the Ready condition is `True`, the environment is running the restored version. The
restore also shows up in the environment's promotion history, marked as a restore.

> [!NOTE]
> Creating a RevertCommit is the authorization boundary: anyone who can create one in the ChangeTransferPolicy's
> namespace can restore that environment, and the push uses the controller's git credentials. Grant `create` on
> `revertcommits` accordingly.

## Resuming promotion

1. Fix forward: get a new dry commit hydrated onto the environment's proposed branch.
2. Delete the RevertCommit. Auto-merge is allowed again.

Deleting the RevertCommit on its own does not necessarily bring the reverted change back. The restore commit sits on
top of the reverted one, so when promotions use merge commits the active branch already contains the proposed commit,
and no pull request opens for the reverted dry SHA until a new commit lands on the proposed branch.
