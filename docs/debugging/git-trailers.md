# Git Trailers

GitOps Promoter records what it promoted as **git trailers** — `Key: value` lines in a trailing block of a commit message, the same convention as `Signed-off-by`. They are how `ChangeTransferPolicy.status.history` (and the promotion history in the dashboard) is rebuilt from Git rather than from controller memory, so a restarted or upgraded controller still shows the same history.

The same trailer data is stored in up to three places, and they do not always agree. This page is the reference for what each trailer means, where it is written, and — most importantly — [which values can differ between the git note and the commit message](#note-versus-commit-message).

## Trailer reference

All trailer keys are Go constants in [`internal/types/constants/trailers.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/types/constants/trailers.go). They are summarized here.

| Trailer | Value | Read back into |
| --- | --- | --- |
| `Pull-request-id` | The SCM's pull request ID. | `status.history[].pullRequest.id` |
| `Pull-request-url` | Link to the pull request. Ignored unless it starts with `http://` or `https://`. | `status.history[].pullRequest.url` |
| `Pull-request-creation-time` | When the pull request was opened, RFC 3339. | `status.history[].pullRequest.prCreationTime` |
| `Pull-request-merge-time` | When the pull request was merged, RFC 3339. See [below](#note-versus-commit-message) — this one is special. | `status.history[].pullRequest.prMergeTime` |
| `Pull-request-source-branch` | The proposed branch, e.g. `environment/production-next`. | Not read back; archival only. |
| `Pull-request-target-branch` | The active branch, e.g. `environment/production`. | Not read back; archival only. |
| `Sha-dry-proposed` | Dry (pre-hydration) SHA of the proposed change. | Used to detect a [snapshot mismatch](finalizers.md#external-merge-scm-ui-tide-or-another-bot); no dedicated history field. |
| `Sha-hydrated-proposed` | Hydrated SHA of the proposed change. | `status.history[].proposed.hydrated` (the entry is loaded from Git at that SHA). |
| `Sha-dry-active` | Dry SHA of the active branch at snapshot time. | Not read back — the active side of a history entry is reconstructed from the merge commit itself. |
| `Sha-hydrated-active` | Hydrated SHA of the active branch at snapshot time. | Not read back, same reason. |
| `Commit-status-active-<key>-phase` | Phase of gate `<key>` on the active branch, e.g. `success`. | `status.history[].active.commitStatuses[]` |
| `Commit-status-active-<key>-url` | Gate detail link. Ignored unless `http://` or `https://`. | `status.history[].active.commitStatuses[]` |
| `Commit-status-active-<key>-description` | Gate description, **JSON-encoded** so it survives multi-line and quoted text. | `status.history[].active.commitStatuses[]` |
| `Commit-status-proposed-<key>-*` | Same three suffixes for gates on the proposed branch. | `status.history[].proposed.commitStatuses[]` |
| `Promoter-merge-commit-snapshot-mismatch` | `true` when the note's proposed SHAs had to be corrected from the merge commit. Written **only** to the note. | `status.history[].mergeCommitSnapshotMismatch` |

> [!NOTE]
> Gate keys are recovered by trimming the final `-phase`, `-url`, or `-description` segment from the trailer key, so a gate key of its own may contain dashes (`Commit-status-active-argocd-health-phase` yields key `argocd-health`). A gate key whose own last segment looks like a suffix would be parsed incorrectly.

## Where trailers come from

### 1. The snapshot in `PullRequest.spec.commit.message`

Each time the ChangeTransferPolicy controller reconciles an **already-open** pull request, it rewrites `spec.commit.message` as title, description, and a freshly built trailer block reflecting current CTP status.

Two consequences worth knowing:

- A brand-new `PullRequest` gets only a title and description. Trailers first appear on the **next** reconcile, so a pull request merged immediately after creation can have no trailers at all — and then no history note is written, because there is nothing to record.
- Because this block is rebuilt from CTP status each pass, it is a *snapshot* of the moment the promoter last looked, not necessarily of what eventually merges.

These edits stay in Kubernetes while the pull request is open; the promoter does not push a trailer refresh to the SCM (only title and description are synced).

### 2. The merge commit message

When the promoter merges the pull request itself, it adds `Pull-request-merge-time` to the message in memory and hands the result to the SCM as the merge commit message. Most providers use it; Bitbucket Cloud's merge API does not accept a message, and squash merges or merges performed in the SCM UI frequently rewrite or discard it.

That fragility is exactly why the note exists.

### 3. The promotion history git note

At finalization — before the `PullRequest` CR is allowed to disappear — the ChangeTransferPolicy controller writes the trailers as a JSON object in a git note at `refs/notes/promoter.history`, attached to the commit named by `PullRequest.status.mergedTargetSha`. See [Promotion history git notes](finalizers.md#promotion-history-git-notes) for the finalizer mechanics, failure handling, and what happens when no merge SHA is ever obtained.

When rebuilding history, the controller walks the last five first-parent commits of the active branch and, for each, **prefers the note** and falls back to the commit message's trailers when no readable note exists. That fallback is what keeps merges from before the notes feature visible.

## Note versus commit message

For a promoter-initiated merge the note and the merge commit message agree: the SCM merge is gated on `spec.mergeSha` matching the proposed head, so the snapshot the promoter holds is the thing that merged.

An **external** merge has no such guard. The proposed branch can advance after the promoter's last snapshot, and a human or bot then merges the *current* head. The note is corrected against the commit that actually merged; the commit message is not. The following table is the authoritative list of what can diverge.

| Trailer | Can the note differ from the commit message? | Why |
| --- | --- | --- |
| `Sha-dry-proposed` | **Yes.** | The note is corrected from `hydrator.metadata` on the merge commit, which is the ground truth for what merged. A commit message carrying trailers keeps the stale snapshot value. When metadata is missing or malformed, the snapshot value is kept in both. |
| `Sha-hydrated-proposed` | **Yes, on regular merges.** | Corrected from the merge commit's **second parent**. A squash or fast-forward commit has no second parent, so the snapshot value is kept and the note matches the message. |
| `Pull-request-merge-time` | **Yes, routinely.** | It is added in-flight at merge time and never persisted back to `spec.commit.message`, so the snapshot the note is built from usually lacks it. The note then derives it from the **merge commit's own timestamp**, while a controller-written merge commit message carries the promoter's clock reading from the moment it called merge. |
| `Commit-status-active-*`, `Commit-status-proposed-*` | **No — but they may describe the wrong revision.** | Gate phases cannot be reconstructed from Git, so the note keeps the snapshot values verbatim. After an external merge they may describe the gates that applied to an *earlier* proposed revision than the one that merged. |
| `Promoter-merge-commit-snapshot-mismatch` | **Note only.** | Never written to a commit message. Its presence is the signal that the corrections above happened. |
| `Pull-request-id`, `-url`, `-creation-time`, `-source-branch`, `-target-branch`, `Sha-dry-active`, `Sha-hydrated-active` | **No.** | Copied into the note verbatim from the snapshot. |

So when `status.history[].mergeCommitSnapshotMismatch` is `true`, treat the proposed SHAs in that entry as trustworthy — they were re-read from the merge commit — and the commit statuses as possibly describing a superseded revision. The controller also emits [PromotionHistoryNoteMergeCommitSnapshotMismatch](../monitoring/events.md#changetransferpolicy) when it applies the correction.

## Inspecting trailers

Read the trailer block of a commit on an environment branch:

```bash
git log -1 --format=%B <sha> | git interpret-trailers --parse
```

Read the note for a merge commit, which requires fetching the notes ref first:

```bash
git fetch origin '+refs/notes/promoter.history:refs/notes/promoter.history'
git notes --ref=promoter.history show <merge-commit-sha>
```

The note is a JSON object mapping each trailer key to a list of values, so `jq` is usually the fastest way to pull one field:

```bash
git notes --ref=promoter.history show <merge-commit-sha> | jq '.["Sha-dry-proposed"]'
```

> [!TIP]
> Promoter trailers are stripped from the commit bodies shown in `ChangeTransferPolicy` status, so `status.active.hydrated.body` displays your commit message rather than the bookkeeping block. `Pull-request-merge-time` is not on the strip list and can still appear there.

## Related

- [Promotion history git notes](finalizers.md#promotion-history-git-notes) — the finalizer that writes the note, regular versus squash merges, and note write failures.
- [Dynamic Pull Request Labels](../advanced-usage/pull-request-labels.md#prow-tide-example) — running with `autoMerge: false` and an external merge bot, the main way to hit a snapshot mismatch.
- [GitCommitStatus](../gating-promotions/built-in-gates/git-commit-status.md) — gate expressions can read `Commit.Trailers`. Because promoter trailers are stripped from the status bodies those expressions evaluate, they see your own and your hydrator's trailers, not the ones on this page.
