# Validated Commit Status Controller

The Validated Commit Status controller orders promotions with one git tag per environment. Each environment's hydrator
reads its own tag instead of the dry branch, and the controller moves a tag only to a dry commit that was **validated**
for that environment: automatically, once the environments it depends on have succeeded on the commit, or by a person.

It is an alternative to the default ordering gate,
[DependentsSuccessfulCommitStatus](dependents-successful-commit-status.md). With that gate every environment hydrates
the head of the dry branch, so a new dry commit replaces the pending promotion of every later environment with a commit
that has not cleared the earlier ones yet. With this gate a later environment keeps the commit that was validated for
it until a newer one has cleared the earlier environments. See
[How does GitOps Promoter handle concurrent releases?](../../faqs.md#how-does-gitops-promoter-handle-concurrent-releases)

Use it when you want any of the following:

- A later environment's pending promotion should not be reset by commits that are still on their way to it.
- A person, or CI, should approve a commit for an environment **before** anything is hydrated for it.
- A hotfix should go to one environment without going through the ones before it.

> [!IMPORTANT]
> With this gate, pushing an environment's tag, or a validation note, is a deploy. Anyone who can push to the dry
> repository can do both. See [What to know before you use it](#what-to-know-before-you-use-it).

## How it works

| Piece | What it is |
| --- | --- |
| Environment tag | A lightweight tag in the dry repository: `<environment branch>-tag`, for example `environment/prod-tag`. The environment's hydrator reads it. |
| Validation | A record that a dry commit may deploy to an environment. It is one line in a git note on the dry commit, under `refs/notes/promoter.validations`. Git is the record. |
| Ordering commit status | On each environment's proposed commit, `success` when the commit was hydrated from the environment's tag, `pending` otherwise. |

A commit enters an environment only when that environment's tag moves, and a tag moves only when a validation is
applied. In a pipeline of `dev`, `staging` and `prod` where `prod` is validated by a person:

1. A commit is pushed to the dry branch. `dev` has no upstream environments, so its tag follows the head of the dry
   branch. The `dev` hydrator hydrates the commit and the promotion merges.
2. `dev` succeeds on the commit: it is live and every active commit status passes. The controller validates the commit
   for `staging` and moves the `staging` tag. The `staging` hydrator hydrates it and the promotion merges.
3. `staging` succeeds on the commit. `prod` is validated by a person, so the controller reports the commit as ready
   and moves nothing. When someone validates it, the `prod` tag moves and `prod` is hydrated and promoted.

A new dry commit pushed while step 2 or 3 is under way moves the `dev` tag only.

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
  dryBranch: main
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
`orderCommitStatusRef` moves the tags, or else the oldest, and the others report `Ready=False`.

### Pointing the hydrators at the tags

Each environment's hydrator must read that environment's tag. A hydrator that still reads the dry branch produces
proposals that never match the tag, and nothing merges.

The tag name is fixed. It is `<environment branch>-tag`, or `<environment branch>-tag/<activePath>` when the
PromotionStrategy or the environment sets `activePath`. It follows the proposed branch, which is
`<environment branch>-next` or `<environment branch>-next/<activePath>`.

For Argo CD's source hydrator, set the dry source's revision to the tag:

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

### `spec.dryBranch`

The branch dry commits are pushed to. An environment with no upstream environments and `validation: Automatic` follows
its head.

### `spec.environments[].validation`

Who validates dry commits for the environment.

- **`Automatic`** (default): the controller. For an environment with upstream environments, once every one of them has
  [succeeded](#when-the-controller-validates) on the commit. For an environment with none, every new head of the dry
  branch counts as validated.
- **`Manual`**: a person or CI, with a [CommitValidation](#with-a-commitvalidation) or a
  [pushed git note](#by-pushing-a-git-note). The controller writes nothing for the environment and reports what is
  ready in `status.environments[].upstreamCleared`.

A manual validation is also accepted for an `Automatic` environment. That is how a hotfix is sent to one.

### `spec.environments[].advance`

When an automatic validation may move the environment's tag.

- **`Immediately`** (default): as soon as it applies.
- **`WhenIdle`**: only while the environment is idle, so that a promotion that is under way is not replaced. An
  environment is idle when its hydrator has processed the commit its tag points at, nothing is proposed, no promotion
  pull request is open, and no active commit status is pending. A failed commit status counts as idle, so that a fix
  can roll forward.

Manual validations always apply immediately, which is why `validation: Manual` with `advance: WhenIdle` is rejected.

### Keep the status out of the SCM (`publishTo`)

Set **`spec.publishTo: []`** to keep the generated CommitStatus in Kubernetes only. The default, `[scm]`, posts it to
the SCM. Omitting the field uses the default. See
[Where a status is published](../index.md#where-a-status-is-published-publishto).

### Commit Status URL Template

`spec.url.template` sets the details URL on each environment's `CommitStatus`. It works as it does for
[DependentsSuccessfulCommitStatus](dependents-successful-commit-status.md#commit-status-url-template), with these
variables:

- `.Environment`: the environment branch the URL is rendered for
- `.Tag`: the name of that environment's tag
- `.ValidatedCommitStatus`: the whole [ValidatedCommitStatus](../../crd-specs.md#validatedcommitstatus)
- `.PromotionStrategy`: the referenced [PromotionStrategy](../../crd-specs.md#promotionstrategy)

The rendered URL must use `http` or `https`.

## Validating a commit

### With a CommitValidation

A [CommitValidation](../../crd-specs.md#commitvalidation) submits validations for one environment of one
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
  sha: 4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a   # the dry commit to validate
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
      source: Resource           # submitted through a CommitValidation
      validatedBy: CommitValidation/demo-prod
      reason: CHG-1234 approved
      validatedAt: "2026-11-13T15:02:11Z"
    - sha: 9be01d7c3e0a5f4b6d8c2e1f0a9b8c7d6e5f4a3b
      source: Automatic          # the controller, once upstream environments succeeded
      validatedBy: environment/staging successful
      validatedAt: "2026-11-12T09:40:03Z"
    - sha: 77c3e0a5f4b6d8c2e1f0a9b8c7d6e5f4a3b29be0
      source: Git                # a note pushed directly
      validatedBy: jane@example.com
      reason: hotfix
      validatedAt: "2026-11-10T22:15:48Z"
```

- **`Rejected`** has one cause: the commit does not exist in the repository. A commit that is not on the dry branch is
  accepted.
- **One request at a time.** `spec.sha` cannot be changed until `status.request` reports the current SHA as handled,
  so a second request cannot overwrite one that has not been written yet. The API server rejects the change; try again.
- **`sha` and `reason` are one request.** `reason` cannot be changed on its own.
- **To validate the same commit again**, clear `spec.sha`, wait for `status.request` to be cleared, and set it again.
- **`sha` is optional.** Without it the resource only shows the environment's history.
- **If the validation cannot be written**, the controller keeps trying and `spec.sha` stays locked. Deleting the
  resource and creating it again is the way out, and loses nothing: the history is read from git.
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

A validation is one JSON line in the note of the dry commit, under `refs/notes/promoter.validations`. Only `target`,
the name of the environment's tag, is required:

```bash
git fetch origin refs/notes/promoter.validations:refs/notes/promoter.validations
git notes --ref=promoter.validations append -m '{"target":"environment/prod-tag","reason":"hotfix"}' <dry-sha>
git push origin refs/notes/promoter.validations
```

Skip the `fetch` if the ref does not exist yet. Use `append`, not `add`, so that other validations of the same commit
are kept. The commit must have been pushed: a validation of a commit the repository does not have is ignored, with a
`ValidationIgnored` event on the `ValidatedCommitStatus`.

| Field | Meaning |
| --- | --- |
| `target` | The environment's tag name, for example `environment/prod-tag`. Required. |
| `reason` | Free text. |
| `by` | Who validated. Defaults to the author of the notes commit. |
| `at` | When, as an RFC 3339 time. Defaults to the date of the notes commit. |
| `source` | Written by the controllers. Leave it out: a line without one is a manual validation. |

SCMs send no webhook when a note is pushed, so the controller sees it the next time it reconciles, at the latest after
`validatedCommitStatus.workQueue.requeueDuration` in the
[ControllerConfiguration](../../crd-specs.md#controllerconfiguration). A `CommitValidation` is applied at once.

## How a tag moves

Validations are applied in the order they were written, which is the order of the commits of the notes ref, not the
order of their clocks. Only the end result is pushed, so the commits in between are skipped.

- A **manual** validation moves the tag to its commit at once, wherever the commit is: ahead of the tag, behind it, or
  off the dry branch.
- An **automatic** validation moves the tag only to a commit that descends from the commit the tag is on. A commit the
  tag has already passed is ignored.
- With `advance: WhenIdle`, an automatic validation also waits until the environment is idle.

### When the controller validates

The controller validates a commit for an environment once every environment it depends on has **succeeded** on that
commit. An environment has succeeded on a commit when the commit is live in it, or was hydrated for it and changed
nothing, and every active commit status passes.

Environments that fan in need not succeed at the same moment. The controller remembers the last ten commits each
environment succeeded on (`status.environments[].successfulShas`) and validates the newest one they have in common.
It has to be the same commit: two upstream environments that never run the same commit leave the environment that
depends on them waiting.

A validation is not taken back. If an upstream environment starts failing, or is rolled back, the tags of later
environments stay where they are.

### Hotfixes and the pin

To send a commit to one environment, validate it for that environment by hand. It can be a commit on a hotfix branch
that the dry branch does not contain. The tag moves there, and the environment is **pinned**: automatic validations of
commits that do not contain the hotfix are held, and status shows why.

```yaml
status:
  environments:
    - branch: environment/staging
      tag:
        name: environment/staging-tag
        sha: 4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a
        source: Manual
      held:
        sha: 9be01d7c3e0a5f4b6d8c2e1f0a9b8c7d6e5f4a3b
        reason: Pinned
        message: Waiting for the dry branch to contain 4f2a9c0
```

The pin is released by either of:

- **Merging the hotfix into the dry branch** with a merge commit. The next commit validated for the environment then
  contains the hotfix and moves the tag.
- **A manual validation** of any commit.

A squash merge or a cherry-pick creates a new commit that does not contain the hotfix commit, so it does not release
the pin. Validate the resulting commit by hand.

A hotfix validated for one environment moves on to the next like any commit, as long as it descends from the commit the
next environment's tag is on.

### Tags moved by hand

You can also push a tag yourself:

```bash
git push --force origin <dry-sha>:refs/tags/environment/prod-tag
```

The controller does not restrict this and does not put the tag back. It records `tag.source: External`, and the tag
stays there until a newer validation arrives. Validations written before the move are not applied again. On an
`Automatic` environment a tag that was pushed backward moves forward again with the next automatic validation; to keep
an environment on an older commit, use [RestoreActiveCommit](../../advanced-usage/rolling-back.md).

The controller moves tags with a leased push, so it never overwrites a move it has not seen.

### Rolling back

[RestoreActiveCommit](../../advanced-usage/rolling-back.md) works as it does with any ordering gate, and is still the
way to get an earlier version back at once and keep the bad commit out.

Validating an older commit by hand also takes an environment back: the tag moves, the hydrator hydrates the older
commit, and it is promoted like any other. It is slower than a restore, and it does not hold on an `Automatic`
environment, whose tag moves forward again with the next automatic validation. It does hold on a `Manual` one.

## Status

```yaml
status:
  dryBranch:
    sha: d41c07...               # head of spec.dryBranch when the controller last looked
  validationsCursor: 9a3f11...   # last commit of the notes ref whose validations were applied
  environments:
    - branch: environment/staging
      phase: success             # the ordering commit status on the proposed commit
      description: Proposed commit matches environment/staging-tag
      reportedSha: 77aa01...
      tag:
        name: environment/staging-tag
        sha: 4f2a9c...
        source: Manual           # Manual | Automatic | Seeded | External
        movedAt: "2026-11-13T15:02:30Z"
      upstreamCleared:           # newest commit every upstream environment has succeeded on
        sha: 9be01d...
      held:                      # only while an automatic validation is waiting
        sha: 9be01d...
        reason: Pinned           # Pinned | Busy
        message: Waiting for the dry branch to contain 4f2a9c0
      successfulShas:            # dry commits this environment succeeded on, newest first, capped at 10
        - sha: 4f2a9c...
          time: "2026-11-13T15:20:41Z"
```

| Field | Meaning |
| --- | --- |
| `tag.source` | What last moved the tag. `Seeded`: the controller created it. `External`: it was moved by hand. |
| `upstreamCleared` | What is ready to validate for a `Manual` environment. For an environment with no upstream environments it is the head of the dry branch. |
| `held` | An automatic validation that has not been applied. `Pinned`: its commit does not contain the commit the tag is on. `Busy`: the environment advances `WhenIdle` and is not idle. |
| `pending` | A tag move that could not be pushed, with the error. The controller tries again every minute. |

The gate is `Ready=False` with reason `EnvironmentTagPushFailed` while a tag cannot be moved, for example because a tag
protection rule refuses the controller's identity.

## Setup and migration

On a new PromotionStrategy, create the `ValidatedCommitStatus`, reference it from `orderCommitStatusRef`, and point the
hydrators at the tags.

To move a PromotionStrategy that uses `DependentsSuccessfulCommitStatus`:

1. **Create the `ValidatedCommitStatus`**, with a `spec.key` that differs from the old gate's. Leave
   `orderCommitStatusRef` alone. The controller creates each environment's tag at the dry commit that is live in the
   environment, so the tags start out describing what is deployed. Where nothing is live, the tag of an environment
   that follows the dry branch is created at its head, and the others are created by their first validation.
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
> `Manual` and `WhenIdle` environments to be behind: nobody validates for a `Manual` environment before the switch, and
> a `WhenIdle` environment is hardly ever idle while its hydrator does not read its tag. Validate the live commit for
> such an environment to bring its tag up to date, then switch.

Also note:

- **Give the two gates different keys** while both exist. A `ChangeTransferPolicy` that finds two commit statuses with
  one key on a commit stops promoting.
- **The new gate reports on proposals from step 1 on.** It reports `pending` on a proposal hydrated from the dry
  branch that does not match the tag. Nothing reads that status until step 3, but it is posted to the SCM. Set
  `publishTo: []` until then if that is unwanted.
- **A `Manual` environment with nothing live has no tag** until its first validation. Its hydrator has nothing to read
  and its `ChangeTransferPolicy`, and so the PromotionStrategy, are not Ready until then.

To back out, point the hydrators at the dry branch again and switch `orderCommitStatusRef` back. The tags and the notes
can stay; nothing else reads them.

## Troubleshooting

| What you see | What it means |
| --- | --- |
| The gate is `pending` with `Waiting for hydrator to process <tag> at <sha> (currently <sha>)` | The proposed commit was hydrated from another dry commit than the tag points at. Normal for a moment after a tag moves. If it stays, the environment's hydrator is not reading the tag. |
| The gate is `pending` with `Waiting for a validation: <tag> does not exist yet` | Nothing was live in the environment when the gate was created, and no commit has been validated for it. |
| `held.reason: Pinned` | The environment is on a commit the dry branch does not contain. See [Hotfixes and the pin](#hotfixes-and-the-pin). |
| `held.reason: Busy` | The environment advances `WhenIdle` and has a promotion under way. |
| `Ready=False`, reason `EnvironmentTagPushFailed` | The repository refused a tag push. `status.environments[].pending.message` has git's answer. |
| `Ready=False`, reason `ValidationNotRecorded` | The controller could not push to `refs/notes/promoter.validations`. |
| `Ready=False`, reason `EnvironmentTagsOwnedByAnotherGate` | Another `ValidatedCommitStatus` references the same PromotionStrategy and moves its tags. |
| `tag.source: External` that you did not expect | Someone, or something, pushed the tag. The controller's own moves are `Manual`, `Automatic` or `Seeded`. |

The controller also emits [events](../../monitoring/events.md#validatedcommitstatus) when it moves a tag, finds one
moved by hand, or cannot push one.

## What to know before you use it

- **Pushing a tag or a note is a deploy.** Push access to the dry repository becomes the right to deploy to every
  environment. Branch and tag protection rules do not cover notes refs, so a validation note cannot be protected. RBAC
  on `CommitValidation` limits the Kubernetes path only.
- **The controller's identity must be allowed to move the tags.** If the repository protects tags, exempt
  `environment/*-tag` (or your equivalent) for the identity in the `ScmProvider`.
- **Nothing is rendered for an approver to look at.** A `Manual` environment is not hydrated until a commit is
  validated for it, so the approver sees the dry change and the results of the earlier environments, not the manifests
  for this one. The promotion pull request appears after validation.
- **An approval does not depend on the earlier environments.** A manual validation is enough on its own.
  `upstreamCleared` shows whether they had succeeded.
- **Environments can run different commits for longer.** A later environment may be promoting a commit that the
  earlier ones have already moved past.
- **The validation notes are never pruned.** They grow by a line per validation.
- **Commits off the dry branch on some git servers.** The controller fetches a validated commit by its SHA. Hosted
  SCMs serve any commit that way. A self-hosted server that only serves commits a ref points at will not find a commit
  that is neither on the dry branch nor the tip of a branch or tag; the validation is then ignored, or rejected by a
  `CommitValidation`.
