# Validated Commit Status Controller

> [!NOTE]
> This gate is new. It is covered by the controller's own tests, which play the hydrator and the SCM. Before relying
> on it, check two things in a non-production PromotionStrategy: that your hydrator hydrates again when a tag it
> follows moves, and that Promoter's credentials are allowed to create and move your environment tags.

The Validated Commit Status controller is an **ordering gate that manages one git tag per environment**. Each
environment's hydrator reads its own tag in the DRY repository instead of the DRY branch, and the controller moves a
tag only to a commit that is **validated** for that environment:

- **automatically**, once every environment it depends on has run the commit, or a newer one that contains it,
  [successfully](../index.md#environment-success); or
- **by a person or CI**, with a [CommitValidation](#with-a-commitvalidation) or a
  [pushed git note](#by-pushing-a-git-note).

It is an alternative to the default ordering gate,
[DependentsSuccessfulCommitStatus](dependents-successful-commit-status.md). With that gate every environment hydrates
the HEAD of the DRY branch, so a new DRY commit replaces the pending promotion of every later environment with a commit
that has not cleared the earlier ones yet. With this gate a later environment keeps the commit that was validated for
it until a newer one has cleared the earlier environments. See
[How does GitOps Promoter handle concurrent releases?](../../faqs.md#how-does-gitops-promoter-handle-concurrent-releases)

Use it when you want any of the following:

- A later environment's pending promotion should not be reset by commits that are still on their way to it.
- A person, or CI, should approve a commit for an environment **before** anything is hydrated for it.
- A hotfix should go to one environment without going through the ones before it.

[Holding Back Environments](../../advanced-usage/holding-back-environments.md) introduces the model. This page is the
reference.

> [!IMPORTANT]
> A validation written by a person passes the gate without the checks on the earlier environments. By default anyone
> who can push to the DRY repository can write one. See
> [Only through CommitValidations](#only-through-commitvalidations) and
> [What to know before you use it](#what-to-know-before-you-use-it).

## How it works

| Piece | What it is |
| --- | --- |
| Environment tag | A lightweight tag in the DRY repository: `<environment branch>-tag`, for example `environment/prod-tag`. The environment's hydrator reads it. |
| Qualification | The controller's memory of what each environment has run successfully: one commit per environment, which stands for every commit before it on the DRY branch. |
| Validation | A person's word that a DRY commit may deploy to an environment. It is one line in a git note on the DRY commit, under `refs/notes/promoter.validations`. |
| Ordering commit status | On each environment's proposed commit: `success` when the commit was hydrated from the environment's tag, and the tagged commit is qualified by the earlier environments or validated by a person. |

In a pipeline of `dev`, `staging` and `prod` where `prod` is validated by a person:

1. A commit is pushed to the DRY branch. `dev` depends on nothing, so its tag follows the HEAD of the DRY branch. The
   `dev` hydrator hydrates the commit and the promotion merges.
2. `dev` runs the commit and every active commit status passes: `dev` has **qualified** it. The controller moves the
   `staging` tag there. The `staging` hydrator hydrates it and the promotion merges.
3. `staging` qualifies the commit. `prod` is validated by a person, so the controller reports the commit as ready
   (`upstreamCleared`) and moves nothing. When someone validates a commit, the `prod` tag moves to it, and `prod` is
   hydrated and promoted.

A new DRY commit pushed while step 2 or 3 is under way moves the `dev` tag only.

## Example configuration

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
    kind: ValidatedCommitStatus
    name: demo
  environments:
    - branch: environment/dev
    - branch: environment/staging
    - branch: environment/prod
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: ValidatedCommitStatus
metadata:
  name: demo
spec:
  key: validated
  promotionStrategyRef:
    name: demo
  drySource:
    branch: main
  # Optional. Environments that are not listed use validation Automatic and advance Immediately.
  environments:
    - branch: environment/staging
      advance: WhenIdle
    - branch: environment/prod
      validation: Manual
```

The PromotionStrategy controller injects `spec.key` onto every `ChangeTransferPolicy`; you do not declare the key in
`proposedCommitStatuses`. The dependency graph comes from the PromotionStrategy, as it does for
[DependentsSuccessfulCommitStatus](dependents-successful-commit-status.md#custom-dependency-graph): a linear chain in
list order, or `spec.environments[].dependsOn` when any environment sets it.

Use one `ValidatedCommitStatus` per PromotionStrategy. If several reference the same strategy, the one named in
`orderCommitStatusRef` moves the tags, or else the oldest, and the others report `Ready=False` with reason
`EnvironmentTagsOwnedByAnotherGate`.

### Pointing the hydrators at the tags

Each environment's hydrator must read that environment's tag. A hydrator that still reads the DRY branch produces
proposals that never match the tag, and nothing merges.

The tag is named `<environment branch>-tag`, or `<environment branch>-tag/<activePath>` when the PromotionStrategy or
the environment sets `activePath`. It follows the proposed branch, which is `<environment branch>-next` or
`<environment branch>-next/<activePath>`. `spec.environments[].tag` overrides the name.

For Argo CD's source hydrator, set the DRY source's revision to the tag:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
spec:
  sourceHydrator:
    drySource:
      repoURL: https://github.com/example/app
      path: deploy/prod
      targetRevision: environment/prod-tag   # was: main
    syncSource:
      targetBranch: environment/prod
      path: deploy/prod
    hydrateTo:
      targetBranch: environment/prod-next
```

A [custom hydrator](../../advanced-usage/custom-hydrator.md) hydrates the commit the tag points at, and runs when the
tag is pushed.

**Your hydrator must report no-op hydrations.** A DRY commit often changes nothing for an environment. The hydrator
then has no commit to push, and must record the DRY SHA it processed in a
[git note](../../advanced-usage/custom-hydrator.md#advanced-with-git-notes) on the proposed branch tip. The Argo CD
source hydrator does this. Without the note the environment never reports the tagged commit, never qualifies it, and
every environment after it waits until a later commit changes that environment's manifests.

## Spec

### `spec.promotionStrategyRef`

Required. The PromotionStrategy whose environments this gate orders.

### `spec.key`

Required. The gate name the PromotionStrategy controller injects onto every `ChangeTransferPolicy`'s
`proposedCommitStatuses`. A common value is `validated`. Lowercase alphanumeric with hyphens, 1–63 characters.

### `spec.drySource`

Required.

| Field | Required | Notes |
|-------|----------|-------|
| `branch` | yes | The DRY branch, for example `main`. An environment with no upstream environments and `validation: Automatic` follows its HEAD, and only commits on it are qualified. |
| `gitRepositoryRef` | no | A [GitRepository](../../crd-specs.md#gitrepository) for the DRY source. Defaults to the PromotionStrategy's `gitRepositoryRef`, which is right when the DRY branch and the hydrated branches share a repository. |

The controller reads the DRY commit graph from this repository, pushes the environment tags to it, and reads and
writes the validation notes there. The credentials behind the GitRepository need permission to create and move tags,
and to push `refs/notes/promoter.validations`.

### `spec.environments`

Optional. Lists the environments that differ from the defaults. Every `branch` must be an environment of the
PromotionStrategy.

| Field | Default | Notes |
|-------|---------|-------|
| `branch` | | Required. The environment's active branch. |
| `validation` | `Automatic` | Who validates DRY commits for the environment. See below. |
| `advance` | `Immediately` | When the controller may move the tag. See below. |
| `tag` | `<branch>-tag[/<activePath>]` | Overrides the name of the tag. Set it when the default name is taken: several PromotionStrategies with the same environment branches that share one DRY repository need a tag each. Every environment needs its own tag. |

#### `validation`

- **`Automatic`** (default): the controller moves the tag to the newest commit every environment this one depends on
  has qualified. For an environment that depends on none, that is the HEAD of the DRY branch.
- **`Manual`**: a person or CI moves the tag, with a [validation](#validating-a-commit) or by
  [pushing the tag](#tags-pushed-by-hand). The controller never moves it on its own, and reports what is ready in
  `status.environments[].upstreamCleared`.

A validation is also accepted for an `Automatic` environment. That is how a hotfix or a rollback is sent to one.

The setting can be changed while the gate is in use. From `Automatic` to `Manual`, the tag stays where it is. From
`Manual` to `Automatic`, the controller moves the tag to what is ready for the environment right away.

#### `advance`

- **`Immediately`** (default): the controller moves the tag as soon as there is a newer commit to move it to.
- **`WhenIdle`**: only while the environment is idle, so that a promotion that is under way is not replaced by the
  next one. An environment is idle when its hydrator has processed the commit its tag points at, nothing is proposed,
  no promotion pull request is open, and no active commit status is pending. A failed commit status counts as idle,
  so that a fix can roll forward. Until then the commit is reported as `held` with reason `Busy`.

`WhenIdle` matters for an environment whose promotions take long, or wait for a person to merge them. Without it, an
environment that receives commits faster than it can finish promoting them never qualifies any, and the environments
after it wait.

A validation by a person always applies immediately, which is why `validation: Manual` with `advance: WhenIdle` is
rejected.

### `spec.pushedValidations`

`Accept` (default) or `Ignore`. See [Only through CommitValidations](#only-through-commitvalidations).

### `spec.publishTo`

Set `publishTo: []` to keep the generated CommitStatus in Kubernetes only. The default, `[scm]`, posts it to the SCM.
See [Where a status is published](../index.md#where-a-status-is-published-publishto).

### `spec.url`

Optional Go template for the SCM details URL on each per-environment gate `CommitStatus`. Same variables and options
as [DependentsSuccessfulCommitStatus](dependents-successful-commit-status.md#commit-status-url-template), with
`.ValidatedCommitStatus` in place of `.DependentsSuccessfulCommitStatus`.

## Validating a commit

A validation moves the environment's tag to the commit at once, wherever the commit is: ahead of what the earlier
environments have qualified, behind the tag, or on a branch the DRY branch does not contain. The gate then passes the
promotion as soon as the hydrator has hydrated the commit. It does **not** ask whether the earlier environments have
qualified it. `upstreamCleared` shows what they have.

### With a CommitValidation

A [CommitValidation](../../crd-specs.md#commitvalidation) validates commits for one environment of one
PromotionStrategy. Create one per environment that people approve:

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: CommitValidation
metadata:
  name: demo-prod
spec:
  promotionStrategyRef:
    name: demo
  branch: environment/prod       # the environment; cannot be changed
  sha: 4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a   # the DRY commit to validate
  reason: CHG-1234 approved      # optional; copied into the validation
```

To validate a commit, set `spec.sha`:

```bash
kubectl patch commitvalidation demo-prod --type merge \
  -p '{"spec":{"sha":"4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a","reason":"CHG-1234 approved"}}'
```

The controller writes the validation to git and reports the outcome in `status.request`. The tag moves right away.

```yaml
status:
  request:
    sha: 4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a
    phase: Recorded              # Recorded | Rejected
    handledAt: "2026-11-13T15:02:11Z"
  history:                       # the environment's validations, newest first, capped at 20
    - sha: 4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a
      source: Resource           # written through a CommitValidation
      validatedBy: CommitValidation/demo-prod
      reason: CHG-1234 approved
      validatedAt: "2026-11-13T15:02:11Z"
    - sha: 77c3e0a5f4b6d8c2e1f0a9b8c7d6e5f4a3b29be0
      source: Git                # a note pushed directly
      validatedBy: jane@example.com
      reason: hotfix
      validatedAt: "2026-11-10T22:15:48Z"
    - sha: 5d0e7b1c9a3f4e2d8c6b0a1f3e5d7c9b2a4f6e80
      source: External           # not a validation: the tag was pushed to this commit by hand
      validatedBy: ValidatedCommitStatus/demo
      reason: Tag moved by hand
      validatedAt: "2026-11-09T08:30:12Z"
```

- **`Rejected`** means the commit cannot be validated: the repository does not have it, or it shares no history with
  the DRY branch (a hydrated commit, for example). `status.request.message` says which. A commit that is not on the
  DRY branch is accepted.
- **One request at a time.** `spec.sha` cannot be changed until `status.request` reports the current SHA as handled,
  so a second request cannot overwrite one that has not been written yet. The API server rejects the change; try again.
- **`sha` and `reason` are one request.** `reason` cannot be changed on its own.
- **To validate the same commit again**, clear `spec.sha`, wait for `status.request` to be cleared, and set it again.
- **`sha` is optional.** Without it the resource only shows the environment's history.
- **The history has no entries for the controller's own moves** of the tag. Those are not validations, and nothing is
  written to git for them.
- **The PromotionStrategy needs a `ValidatedCommitStatus`.** Until it has one, the request is kept, the resource is
  `Ready=False` with reason `ValidatedCommitStatusNotFound`, and the validation is recorded when one is created.

**Who may validate.** Grant `update` (or `patch`) on the one `CommitValidation` by name:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: validate-demo-prod
rules:
  - apiGroups: ["promoter.argoproj.io"]
    resources: ["commitvalidations"]
    resourceNames: ["demo-prod"]
    verbs: ["get", "update", "patch"]
```

`validatedBy` is the name of the resource. Who changed it is in the Kubernetes audit log.

### By pushing a git note

A validation is one JSON line in the note of the DRY commit, under `refs/notes/promoter.validations`. Only `target`,
the name of the environment's tag, is required:

```bash
git fetch origin refs/notes/promoter.validations:refs/notes/promoter.validations
git notes --ref=promoter.validations append -m '{"target":"environment/prod-tag","reason":"hotfix"}' <dry-sha>
git push origin refs/notes/promoter.validations
```

Skip the `fetch` if the ref does not exist yet. Use `append`, not `add`, so that other lines of the same commit are
kept.

| Field | Meaning |
| --- | --- |
| `target` | The environment's tag name, for example `environment/prod-tag`. Required. |
| `reason` | Free text. |
| `by` | Who validated. Defaults to the author of the notes commit. |
| `at` | When, as an RFC 3339 time. Defaults to the date of the notes commit. |
| `source` | Written by the controllers. Leave it out. |

SCMs send no webhook when a note is pushed, so the controller sees it the next time it reconciles, at the latest after
`validatedCommitStatus.workQueue.requeueDuration` in the
[ControllerConfiguration](../../crd-specs.md#controllerconfiguration). A `CommitValidation` is applied at once.

A validation is **ignored**, with a `ValidationIgnored` event on the `ValidatedCommitStatus`, when its commit is not
in the repository or shares no history with the DRY branch.

### Only through CommitValidations

Branch and tag protection rules do not cover notes refs. With the default, `spec.pushedValidations: Accept`, anyone
who can push to the DRY repository can validate any commit for any environment, and RBAC on `CommitValidation` limits
the Kubernetes path only.

Set **`spec.pushedValidations: Ignore`** to close the other path. The controller then applies a line only when it is
what a `CommitValidation` asks for at the moment: the line carries the id of that resource's current request, and is
for the commit and the environment the resource names. A line pushed by hand is passed over, and so is one that
copies a request's id onto another commit. A request is applied once: pushing its line again later does not apply it
a second time.

## How a tag moves

Each reconcile the controller works out, for every environment, what put its tag where it is
(`status.environments[].tag.source`) and where the tag should be.

| `tag.source` | The tag was placed by | The gate asks the earlier environments? |
| --- | --- | --- |
| `Automatic` | the controller, at a commit the earlier environments have qualified | yes |
| `Seeded` | the controller, when it created the tag | yes |
| `External` | a push of the tag by hand | yes |
| `Manual` | a validation | **no** |

### On its own

For a `validation: Automatic` environment the controller moves the tag to the newest commit every environment it
depends on has qualified (`upstreamCleared`): the merge base of their newest qualified commits. Environments that fan
in need not qualify the same commit, or at the same moment.

- The tag only moves **forward**, to a commit that contains the commit it is on.
- The controller does not wait for the environment to finish what it is doing, unless it
  [advances `WhenIdle`](#advance). An open promotion pull request is updated by the next hydration.
- A commit the tag cannot move to yet is reported in `held`: `Busy` for a `WhenIdle` environment that is not idle,
  `Pinned` when the commit does not contain the commit the tag is on.

### By a validation, or by hand

A [validation](#validating-a-commit) moves the tag to its commit at once, and comes before any move of the
controller's own. Validating the commit the tag is already on makes a validated tag of it without a push.

A person can also [push the tag](#tags-pushed-by-hand). Either way **the tag stays where the person put it** until
the earlier environments qualify something newer than they had when the tag was placed
(`tag.acknowledgedTarget`). Then, on a `validation: Automatic` environment, the controller takes the tag over again
if the newer commit contains the commit the tag is on. If it does not, the commit is `held` as `Pinned`. A tag that
was placed while nothing was qualified yet is taken over as soon as something is.

On a `validation: Manual` environment the tag stays until a person moves it again.

### Tags that do not exist

The controller creates a missing tag at the DRY commit that is live in the environment, so that the first hydration
from the tag changes nothing. For a `validation: Automatic` environment it creates the tag directly at
`upstreamCleared` when that is ahead of what is live, or when nothing on the DRY branch is live.

No tag is created while there is nowhere to put it: nothing is live in the environment, and either a person
validates for it, or the environments it depends on have qualified nothing yet. A tag at the HEAD of the DRY branch
would propose a commit that nobody chose and nothing has run. The environment's hydrator has nothing to read until
then.

A tag that is deleted is put back where it was.

### After a force-push to the DRY branch

Qualifications of commits that are no longer on the DRY branch are dropped. On a `validation: Automatic` environment
a tag that is no longer on the branch is put back on it, at `upstreamCleared`, unless a validation placed it: a
validated tag stays, and is `Pinned`. On a `validation: Manual` environment the tag is left alone, and the gate
reports that it is not on the DRY branch.

### Pushes

Each tag is pushed on its own, with a lease on what the controller read for it at the start of the reconcile. A tag
someone moved in between is not overwritten: their move stands, and the next reconcile takes it from there. One
rejected tag does not hold back the others. Before a tag leaves the commit an open promotion was hydrated from, the
gate on that promotion is set to `pending`, so the promotion cannot merge while the tag is on its way.

A move the repository refuses, for example because of tag protection, sets `Ready=False` with reason `TagPushFailed`
and is tried again after a minute. A validation whose move was refused is kept in `status.environments[].pending`
meanwhile. A newer validation, or a push of the tag by hand, takes its place.

## Qualification

For each environment, if every active commit status is `success`, the DRY commit that is live is **qualified** for
it. The controller keeps one commit per environment, the newest qualified one
(`status.environments[].qualified`). Qualifying a commit qualifies all commits before it on the DRY branch.

- **On the DRY branch.** Only a commit reachable from the HEAD of `spec.drySource.branch` can be qualified. An
  environment running a hotfix from another branch qualifies nothing, so the hotfix does not move on to the next
  environment by itself.
- **Latched.** A later failure does not take a qualification back.
- **Forward only.** An environment that has an older commit live, because its tag was moved back, leaves its
  qualification where it is. Validating an older commit is not a statement about the newer ones.
- **Except after a restore.** When the live commit was put there by a
  [RestoreActiveCommit](../../advanced-usage/rolling-back.md) and is behind the qualified commit, the qualification
  is pulled back to it. A restore says that the commits after it should not have been trusted, so the environments
  after this one must not keep taking them. Tags that already moved are not moved back; the gate holds those
  promotions until the environment qualifies the commit again. A revert pushed to the active branch by hand is not
  recognized as a restore.
- **No-ops.** A hydration that changed nothing for the environment qualifies the new commit as soon as the
  hydrator's note arrives, provided the proposed and the active manifests are identical.
- **An environment with no active commit statuses** qualifies a commit as soon as it is live.

A commit is qualified only if the controller observed every active status passing while the commit was live. If an
environment passes its checks and is promoted again within a second or two, the observation is lost, and the next
qualification covers the missed commit.

Each qualification is also written to a git note on the hydrated commit that was live, under
`refs/notes/promoter.qualified` in the repository of the environment branches. A gate that has no status for an
environment rebuilds the qualification from those notes.

## The gate

For each environment with a pending promotion the controller writes the ordering `CommitStatus` on the proposed
hydrated commit. The checks run in this order, and the description is the first one that fails:

| Check | Passes when | `pending` description |
|-------|-------------|-----------------------|
| Tag | the environment's tag exists | `Waiting for tag "environment/prod-tag" to be created` |
| Hydration | the proposed commit was hydrated from the commit the tag points at | `Waiting for hydrator to process tag "environment/prod-tag" at a1b2c3d (currently f6e5d4c)` |
| Validated | `tag.source` is `Manual`: **the gate passes here**, with `Tag "environment/prod-tag" at a1b2c3d is hydrated and was validated by CommitValidation/demo-prod` | |
| On the DRY branch | the tagged commit is reachable from the DRY branch HEAD | `Tag "environment/prod-tag" at a1b2c3d is not on dry branch "main"` |
| Qualification | every environment this one depends on has qualified the tagged commit, or a newer one that contains it | `Waiting for "environment/staging" to qualify dry a1b2c3d (newest qualified: f6e5d4c)` |

For a tag the controller moved, the last two hold by construction. For a tag a person pushed, and for a hydrator that
is misconfigured to follow the DRY branch, they are what keeps an unqualified commit from merging.

The gate is an ordinary commit status. Someone with permission to merge the promotion pull request in the SCM can
merge it while the gate is `pending`.

When there is no proposed change for an environment, the controller keeps the last child `CommitStatus` and mirrors
it onto `status.environments[]`.

### Picking up no-op hydrations

SCMs send no webhook when a git note is pushed, so a hydration that only wrote a note is invisible until the
ChangeTransferPolicy next fetches notes. When the commit an environment was hydrated from differs from its tag, the
controller asks that environment's ChangeTransferPolicy to reconcile, starting after 15 seconds and backing off to 2
minutes, until the two agree. The PromotionStrategy controller's own nudge, which assumes all environments converge
on one DRY commit, is turned off for a PromotionStrategy ordered by this gate.

### Webhooks

The [webhook receiver](../../getting-started.md#scm-provider-configuration) routes a push to the DRY branch, or to
one of the gate's environment tags, to the gate. It finds the gate by the push's "before" SHA, which must be the DRY
branch HEAD or the tag commit the gate last recorded in `status`. Make sure the webhook you configured for the DRY
repository sends tag push events as well as branch pushes. A push it cannot match, and a pushed note, are picked up
on the requeue interval.

## Hotfixes and the pin

To send a commit to one environment, validate it for that environment. It can be a commit on a hotfix branch that the
DRY branch does not contain. The tag moves there, the gate passes it, and the environment is **pinned**: commits the
earlier environments qualify that do not contain the hotfix are held, and status shows why.

```yaml
status:
  environments:
    - branch: environment/staging
      tag:
        name: environment/staging-tag
        sha: 4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a
        source: Manual
        validatedBy: jane@example.com
      held:
        sha: 9be01d7c3e0a5f4b6d8c2e1f0a9b8c7d6e5f4a3b
        reason: Pinned
        message: Waiting for the dry branch to contain 4f2a9c0
```

The pin is released by either of:

- **Merging the hotfix into the DRY branch** with a merge commit. Once the earlier environments qualify a commit that
  contains the hotfix, the controller moves the tag again.
- **A validation** of any commit.

A squash merge or a cherry-pick creates a new commit that does not contain the hotfix commit, so it does not release
the pin. Validate the resulting commit.

A hotfix that is not on the DRY branch **does not move on to the next environment by itself**: only commits on the
DRY branch are [qualified](#qualification). Validate it for each environment that should get it, or merge it.

## Tags pushed by hand

You can also push a tag yourself:

```bash
git push --force origin <dry-sha>:refs/tags/environment/prod-tag
```

The controller does not put the tag back. It records `tag.source: External`, and the tag
[stays there](#by-a-validation-or-by-hand) until the earlier environments qualify something newer. Unlike a
validation, a pushed tag is **verified**: the gate passes only a commit on the DRY branch that the earlier
environments have qualified. Any commit at or behind what they have qualified passes, including commits they
skipped. A commit they have not reached yet waits for them. On a `validation: Automatic` environment, a tag pushed to
a commit that is not on the DRY branch is put back on it.

To send an environment a commit the gate would hold, validate the commit instead. Validating the commit a pushed tag
is on turns it into a validated tag.

**The push is recorded in git.** When the controller sees a tag that was pushed by hand, it appends a line with
`source: External` to the validation note of the commit. The line validates nothing and never moves a tag. It shows
in the [history](#with-a-commitvalidation) of the environment, with the `ValidatedCommitStatus` that saw the move as
`validatedBy`: git does not say who pushes a tag.

Git treats tags as immutable by convention, which is why the push needs `--force`. A clone that already fetched the
tag will not update it on a plain `git fetch`; use `git fetch --force --tags`. SCM web UIs can create and delete tags
but not move them.

## Rolling back

[RestoreActiveCommit](../../advanced-usage/rolling-back.md) works as it does with any ordering gate, and is the way to
get an earlier version back at once. It also [takes back](#qualification) what the environment had qualified since,
so the environments after it stop taking those commits.

Validating an older commit, or pushing the tag back, also takes an environment back: the tag moves, the hydrator
hydrates the older commit, and it is promoted like any other. It is slower than a restore, and it says nothing about
the newer commits, which the later environments keep. On a `validation: Automatic` environment it holds until the
earlier environments qualify something newer; on a `Manual` one, until a person moves the tag.

## When the gate's status is lost

A `ValidatedCommitStatus` that is deleted and created again takes up from what is in git:

- **What each environment had qualified** is read from the qualification notes on its active branch.
- **Each tag is what the newest line of its commit says it is**: a validated tag, or one pushed by hand. A validated
  tag is still passed by the gate, and both stay where they are until the earlier environments qualify something
  newer. A rollback is not undone.
- **Only validations written after that line are applied.** Nothing written before it is applied again.
- **A tag with no line of its commit was placed by the controller**, which carries on from there. A validation that
  was written and not applied yet when the status was lost is not applied; make it again.

With `pushedValidations: Ignore`, a validated tag is one again only if its line is still what a `CommitValidation`
asks for. Otherwise it is treated as pushed by hand, and verified.

If the validation notes are rewritten, to prune them for example, the controller no longer finds the commit it had
applied them up to (`status.validationsCursor`), and takes its place from the tags in the same way.

## Status

```yaml
status:
  dry:
    sha: d41c07...               # HEAD of spec.drySource.branch when the controller last looked
  validationsCursor: 9a3f11...   # last commit of the validation notes whose lines were applied
  environments:
    - branch: environment/staging
      phase: success             # the ordering commit status on the proposed commit
      description: Tag "environment/staging-tag" at 4f2a9c0 is hydrated and was validated by jane@example.com
      reportedSha: 77aa01...
      tag:
        name: environment/staging-tag
        sha: 4f2a9c...
        source: Manual           # Manual | Automatic | Seeded | External
        movedAt: "2026-11-13T15:02:30Z"
        validatedBy: jane@example.com
        acknowledgedTarget: 7c1e55...
      qualified:                 # newest commit this environment has run successfully
        drySha: 7c1e55...
        hydratedSha: 0011aa...
        qualifiedAt: "2026-11-12T14:02:11Z"
      upstreamCleared:           # newest commit every environment this one depends on has qualified
        sha: 9be01d...
      held:                      # only while the controller cannot move the tag to a commit it would
        sha: 9be01d...
        reason: Pinned           # Pinned | Busy
        message: Waiting for the dry branch to contain 4f2a9c0
```

| Field | Meaning |
| --- | --- |
| `tag.source` | What put the tag where it is. See [How a tag moves](#how-a-tag-moves). |
| `tag.validatedBy` | Who validated the commit the tag is on. Only with `source: Manual`. |
| `tag.acknowledgedTarget` | Only for a tag a person placed: what `upstreamCleared` was at the time. The tag stays until `upstreamCleared` is newer than this. |
| `qualified` | The newest commit this environment has [qualified](#qualification). |
| `upstreamCleared` | Where the controller moves an automatically validated tag, and what is ready to validate for a `Manual` environment. For an environment that depends on none it is the HEAD of the DRY branch. Omitted while an environment it depends on has qualified nothing. |
| `held` | A commit the controller would move the tag to and cannot yet. `Pinned`: the commit does not contain the commit the tag is on. `Busy`: the environment advances `WhenIdle` and is not idle. |
| `pending` | A move that a validation asked for and that could not be pushed, with the error. The controller tries again. |
| `appliedRequest` | The id of the last `CommitValidation` request applied to the environment. Its line is not applied a second time. |
| `upstreams` | For a tag the gate verifies: each environment this one depends on, and whether it has qualified the tagged commit. |
| `activeCommitStatuses` | A copy of the PromotionStrategy environment's active commit statuses. |

Gate report fields (`phase`, `description`, `url`, `reportedSha`) mirror the child `CommitStatus` spec, following the
[gate `status.environments` standard](../../contributing/developing-a-commitstatus.md#gate-statusenvironments-standard).

## Conditions

In addition to the [standard conditions](../../crd-specs.md#status-conditions), the controller reports:

| Reason | Meaning |
|--------|---------|
| `CommitStatusesNotReady` | A child CommitStatus is not Ready. |
| `DryRepositoryUnavailable` | The controller could not read the DRY repository. Child CommitStatuses keep their last phase and no tag is moved. |
| `TagPushFailed` | A tag push was refused, for example by tag protection. The message names the tag. The other tags are still managed, and the push is retried after a minute. |
| `ValidationNotRecorded` | The controller could not push the record of a tag pushed by hand to `refs/notes/promoter.validations`. It tries again. |
| `EnvironmentTagsOwnedByAnotherGate` | Another `ValidatedCommitStatus` references the same PromotionStrategy and moves its tags. |

The controller also emits [events](../../monitoring/events.md#validatedcommitstatus) when an environment qualifies a
commit, when it moves a tag, finds one pushed by hand, or cannot push one, and when it ignores a validation.

## Setup and migration

On a new PromotionStrategy, create the `ValidatedCommitStatus`, reference it from `orderCommitStatusRef`, and point
the hydrators at the tags.

To move a PromotionStrategy that uses `DependentsSuccessfulCommitStatus`:

1. **Create the `ValidatedCommitStatus`**, with a `spec.key` that differs from the old gate's. Leave
   `orderCommitStatusRef` alone. The controller creates each environment's tag at the DRY commit that is live in the
   environment, so the tags start out describing what is deployed, and credits each environment with the commit it
   is running.
2. **Point each hydrator at its tag.**
3. **Switch `orderCommitStatusRef`** to the `ValidatedCommitStatus` and delete the `DependentsSuccessfulCommitStatus`.

Do steps 2 and 3 together. Between them the old gate holds later environments at `pending`, which is safe but stalls
promotion.

> [!WARNING]
> The controller moves tags from the moment the `ValidatedCommitStatus` exists, whether or not the PromotionStrategy
> references it yet. Meanwhile the old gate keeps promoting. Before step 2, compare each tag with what is live in its
> environment:
>
> ```bash
> git ls-remote origin 'refs/tags/environment/*-tag'
> kubectl get promotionstrategy demo -o jsonpath='{range .status.environments[*]}{.branch}{"\t"}{.active.dry.sha}{"\n"}{end}'
> ```
>
> A tag that is behind what is live rolls its environment back as soon as the hydrator reads it. Expect the tags of
> `Manual` and `WhenIdle` environments to be behind: nobody moves the tag of a `Manual` environment before the
> switch, and a `WhenIdle` environment is hardly ever idle while its hydrator does not read its tag. Push the tag to
> the live commit, or validate it, to bring such an environment up to date, then switch.

Also note:

- **Give the two gates different keys** while both exist. A `ChangeTransferPolicy` that finds two commit statuses
  with one key on a commit stops promoting.
- **The new gate reports on proposals from step 1 on.** It reports `pending` on a proposal hydrated from the DRY
  branch that does not match the tag. Nothing reads that status until step 3, but it is posted to the SCM. Set
  `publishTo: []` until then if that is unwanted.
- **There is no record of what an environment ran before the gate existed.** An environment that is successful when
  the gate is created qualifies its live commit on the first reconcile.

To back out, point the hydrators at the DRY branch again and switch `orderCommitStatusRef` back. Deleting the gate
removes its child CommitStatuses and leaves the tags and the notes; nothing else reads them.

## Troubleshooting

| What you see | What it means |
| --- | --- |
| The gate is `pending` with `Waiting for hydrator to process tag ... (currently ...)` | The proposed commit was hydrated from another DRY commit than the tag points at. Normal for a moment after a tag moves. If it stays, the environment's hydrator is not reading the tag, or does not write a note for a hydration that changed nothing. |
| The gate is `pending` with `Waiting for tag ... to be created` | Nothing was live in the environment when the gate was created, and nothing has been validated or qualified for it yet. See [Tags that do not exist](#tags-that-do-not-exist). |
| The gate is `pending` with `Waiting for "<environment>" to qualify dry ...` | The tag was pushed by hand to a commit an earlier environment has not qualified. Wait, or validate the commit. |
| The gate is `pending` with `Tag ... is not on dry branch ...` | The tag of a `Manual` environment points at a commit that is not on the DRY branch, and no validation put it there. Validate the commit, or move the tag. |
| `held.reason: Pinned` | The environment is on a commit that what is ready for it does not contain. See [Hotfixes and the pin](#hotfixes-and-the-pin). |
| `held.reason: Busy` | The environment advances `WhenIdle` and has a promotion under way. |
| An environment after a slow one never advances | The slow environment gets a new commit before it has passed its checks on the last one, and never qualifies any. Give it `advance: WhenIdle`. |
| `Ready=False`, reason `TagPushFailed` | The repository refused a tag push. The condition's message, or `status.environments[].pending.message`, has git's answer. |
| `tag.source: External` that you did not expect | Someone, or something, pushed the tag. |
| A validation did nothing | Look for a `ValidationIgnored` event on the `ValidatedCommitStatus`, and check `spec.pushedValidations`. |

## What to know before you use it

- **A validation skips the earlier environments.** That is its purpose. With `pushedValidations: Accept`, push access
  to the DRY repository is the right to deploy any commit to any environment, because a validation note cannot be
  protected. Use `pushedValidations: Ignore` and RBAC on `CommitValidation` to decide who may.
- **Pushing a tag is a deploy too**, of a commit the earlier environments have qualified. Whoever can push an
  environment's tag can take that environment to any such commit, including an older one. Protect the tags in the
  SCM if that is too much.
- **The controller's identity must be allowed to move the tags.** If the repository protects tags, exempt
  `environment/*-tag` (or your equivalent) for the identity in the `ScmProvider`. It also pushes to
  `refs/notes/promoter.validations`.
- **Nothing is rendered for an approver to look at.** A `Manual` environment is not hydrated until its tag moves, so
  the approver sees the DRY change and the results of the earlier environments, not the manifests for this one. The
  promotion pull request appears after validation.
- **Environments can run different commits for longer.** A later environment may be promoting a commit that the
  earlier ones have already moved past. Compare an environment against its tag, not against its siblings.
- **The validation notes are never pruned.** They grow by a line per validation and per tag pushed by hand.
- **Commits off the DRY branch on some git servers.** The controller fetches a validated commit by its SHA. Hosted
  SCMs serve any commit that way. A self-hosted server that only serves commits a ref points at will not find a
  commit that is neither on the DRY branch nor the tip of a branch or tag; the validation is then ignored, or
  rejected by a `CommitValidation`.

## Comparison with DependentsSuccessfulCommitStatus

| | DependentsSuccessfulCommitStatus | ValidatedCommitStatus |
|-|----------------------------------|-----------------------|
| Each environment hydrates from | the DRY branch | its own tag |
| Promotion candidate for a later environment | HEAD | the newest commit the earlier environments have qualified, or what a person validated or tagged |
| Earlier environment must be running the candidate | now | at some point in the past |
| Earlier environment must be successful | now, for the candidate | at some point while running the candidate or a commit that contains it |
| A new DRY commit while a later environment waits | replaces the candidate | leaves the later environment's candidate alone |
| Approving a commit for an environment before it is hydrated | no | yes (`validation: Manual`) |
| Sending a commit to one environment past the others | no | yes (a validation) |
| Needs write access to the DRY repository | no | yes, to push tags and validation notes |
| Hydrator must write git notes for no-ops | recommended | required |
| State | none | status and git notes |

Use DependentsSuccessfulCommitStatus when release-latest suits you and you want the strictest live check. Use
ValidatedCommitStatus when later environments release on a slower cadence than earlier ones, or when someone needs
to choose what an environment gets and when.
