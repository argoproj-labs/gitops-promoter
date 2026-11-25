# Dependents Qualified Commit Status Controller

> [!NOTE]
> This gate is new. It is covered by the controller's own tests, which play the hydrator and the SCM. Before relying
> on it, check two things in a non-production PromotionStrategy: that your hydrator hydrates again when a tag it
> follows moves, and that Promoter's credentials are allowed to create and move your environment tags.

The Dependents Qualified Commit Status controller is an **ordering gate that manages environment tags**. Like
[DependentsSuccessfulCommitStatus](dependents-successful-commit-status.md), it orders each environment after its
**dependent environments**. It differs in two ways:

- **It remembers.** It does not ask whether an upstream is running the target change and is
  [successful](../index.md#environment-success) *right now*. It asks whether the upstream **has ever** run the target
  change, or a newer change that contains it, and been successful while doing so.
- **It decides what each environment hydrates.** Every environment hydrates from its own tag in the DRY repository
  instead of from the DRY branch, and the controller advances each tag to the newest commit that environment's
  upstreams have qualified.

Read [Holding Back Environments](../../advanced-usage/holding-back-environments.md) first for the model and the
hydrator setup. This page is the controller reference.

## Minimal configuration

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: demo
spec:
  gitRepositoryRef:
    name: demo
  orderCommitStatusRef:
    group: promoter.argoproj.io
    kind: DependentsQualifiedCommitStatus
    name: demo
  environments:
    - branch: environment/dev
    - branch: environment/test
    - branch: environment/prod
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsQualifiedCommitStatus
metadata:
  name: demo
spec:
  key: dependents-qualified
  promotionStrategyRef:
    name: demo
  drySource:
    branch: main
```

Every environment of the PromotionStrategy hydrates from its own tag. The names are fixed (see
[Environment tags](#environment-tags)); with this example they are `promoter/environment/dev`,
`promoter/environment/test`, and `promoter/environment/prod`.

As with DependentsSuccessfulCommitStatus, omitting `dependsOn` on every PromotionStrategy environment infers a linear
chain from list order, and `PromotionStrategy.spec.environments[].dependsOn` declares a custom DAG. The graph rules
(acyclic, known branches, roots when any environment declares `dependsOn`) are identical; see
[Custom dependency graph](dependents-successful-commit-status.md#custom-dependency-graph).

## What the controller does

Each reconcile runs four steps in order. A reconcile is triggered by a change to the PromotionStrategy's status, by a
push to the DRY branch or to an environment tag (see [Webhooks](#webhooks)), by a change to a
[RestoreActiveCommit](../../advanced-usage/rolling-back.md) for the PromotionStrategy, and on the controller's requeue
interval.

### 1. Observe

- The HEAD of `spec.drySource.branch`.
- The commit each environment tag points at. Tags are read from the remote on every reconcile, because a person may
  move one at any time and not every SCM reports it.
- For each environment, the **effective proposed DRY SHA**: the hydrator's git note `drySha` on the proposed branch tip
  when present, otherwise `hydrator.metadata` on the proposed commit. A no-op hydration updates only the note, so the
  note is preferred everywhere on this page.
- For each environment, the **live DRY SHA**: normally the active commit's `hydrator.metadata` DRY SHA. When the
  environment has no promotion pending (active and proposed `hydrator.metadata` agree) and a note on the proposed tip
  names a newer DRY commit, the live DRY SHA is the note's, provided the proposed tip and the active commit have
  identical content under the environment's `activePath`. The comparison guards against a note that describes
  manifests the environment is not running.

### 2. Qualify

For each environment `U`, if every active commit status is `success`, the live DRY SHA is **qualified** for `U`. The
controller keeps one watermark per environment: the newest qualified DRY SHA. Qualifying a commit qualifies all of its
ancestors, so the watermark is all the gate needs.

- **On the DRY branch.** Only a commit reachable from the HEAD of `spec.drySource.branch` can be qualified. An
  environment running a commit from another branch qualifies nothing.
- **Latched.** A later failure does not move the watermark back.
- **Forward only.** The watermark moves only to a descendant of its current value. An environment that has an older
  commit live because its tag was moved back, and is successful there, leaves the watermark where it is.
- **Except after a restore.** When the live commit was put there by a
  [RestoreActiveCommit](../../advanced-usage/rolling-back.md) and is behind the watermark, the watermark is pulled back
  to it, whatever the active commit statuses say. A restore states that the commits after it should not have been
  trusted, so dependents must not keep taking them. Tags that already moved are not moved back; the gate holds those
  promotions until the environment qualifies the commit again. The restore is recognized from the
  `Promoter-restored-from` marker on the restore commit, so this does not depend on the RestoreActiveCommit still
  existing. A rollback made by hand (a revert pushed to the active branch) is not recognized as a restore.
- **Dropped after a force-push.** A watermark that is no longer reachable from the DRY branch HEAD is discarded.
- **No-ops.** A note-only hydration changes the live DRY SHA without changing the hydrated commit or resetting any
  active commit status, so a successful environment qualifies the new commit on the reconcile that observes the note.
- **An environment with no active commit statuses** qualifies a commit as soon as it is live.

#### How a historical commit is checked

The controllers that report active commit statuses (Argo CD health, timers, your own) only ever report on the commit
that is live. Once an environment moves on, nothing can be asked about the commit it left. So this controller does not
look statuses up later. It evaluates them **while the commit is live** and persists the verdict:

- **`status.environments[].qualified`**, which the gate and the UI read.
- **A git note on the hydrated repository**, under `refs/notes/promoter.qualified` on the active hydrated commit that
  was live at the time. The note holds one entry per environment, keyed by the environment's branch (and
  `<branch>:<activePath>` when the environment has an `activePath`), so environments that share an active branch do
  not overwrite each other. An entry records the DRY SHA, when it qualified, and a snapshot of each active commit
  status key and phase that made up the verdict. It is written once per qualification, and the controller does not
  act on a newly qualified commit until the note is pushed. A restore is the exception: the watermark is pulled back
  even when the note cannot be written, so that a failed push never leaves dependents free to take a commit the
  restore took back. When `status` has no entry for an environment (a new or recreated gate), the controller rebuilds
  the watermark by walking the active branch first-parent, up to 200 commits, and taking the newest entry for that
  environment.

When a tag later points at an old commit `X`, the gate does not need a record for `X` itself. It asks whether `X` is the
upstream's watermark or an ancestor of it, which is one ancestry check against the DRY repository. Commits the
upstream skipped, and commits it ran but left before its checks finished, are covered by the next commit it did
qualify.

Two limits follow from evaluating live:

- A commit is qualified only if the controller observed every active status passing while the commit was live. If an
  environment passes its checks and is promoted again within the second or two it takes that state to reach this
  controller, the observation is lost. The next qualification covers the missed commit.
- The verdict reflects the active commit statuses configured at the time. Adding a status to the PromotionStrategy
  later does not re-evaluate what is already qualified.

> [!NOTE]
> The record is one note holding the verdict, rather than a note per CommitStatus holding the inputs. Separate notes
> cannot show that the statuses passed at the same time, they do not carry the DRY SHA (a CommitStatus knows only the
> hydrated commit), and a no-op hydration changes no CommitStatus, so there would be nothing to write.

> [!NOTE]
> The qualified record uses its own notes ref rather than extending the promotion-history note
> (`refs/notes/promoter.history`). The ChangeTransferPolicy controller treats an existing promotion-history note on a
> merge commit as "already written" and skips its own write, so a qualification landing first would drop the promotion
> history for that commit.

### 3. Advance tags

For each environment the controller moves the tag of (`advance: WhenIdle`, the default, or `advance: Immediately`),
it computes where the tag should be:

| Environment | Tag should point at |
|-------------|---------------------|
| Root (no upstreams) | HEAD of the DRY branch |
| Dependent | The newest commit that is at or behind the watermark of **every** direct `dependsOn` upstream: the merge base of those watermarks |

It pushes the tag when the target is a descendant of the tag's current commit. Otherwise it leaves the tag alone: a
tag is never moved backward, and a tag someone pushed ahead of what upstream has qualified stays there (the gate holds
the promotion until upstream catches up). A dependent with an upstream that has qualified nothing has no target.

**When the tag moves.** With `advance: WhenIdle` the controller moves a tag forward only while the environment is
**idle**, so that the environment finishes what it is promoting before it is given the next commit. An environment is
idle when all of these hold:

- its hydrator has processed the commit the tag points at (a [no-op hydration](#picking-up-no-op-hydrations) counts);
- nothing is waiting to be promoted: the proposed and active DRY commits are the same, and no promotion pull request
  is open;
- no active commit status is `pending`.

A `failure` counts as idle, so that a fix can roll forward. So does an environment that a
[RestoreActiveCommit](../../advanced-usage/rolling-back.md) is blocking: what is proposed there will not merge, and
the commit that fixes it has to be hydrated before anyone can lift the block for it.

While the environment is busy, the tag stays where it is and `status.environments[].held` names the commit that is
waiting and what the controller is waiting for. When the environment becomes idle, the tag moves to the newest
target, skipping the commits in between.

With `advance: Immediately` the controller does not look at the environment's own state. An open promotion PR is
updated by the next hydration, and active checks that are still running do not delay the move. An environment that
receives commits faster than it can finish promoting them then never qualifies any, and the environments after it
get nothing. Use it only for an environment whose promotions finish well within the time between commits, or where
running the newest commit matters more than qualifying one.

Environments with `advance: Manual` are skipped.

**Missing tags.** A missing tag is created for every environment, including manual ones, at the DRY commit that is
live in the environment, so the first hydration from the tag changes nothing. When nothing is live, or what is live is
not on the DRY branch, the tag is created at the DRY branch HEAD. A tag the controller moves is created directly at its
target when the target is ahead of the live commit; creating a tag never waits for the environment to be idle. If the PromotionStrategy has not reported the environment yet, the
controller reads `hydrator.metadata` from the tip of the active branch itself. It does not wait, because on a new
setup the hydrator needs the tag before it can create the proposed branch.

**After a force-push to the DRY branch.** A tag that is no longer reachable from the DRY branch HEAD has no "forward".
A tag the controller moves is reset to its target, without waiting for the environment to be idle. An
`advance: Manual` tag is left alone, and the gate reports that it is not on the DRY branch.

**Pushes.** Each tag is pushed on its own, with a lease on the commit the controller read for it at the start of the
reconcile. A tag someone moved in between is not overwritten; the next reconcile re-evaluates. One rejected tag does
not hold back the others. (GitHub also sends no push event when more than three tags are pushed at once.)

### 4. Gate

For each environment `E` with a pending promotion, let `X` be its effective proposed DRY SHA. The controller writes
the ordering `CommitStatus` on the proposed hydrated commit:

| Check | Passes when | `pending` description |
|-------|-------------|-----------------------|
| Tag | `E`'s tag exists | `Waiting for tag "promoter/environment/prod" to be created` |
| Hydration | `X` is the commit `E`'s tag points at | `Waiting for hydrator to process tag "promoter/environment/prod" at a1b2c3d (currently f6e5d4c)` |
| On the DRY branch | the tagged commit is reachable from the DRY branch HEAD | `Tag "promoter/environment/prod" at a1b2c3d is not on dry branch "main"` |
| Qualification | for every direct upstream `U`, `X` is `U`'s watermark or an ancestor of it | `Waiting for "environment/test" to qualify dry a1b2c3d (newest qualified: f6e5d4c)` |

The checks run in that order and the description is the first one that fails. The gate is `success` when all pass.
Roots have no upstreams, so qualification is not checked for them.

Both checks run for every environment regardless of `advance`. For a tag the controller moved, qualification holds by
construction. For a tag a person moved, and for a hydrator that is misconfigured to follow the DRY branch instead of
the tag, it is the check that keeps an unqualified commit from merging.

There is no no-op recursion into an upstream's own upstreams, unlike DependentsSuccessfulCommitStatus. An upstream
qualifies a commit only after it has observed that commit, whether through a new hydrated commit or a note.

When there is no proposed change for `E`, the controller skips evaluation, keeps the last child `CommitStatus`, and
mirrors it onto `status.environments[]`, matching the other ordering gate.

### Picking up no-op hydrations

SCMs send no webhook when a git note is pushed, so a note-only hydration is invisible until the ChangeTransferPolicy
next fetches notes. When an environment's effective proposed DRY SHA differs from its tag, the controller asks that
environment's ChangeTransferPolicy to reconcile, with backoff, until the two agree.

The backoff starts at 15 seconds and doubles up to 2 minutes for as long as the tag stays where it is.

This replaces the PromotionStrategy controller's existing nudge, which compares sibling environments and assumes they
all converge on the same DRY commit. With environment tags they do not, so that comparison is turned off for a
PromotionStrategy whose ordering gate is a DependentsQualifiedCommitStatus.

A commit that is a no-op for several environments in a row moves through them one at a time: tag push, hydration, note,
qualification, next tag. Each hop costs a hydrator cycle and a note fetch, where today all environments hydrate HEAD at
once.

### Webhooks

The [webhook receiver](../../getting-started.md#scm-provider-configuration) routes a push to the DRY branch, or to one of the gate's environment
tags, to the gate. It finds the gate by the push's "before" SHA, which must be the DRY branch HEAD or the tag commit
the gate last recorded in `status`. A push it cannot match (a tag that was just created, or a gate whose status is
behind) is picked up on the requeue interval instead.

Tag pushes arrive as `refs/tags/<name>` from GitHub, GitLab, Forgejo, Gitea, and Bitbucket Cloud. Make sure the
webhook you configured for the DRY repository sends tag push events as well as branch pushes.

## Spec

### `spec.promotionStrategyRef`

Required. The PromotionStrategy whose environments this gate orders. Its `name` must match the PromotionStrategy
whose `orderCommitStatusRef` points here.

### `spec.key`

Required. The gate name the PromotionStrategy controller injects onto every `ChangeTransferPolicy`'s
`proposedCommitStatuses`. A common value is `dependents-qualified`. Lowercase alphanumeric with hyphens, 1–63
characters.

### `spec.drySource`

Required.

| Field | Required | Notes |
|-------|----------|-------|
| `branch` | yes | The DRY branch the root environments follow, for example `main`. |
| `gitRepositoryRef` | no | A [GitRepository](../../crd-specs.md#gitrepository) for the DRY source. Defaults to the PromotionStrategy's `gitRepositoryRef`, which is right when the DRY branch and the hydrated branches share a repository. |

The controller reads the DRY commit graph from this repository to resolve ancestry, and pushes environment tags to it.
The credentials behind the GitRepository need permission to create and update tags.

### Environment tags

The tag an environment hydrates from is not configurable. It is derived from the environment on the
PromotionStrategy:

| Environment | Tag |
|-------------|-----|
| no `activePath` | `promoter/<active branch>` |
| with an `activePath` (its own, or the PromotionStrategy's) | `promoter/<active branch>/<activePath>` |

For example `promoter/environment/prod`, or `promoter/environment/prod/apps/checkout` for `activePath: apps/checkout`.
The hydrator's configuration for the environment must name that tag. The gate reports the name in
`status.environments[].tag.name`.

Appending the `activePath` gives PromotionStrategies that share an active branch their own tags, as it gives them
their own proposed branches. The
[`activePath` naming rules](../../structuring-your-repos.md#what-an-activepath-may-contain) keep the result a valid
tag name under `promoter/`.

Two gates must not manage the same tag. The tag is the active branch and the `activePath` joined with a `/`, so
`environment/prod` with `activePath: apps/checkout` and `environment/prod/apps` with `activePath: checkout` both give
`promoter/environment/prod/apps/checkout`, as do two environments with the same active branch and `activePath`. Within
one hydrated repository that does not happen between correctly configured PromotionStrategies. It takes
PromotionStrategies whose hydrated branches are in different repositories and whose gates point
`drySource.gitRepositoryRef` at one DRY repository, or two gates for one PromotionStrategy.

The controller compares the gates of a namespace. When two of them have a tag in common in the same GitRepository, the
gate that was created later sets `Ready` to `False` with reason `TagConflict`, and reads and moves no tag until the
conflict is gone. The older gate carries on. Give the environments different names, or their own DRY repositories.

The comparison is by GitRepository resource. It does not see two GitRepository resources that describe the same
repository, or gates in another namespace or cluster. Such gates would move each other's tags.

### `spec.environments`

Optional. Settings for individual environments. An environment of the PromotionStrategy with no entry gets the
defaults; an entry for a branch the PromotionStrategy does not have sets `Ready` to `False`.

| Field | Required | Notes |
|-------|----------|-------|
| `branch` | yes | The environment's active branch, matching `PromotionStrategy.spec.environments[].branch`. |
| `advance` | no | `WhenIdle` (default): the controller moves the tag once the environment has finished what it is promoting. `Immediately`: the controller moves the tag as soon as there is a newer commit for it. `Manual`: the controller creates the tag if it is missing and never moves it. See [Advance tags](#3-advance-tags). |

### `spec.publishTo`

Set `publishTo: []` to keep the generated CommitStatus in Kubernetes only. The default, `[scm]`, posts it to the SCM.
See [Where a status is published](../index.md#where-a-status-is-published-publishto).

### `spec.url`

Optional Go template for the SCM details URL on each per-environment gate `CommitStatus`. Same variables and options
as [DependentsSuccessfulCommitStatus](dependents-successful-commit-status.md#commit-status-url-template), with
`.DependentsQualifiedCommitStatus` in place of `.DependentsSuccessfulCommitStatus`.

### Full example

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsQualifiedCommitStatus
metadata:
  name: demo
spec:
  key: dependents-qualified
  promotionStrategyRef:
    name: demo
  drySource:
    branch: main
    gitRepositoryRef:
      name: demo-dry
  environments:
    - branch: environment/prod
      advance: Manual
  publishTo: [scm]
  url:
    template: "https://promoter.example.com/promotion-strategies/{{ .PromotionStrategy.Name }}?env={{ urlQueryEscape .Environment }}"
```

## Wiring the gate into the PromotionStrategy

Set `orderCommitStatusRef` on the PromotionStrategy to `kind: DependentsQualifiedCommitStatus`. The resolution contract
(`spec.key` and `spec.promotionStrategyRef.name`, typed `Get` for in-tree kinds) is the one described in
[DependentsSuccessfulCommitStatus > Wiring the gate](dependents-successful-commit-status.md#wiring-the-gate-into-the-promotionstrategy).
A PromotionStrategy uses exactly one ordering gate. To switch an existing PromotionStrategy, follow
[Adopting environment tags](../../advanced-usage/holding-back-environments.md#adopting-environment-tags-on-an-existing-promotionstrategy):
the tags must exist and the hydrator must follow them before the gate can pass.

> [!IMPORTANT]
> On first install the qualified history is empty. An environment that is successful qualifies its live commit on the
> first reconcile, so a switch on a healthy, converged pipeline is immediate. Nothing an environment ran before the
> gate was installed is on record. The controller does not backfill from ChangeTransferPolicyHistory in v1.

## Status

`status.dry.sha` is the DRY branch HEAD the controller last observed. Each environment branch appears once under
`status.environments[]` (`listMapKey=branch`).

| Field | Always present | Notes |
|-------|----------------|-------|
| `branch` | yes | Environment branch name |
| `tag` | yes | `{name, sha}`: the environment tag and the commit it pointed at when last observed. `sha` is omitted while the tag does not exist. |
| `activeCommitStatuses` | optional | Verbatim copy of `PromotionStrategy.status.environments[].active.commitStatuses`. Omitted when empty. |
| `qualified` | optional | `{drySha, hydratedSha, qualifiedAt}`: the newest DRY commit qualified for this environment. Omitted until the first qualification. |
| `held` | optional | `{sha, message}`: the commit an `advance: WhenIdle` tag will move to once the environment is idle, and what the controller is waiting for. Omitted when the tag is where the controller wants it. |
| `upstreams` | optional | Direct `dependsOn` upstreams: `[{branch, satisfied, reason}]`. `reason` is set when `satisfied` is false. |

Gate report fields (`phase`, `description`, `url`, `reportedSha`) mirror the child `CommitStatus` spec, following the
[gate `status.environments` standard](../../contributing/developing-a-commitstatus.md#gate-statusenvironments-standard).

### Example

```yaml
status:
  dry:
    sha: f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5
  environments:
    - branch: environment/test
      tag:
        name: promoter/environment/test
        sha: d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3
      activeCommitStatuses:
        - key: argocd-health
          phase: success
      qualified:
        drySha: d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3
        hydratedSha: 0011223344556677889900112233445566778899
        qualifiedAt: "2026-11-12T14:02:11Z"
    - branch: environment/prod
      tag:
        name: promoter/environment/prod
        sha: d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3
      activeCommitStatuses:
        - key: argocd-health
          phase: success
      upstreams:
        - branch: environment/test
          satisfied: true
      phase: success
      description: 'Tag "promoter/environment/prod" at d4c3b2a is hydrated and qualified upstream'
      reportedSha: abc123def4567890abcdef1234567890abcdef12
```

## Conditions

In addition to the [standard conditions](../../crd-specs.md#status-conditions), the controller reports:

| Reason | Meaning |
|--------|---------|
| `CommitStatusesNotReady` | A child CommitStatus is not Ready. |
| `DryRepositoryUnavailable` | The controller could not read the DRY repository to observe HEAD, read tags, or resolve ancestry. Child CommitStatuses keep their last phase and no tag is moved. |
| `TagConflict` | An older gate in the namespace manages one of this gate's tags in the same GitRepository. See [Environment tags](#environment-tags). The gate moves no tag and leaves its child CommitStatuses as they are; it checks again after a minute. |
| `TagPushFailed` | A tag push was rejected, for example by tag protection or missing permission. The message names the tag. The other tags are still managed, and the push is retried after a minute. |

## Lifecycle

- **Deleting the gate** removes its child CommitStatuses. The tags stay where they are and nothing moves them, so
  every environment keeps hydrating the commit its tag points at. Point the hydrator back at the DRY branch, or move
  the tags yourself, when you remove the gate.
- **Removing an environment, or changing its active branch or `activePath`,** leaves the old tag in the DRY
  repository. The controller never deletes a tag.
- **Recreating the gate** rebuilds each environment's watermark from the qualification notes on its active branch.

## Comparison with DependentsSuccessfulCommitStatus

| | DependentsSuccessfulCommitStatus | DependentsQualifiedCommitStatus |
|-|----------------------------------|---------------------------------|
| Each environment hydrates from | the DRY branch | its own tag |
| Promotion candidate for a dependent | HEAD | the newest commit every upstream has qualified, or whatever a person tagged |
| Upstream must be running the target | now | at some point in the past |
| Upstream must be successful | now, for the target | at some point while running the target or a descendant (latched) |
| A new DRY commit while a higher environment waits | replaces the candidate; the higher environment waits for lower environments again | leaves the higher environment's candidate alone |
| Holding an environment at a chosen version | no | yes (`advance: Manual`) |
| Needs write access to the DRY repository | no | yes, to push tags |
| Hydrator must write git notes for no-ops | recommended | required |
| State | none | status + git notes |

Use DependentsSuccessfulCommitStatus when release-latest suits you and you want the strictest live check. Use
DependentsQualifiedCommitStatus when higher environments release on a slower cadence than lower ones, or when someone
needs to choose what a higher environment gets and when.
