# Rolling Back an Environment

A [RevertActiveCommit](../crd-specs.md#revertactivecommit) puts one environment's active branch back to a version that was
promoted earlier, and stops the bad change from being promoted again until you say so. Use it when a change is already
live and you need the previous version back now, before a fix can go through the normal promotion flow.

## How it works

1. **Restore.** The controller writes a new commit on top of the active branch whose content is the chosen hydrated
   commit: the whole tree, or only `activePath` when the ChangeTransferPolicy sets one. It is an ordinary commit, not a
   force-push, so the branch history stays intact. The proposed branch is not touched. The restore commit copies the
   chosen version's [git trailers](../debugging/git-trailers.md) into its own commit message and its
   promotion-history note into its own note, each with `Promoter-restored-from` added. When the chosen version has no
   note, the note is built from its commit-message trailers instead. The copied pull request details, including
   `Pull-request-merge-time`, still describe that version's original promotion.
2. **Block.** Until the restore lands, the RevertActiveCommit itself holds every promotion pull request and auto-merge
   for that environment (there is no marker in git yet). Once `status.restoredFrom` is set, the gate moves to the
   restore commit's promotion-history note: the dry SHA that was live before the restore (also recorded in
   `status.blockedDrySha`) cannot open a pull request, and nothing is auto-merged, while that note carries
   `Promoter-restored-from` and lacks `Promoter-revert-unblocked-at`.
3. **Hold.** A pull request for a newer dry SHA can still open and run its checks, but it waits until the note is
   unblocked.

The restore runs once. `promotionStrategyRef`, `branch`, and `sha` are immutable; to restore a different version,
create another RevertActiveCommit. `spec.blockEnvironment` defaults to true and may be changed. Once
that new restore is the active tip, the ChangeTransferPolicy deletes the previous one. Deletion does not stamp
`Promoter-revert-unblocked-at`; the new tip stays held until its own `spec.blockEnvironment` is set to false. An object that has not recorded `status.activeSha` is left in place, as is one
whose restore commit is still the tip or is no longer in the repository. If the active branch already has the chosen version's content, nothing is
written, `status.blockedDrySha` stays empty, and the RevertActiveCommit emits an `AlreadyRestored` event; the
auto-merge hold still applies while the restore note is gated (or while the restore is still pending).

> [!IMPORTANT]
> The restore commit is pushed directly to the active branch with the controller's git credentials; it does not go
> through a pull request. If the active branch is protected against direct pushes, the push is rejected and the
> RevertActiveCommit stays `Ready=False`. Allow the controller's identity (for example the GitHub App or deploy key) to
> bypass that protection. When `activePath` is empty, you can make the same git writes yourself; see
> [Restoring by hand (no activePath)](#restoring-by-hand-no-activepath).

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

When `status.activeSha` is set and the Ready condition is `True`, the active Git branch has been restored. Confirm
that Argo CD has synced the restore commit (`status.activeSha`) and that the workloads are healthy before you remove
the promotion hold. With auto-sync disabled, a sync window blocking deployment, or a failed rollout, the Git restore
can succeed while the previous workload is still serving.

The restore also shows up in the environment's promotion history, marked by `restoredFrom`. A `sha` that is not in the active branch's history (for example a commit from another environment's branch or an unmerged pull request) is refused, and the Ready condition is `False` with the reason. A commit that carries `Promoter-restored-from` is itself a restore and is refused. That trailer is written on the restore commit only. The commit the restore moved off does not carry it, so that commit can be restored again. Once the active tip is already the restore of `sha` and its note carries `Promoter-revert-unblocked-at`, another RevertActiveCommit for that same `sha` is refused until the active branch moves. The note is left unchanged, so the unblock stays in history. While that refused RevertActiveCommit exists, `status.restoredFrom` stays empty and it holds promotion for the environment; delete it to release that pending hold. Deleting a RevertActiveCommit whose restore succeeded does not stamp `Promoter-revert-unblocked-at`.

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
2. Set `spec.blockEnvironment` to `false` on the RevertActiveCommit. The controller stamps
   `Promoter-revert-unblocked-at: <RFC3339>` on the restore commit's promotion-history note, then deletes the
   RevertActiveCommit. A finalizer holds that delete until the note is written. Auto-merge is allowed again, and
   the new commit promotes as usual. The unblock time shows up on the restore history entry as `revertUnblockedAt`.
   If the git repository is unreachable, the RevertActiveCommit stays until the note can be written. Deleting the
   RevertActiveCommit while `blockEnvironment` is still true does not stamp the trailer, so deleting the
   PromotionStrategy leaves the hold in place. While the active tip is still that gated restore, the
   ChangeTransferPolicy creates a RevertActiveCommit (with `blockEnvironment` defaulting to true) if one is missing,
   and setting the field to false on that object is how you release it. A restore done
   [by hand](#restoring-by-hand-no-activepath) gets that object the same way; you can still stamp the trailer with
   the script under [Lift the hold](#lift-the-hold).

## Restoring by hand (no activePath)

When the environment's ChangeTransferPolicy leaves `activePath` empty, the restore commit's tree is the whole tree of
the hydrated commit you are putting back. The scripts below perform that restore, and lift the hold afterwards, with
git. Run them from a clone that can push the active branch and `refs/notes/promoter.history`. `jq` is required. The
target commit has to be the active tip or one of its ancestors. The script fetches that branch; a shallow clone that
does not contain the target fails at `git cat-file`.

The commit is parented on the current tip (`git commit-tree`) and pushed with `--force-with-lease`. A branch that moved
after the fetch is rejected; fetch and run the script again. The note is pushed first. If the branch push is then
rejected, that note sits on a commit that never became the tip, and the retry writes a new commit and a new note. The
proposed branch is left where it is. The author of the new commit is your `user.name` / `user.email`.

Once the note is on the active tip, the ChangeTransferPolicy reads the same gate it reads after a RevertActiveCommit
finishes: `Promoter-restored-from` without `Promoter-revert-unblocked-at` holds auto-merge, and the dry SHA in
`hydrator.metadata` on the parent commit (the tip this restore moved off of) cannot open a pull request. The
ChangeTransferPolicy also creates a RevertActiveCommit for that restore (when the policy has none yet and the tip is
still gated), with `spec.blockEnvironment` defaulting to true. Set that field to `false` to lift the hold — the controller
writes the note, then deletes the RevertActiveCommit. You can still stamp `Promoter-revert-unblocked-at` with the script under
[Lift the hold](#lift-the-hold).

The new commit message copies the target commit's [message trailers](../debugging/git-trailers.md). The new note copies
the target's promotion-history note, with `Promoter-restored-from` set to the target SHA. When that note is missing,
empty, or not JSON, the note is built from the message trailers instead. `Promoter-restored-from` is the last line of
the commit message. In the note it is one more key in the JSON object.

> [!WARNING]
> A ChangeTransferPolicy that sets `activePath` restores only that directory onto the current active tree. This script
> replaces the whole tree, so on a branch shared by several PromotionStrategies it overwrites every other application.

### Restore

```bash
#!/bin/bash
# Whole-tree restore. The ChangeTransferPolicy's activePath must be empty.
set -euo pipefail

ACTIVE_BRANCH="environment/production"
TARGET_SHA="<40- or 64-character hydrated SHA>"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

NOTES_REF="refs/notes/promoter.history"

# Commit-message trailers as JSON. Keys are sorted; values keep their original order.
# drop_key omits one key so the restore marker can be appended last on the message.
trailer_json() {
  local sha="$1" drop_key="${2:-}"
  git log -1 --format=%B "$sha" | git interpret-trailers --only-trailers | jq -Rs --arg drop "$drop_key" '
    split("\n")
    | map(select(length > 0))
    | map(capture("^(?<k>[^:]+):[ \\t]*(?<v>.*)$"))
    | map(select(.k != $drop))
    | group_by(.k)
    | map({key: .[0].k, value: map(.v)})
    | from_entries
  '
}

# Promotion-history note when it is a non-empty JSON object; otherwise the commit trailers.
promotion_json() {
  local sha="$1" raw
  raw=$(git notes --ref="$NOTES_REF" show "$sha" 2>/dev/null || true)
  if printf '%s' "$raw" | jq -e 'type == "object" and length > 0' >/dev/null 2>&1; then
    printf '%s' "$raw"
    return
  fi
  trailer_json "$sha"
}

git fetch origin "$ACTIVE_BRANCH"
if git ls-remote --exit-code origin "$NOTES_REF" >/dev/null 2>&1; then
  git fetch origin "+${NOTES_REF}:${NOTES_REF}"
fi

ACTIVE_TIP=$(git rev-parse "origin/${ACTIVE_BRANCH}")
git cat-file -e "${TARGET_SHA}^{commit}"
if ! git merge-base --is-ancestor "$TARGET_SHA" "$ACTIVE_TIP"; then
  echo "commit ${TARGET_SHA} is not in the history of ${ACTIVE_BRANCH}" >&2
  exit 1
fi

WANT_TREE=$(git rev-parse --verify "${TARGET_SHA}^{tree}")
TIP_TREE=$(git rev-parse --verify "${ACTIVE_TIP}^{tree}")
if [[ "$WANT_TREE" == "$TIP_TREE" ]]; then
  marker=$(promotion_json "$ACTIVE_TIP" | jq -r '."Promoter-restored-from"[0] // empty')
  if [[ "$marker" == "$TARGET_SHA" ]]; then
    if git notes --ref="$NOTES_REF" show "$ACTIVE_TIP" 2>/dev/null \
      | jq -e '."Promoter-revert-unblocked-at" | length > 0' >/dev/null 2>&1; then
      echo "active tip ${ACTIVE_TIP} already restores ${TARGET_SHA} and carries Promoter-revert-unblocked-at; it cannot be restored again until the active branch moves" >&2
      exit 1
    fi
    echo "active tip ${ACTIVE_TIP} already restores ${TARGET_SHA}; nothing to write"
    exit 0
  fi
  echo "active branch already matches ${TARGET_SHA} at ${ACTIVE_TIP}; nothing to write"
  exit 0
fi

if promotion_json "$TARGET_SHA" | jq -e '."Promoter-restored-from" | length > 0' >/dev/null; then
  echo "commit ${TARGET_SHA} carries Promoter-restored-from and cannot be restored to" >&2
  exit 1
fi

short=${TARGET_SHA:0:7}
lines=$(trailer_json "$TARGET_SHA" "Promoter-restored-from" \
  | jq -r 'to_entries[] | .key as $k | .value[] | "\($k): \(.)"')
msg_file=$(mktemp "${TMPDIR:-/tmp}/promoter-restore.XXXXXX")
{
  printf 'Revert %s to %s\n\n' "$ACTIVE_BRANCH" "$short"
  if [[ -n "$lines" ]]; then
    printf '%s\n' "$lines"
  fi
  printf 'Promoter-restored-from: %s\n' "$TARGET_SHA"
} >"$msg_file"
RESTORE_SHA=$(git commit-tree "$WANT_TREE" -p "$ACTIVE_TIP" -F "$msg_file")
rm -f "$msg_file"

note=$(promotion_json "$TARGET_SHA" | jq -c --arg sha "$TARGET_SHA" '
  . + {"Promoter-restored-from": [$sha]} | to_entries | sort_by(.key) | from_entries
')
git notes --ref="$NOTES_REF" add -f -m "$note" "$RESTORE_SHA"
git push origin "${NOTES_REF}:${NOTES_REF}"
git push --force-with-lease="refs/heads/${ACTIVE_BRANCH}:${ACTIVE_TIP}" \
  origin "${RESTORE_SHA}:refs/heads/${ACTIVE_BRANCH}"

echo "restored ${ACTIVE_BRANCH} to ${TARGET_SHA} as ${RESTORE_SHA}"
```

The script prints the new commit SHA. Its subject is `Revert <branch> to <short sha>`, the trailer block ends with
`Promoter-restored-from: <target>`, and the note on that SHA is the copied history JSON.

```bash
git log -1 --format=%B <restore-sha>
git notes --ref=refs/notes/promoter.history show <restore-sha>
```

Running it again, while that commit is still the tip and the note has no `Promoter-revert-unblocked-at`, prints
`nothing to write`. After the note has been unblocked, another run for the same target is refused until the active
branch moves. A target that already carries `Promoter-restored-from` is refused. A commit that is not in the active
branch's history is refused.

### Lift the hold

This stamps `Promoter-revert-unblocked-at` on the restore commit's note, which is the write the RevertActiveCommit
controller makes when `spec.blockEnvironment` is set to `false`. The commit message stays as it is. The trailer is note-only.

[Resuming promotion](#resuming-promotion) still applies: the stamp allows auto-merge again, and the reverted dry SHA
stays contained in the active branch until a new commit lands on the proposed branch.

`RESTORE_SHA` defaults to the current active tip. Set it when the restore commit you are releasing is an older one.

```bash
#!/bin/bash
set -euo pipefail

ACTIVE_BRANCH="environment/production"
RESTORE_SHA=""

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

NOTES_REF="refs/notes/promoter.history"

trailer_json() {
  local sha="$1"
  git log -1 --format=%B "$sha" | git interpret-trailers --only-trailers | jq -Rs '
    split("\n")
    | map(select(length > 0))
    | map(capture("^(?<k>[^:]+):[ \\t]*(?<v>.*)$"))
    | group_by(.k)
    | map({key: .[0].k, value: map(.v)})
    | from_entries
  '
}

git fetch origin "$ACTIVE_BRANCH"
git fetch origin "+${NOTES_REF}:${NOTES_REF}"

if [[ -z "$RESTORE_SHA" ]]; then
  RESTORE_SHA=$(git rev-parse "origin/${ACTIVE_BRANCH}")
fi

raw=$(git notes --ref="$NOTES_REF" show "$RESTORE_SHA" 2>/dev/null || true)
if printf '%s' "$raw" | jq -e 'type == "object" and length > 0' >/dev/null 2>&1; then
  note=$raw
elif git log -1 --format=%B "$RESTORE_SHA" | git interpret-trailers --only-trailers | grep -q '^Promoter-restored-from:'; then
  note=$(trailer_json "$RESTORE_SHA")
else
  echo "${RESTORE_SHA} is not a restore commit" >&2
  exit 1
fi

if printf '%s' "$note" | jq -e '."Promoter-revert-unblocked-at" | length > 0' >/dev/null; then
  echo "${RESTORE_SHA} already carries Promoter-revert-unblocked-at"
  exit 0
fi

now=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
updated=$(printf '%s' "$note" | jq -c --arg t "$now" '
  . + {"Promoter-revert-unblocked-at": [$t]} | to_entries | sort_by(.key) | from_entries
')
git notes --ref="$NOTES_REF" add -f -m "$updated" "$RESTORE_SHA"
git push origin "${NOTES_REF}:${NOTES_REF}"
echo "stamped Promoter-revert-unblocked-at ${now} on ${RESTORE_SHA}"
```
