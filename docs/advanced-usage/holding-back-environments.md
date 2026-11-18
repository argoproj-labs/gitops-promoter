# Holding Back Environments

> [!NOTE]
> This feature is new. It is covered by the controller's own tests, which play the hydrator and the SCM. Try it on a
> non-production PromotionStrategy first, and check two things there: that your hydrator hydrates again when a tag it
> follows moves, and that Promoter's credentials are allowed to create and move your environment tags.

By default every environment hydrates from the HEAD of your DRY branch. That is the "release latest" model described in
[FAQs > How does GitOps Promoter handle concurrent releases?](../faqs.md#how-does-gitops-promoter-handle-concurrent-releases),
and it has two well-known costs:

1. **Interruption.** HEAD is always the promotion candidate for every environment. A change that has cleared `dev` and
   `test` and is waiting to go to `prod` is replaced the moment a newer commit lands, and `prod` then waits for the new
   commit to clear every earlier environment. If commits land faster than the earlier environments can verify them,
   `prod` waits indefinitely.
2. **No choosing.** You cannot ship to `dev` and `test` continuously while holding `prod` at a version QA has
   blessed, or send one environment a fix without sending it through the others, except by
   [splitting changes across DRY commits](../structuring-your-changes.md#multiple-dry-commits-intentional-drift),
   which gives up Promoter's verification.

Environment tags solve both. Instead of hydrating from HEAD, every environment hydrates from its own **environment
tag** in the DRY repository. The
[ValidatedCommitStatus](../gating-promotions/built-in-gates/validated-commit-status.md) ordering gate moves those tags
for you: an environment's tag only moves on its own to a commit the environments before it have already run
successfully. A later environment's candidate is therefore always something it is allowed to take, no matter what has
landed on the DRY branch since. If you want to decide yourself what an environment gets, you **validate** a commit for
it, or take over its tag.

This page introduces the model. The
[gate's reference](../gating-promotions/built-in-gates/validated-commit-status.md) has every rule.

## How it works

```text
DRY branch (main):   A ─ B ─ C ─ D ─ E ─ F   (HEAD)
                                 ▲       ▲
           environment/prod-tag  │       │  environment/dev-tag
           environment/test-tag ─┘

dev   hydrates environment/dev-tag   → F is live, checks still running
test  hydrates environment/test-tag  → D is live and successful
prod  hydrates environment/prod-tag  → D is proposed; test qualified it, so prod may merge
```

1. **Advance the roots.** The controller watches the DRY branch. When HEAD moves, it moves the tag of every root
   environment (one with no `dependsOn` upstreams) to the new HEAD. The hydrator hydrates the root from its tag and
   Promoter opens the promotion PR as usual.
2. **Qualify.** The controller watches each environment's active commit statuses. Whenever a DRY commit is live in an
   environment and every active commit status is `success`, that commit is **qualified** for the environment.
   Qualifying a commit qualifies every commit before it on the DRY branch: the environment does not need to run every
   commit, only a newer one that contains it.
3. **Advance the dependents.** When an environment qualifies a newer commit, the controller moves the tag of each
   environment that depends on it to the newest commit all of that environment's upstreams have qualified. The
   hydrator picks up the moved tag and the next promotion PR opens.
4. **Gate.** The controller posts the ordering commit status on each environment's proposed commit. It reports
   `success` once the hydrator has caught up to the tag **and** the tagged commit is qualified in every upstream, or
   was validated by a person.

The gate sits in the usual place: the PromotionStrategy's `orderCommitStatusRef` points at the
ValidatedCommitStatus, and the PromotionStrategy controller injects its key onto every ChangeTransferPolicy's
`proposedCommitStatuses`. The PR, auto-merge, and the active commit statuses on each environment are unchanged.

### What happens to an environment that is busy

By default the controller does not wait for an environment to finish what it is doing before moving its tag.

- **A promotion PR is open.** The hydrator pushes the new commit to the proposed branch and the open PR picks it up,
  exactly as it does today when a new DRY commit lands.
- **Active checks are still running.** The tag moves anyway, the new commit is hydrated, and a new PR opens.

What changed is what the new candidate is. Today it is HEAD, which earlier environments may not have finished
verifying. With environment tags it is the newest commit the upstreams have qualified, so the replacement is as
mergeable as the commit it replaced.

An environment that gets a new commit before it has passed its checks on the last one never qualifies any, and the
environments after it wait. Set `advance: WhenIdle` on such an environment: the controller then moves its tag only
once it has finished what it is promoting.

## Configuring the hydrator

Point each Application's `drySource.targetRevision` at its environment's tag rather than the DRY branch. The tag is
named after the environment's branch: `<branch>-tag`.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo-prod
spec:
  sourceHydrator:
    drySource:
      repoURL: https://github.com/example/demo.git
      targetRevision: environment/prod-tag    # a tag, instead of main
      path: environments/prod
    syncSource:
      targetBranch: environment/prod
      path: .
    hydrateTo:
      targetBranch: environment/prod-next
```

The gate needs little configuration. The PromotionStrategy gains no new fields; it only references the gate through
`orderCommitStatusRef`.

```yaml
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
```

When several PromotionStrategies with the same environment branches share one DRY repository, give each its own
tags with `spec.environments[].tag`.

### Your hydrator must report no-op hydrations

A DRY commit often changes nothing for a given environment. The hydrator then has no new commit to push, and it must
record the DRY SHA it processed in a [git note](custom-hydrator.md#advanced-with-git-notes) on the proposed branch tip
instead. The Argo CD Source Hydrator does this.

The controller relies on that note. An environment whose manifests did not change is qualified for the new DRY commit
as soon as the note arrives, because it is already running those manifests successfully, and its dependents' tags then
advance. A hydrator that skips unchanged output **without** writing a note gives the controller nothing to observe:
the environment never reports the tagged commit, never qualifies it, and every environment downstream waits until a
later commit changes that environment's manifests.

### Promoter needs write access to the DRY repository

The controller pushes tags, and the notes that record validations, with the credentials of the GitRepository that
holds the DRY branch. Those credentials need permission to create and move tags. If the repository protects tags
(GitHub rulesets, GitLab protected tags), exempt your environment tag pattern or grant Promoter's identity bypass. A
rejected push sets the gate's `Ready` condition to `False` with reason `TagPushFailed` and names the tag.

The controller also records each qualification as a git note under `refs/notes/promoter.qualified` in the hydrated
repository. Promoter already pushes to that repository, so this needs no new permission.

### Send tag pushes to the webhook receiver

If you use the [webhook receiver](../getting-started.md#scm-provider-configuration), make sure the webhook on the DRY
repository sends tag pushes as well as branch pushes. The controller then reacts to a new DRY commit, or to a tag you
moved, right away. Without the webhook it notices on its requeue interval.

### Adopting environment tags on an existing PromotionStrategy

1. Create the ValidatedCommitStatus. The controller creates every missing environment tag.
2. Change each Application's `targetRevision` from the DRY branch to its tag.
3. Point the PromotionStrategy's `orderCommitStatusRef` at the new gate.

A new tag starts at the DRY commit that is live in its environment, so hydrating from the tag changes nothing. The
tag of an environment the controller validates for is created where the controller would move it anyway, when that
is newer than what is live. See
[Setup and migration](../gating-promotions/built-in-gates/validated-commit-status.md#setup-and-migration) for what to
check before step 2.

### Removing environment tags

Deleting the gate does not delete the tags, and nothing moves them afterward. Before you delete it, point each
Application's `targetRevision` back at the DRY branch and the PromotionStrategy's `orderCommitStatusRef` back at a
DependentsSuccessfulCommitStatus. Then delete the tags yourself if you no longer want them.

## Deciding what an environment gets

To decide for yourself when an environment gets a new version, set `validation: Manual` on it in the gate. The
controller stops moving that tag, and reports what is ready for the environment in
`status.environments[].upstreamCleared`.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ValidatedCommitStatus
metadata:
  name: demo
spec:
  # ...
  environments:
    - branch: environment/prod
      validation: Manual
```

There are two ways to move the tag, and they differ in what the gate then checks.

### Validate a commit

A **validation** says that a commit may deploy to the environment. Make one by setting `spec.sha` on a
[CommitValidation](../gating-promotions/built-in-gates/validated-commit-status.md#with-a-commitvalidation), which
RBAC can limit to the people who approve that environment, or by
[pushing a git note](../gating-promotions/built-in-gates/validated-commit-status.md#by-pushing-a-git-note).

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: CommitValidation
metadata:
  name: demo-prod
spec:
  promotionStrategyRef:
    name: demo
  branch: environment/prod
  sha: 4f2a9c0d1e5b7a6c8f9e0d1c2b3a4f5e6d7c8b9a
  reason: CHG-1234 approved
```

The controller moves the tag to the commit at once, wherever the commit is, and the gate passes the promotion once
the hydrator has hydrated it. **A validation is enough on its own**: the gate does not ask whether the upstream
environments have qualified the commit. That is how you send a hotfix to one environment, including a commit on a
branch the DRY branch does not contain.

A validation works on any environment, not only a `Manual` one.

### Push the tag

```shell
git push --force origin <dry-sha>:refs/tags/environment/prod-tag
```

The hydrator hydrates `prod` from the new tag and Promoter opens the promotion PR. A pushed tag is **verified**:

- **Hydration.** The proposed commit was hydrated from the commit the tag points at. Until the hydrator catches up,
  the gate is `pending`, so an older proposed commit cannot merge after you have moved the tag somewhere else.
- **Qualification.** The tagged commit, or a newer commit that contains it, is qualified in every upstream
  environment. If you tag a commit your upstreams have not finished verifying, the gate stays `pending` until they
  do. The gate's `description` names the upstream it is waiting for.

Any commit at or behind the newest commit your upstreams have qualified passes, including commits the upstreams
skipped over. You can also move a tag backward. To undo a bad promotion quickly, prefer
[rolling back](rolling-back.md), which does not wait for the hydrator.

Git treats tags as immutable by convention, which is why the push needs `--force`. A clone that already fetched the
tag will not update it on a plain `git fetch`; use `git fetch --force --tags`. SCM web UIs can create and delete tags
but not move them.

### On an environment the controller validates for

You can validate a commit, or push the tag, for an environment with the default `validation: Automatic` too. The tag
then stays where you put it until the upstream environments qualify something newer than they had at that moment.
After that the controller moves the tag again.

### Overriding the gate

The gate is an ordinary commit status. Someone with permission to merge the promotion PR in the SCM can merge it while
the gate is `pending`, as with any other gate. A commit promoted this way is treated like any other once it is live:
if it is on the DRY branch and the environment's active checks pass, the commit is qualified for that environment and
its dependents may take it.

## Semantics in detail

**What "qualified" means.** A DRY commit `D` is qualified for environment `U` when, at some point while `D` was live in
`U`, every active commit status configured for `U` was `success`. This is the same per-environment
[success](../gating-promotions/index.md#environment-success) check the HEAD-tracking gate uses, recorded instead of
evaluated live.

**Qualification covers earlier commits.** If `U` qualified `F`, every ancestor of `F` on the DRY branch is qualified
for `U` too. `F` contains their changes, and `U` ran `F` successfully. The one gap is a commit whose changes touch only
a downstream environment's directory: no upstream can exercise that, with or without tags.

**Qualification is latched.** Once qualified, a commit stays qualified. If `U` turns unhealthy later while still
running `D`, dependents may still take `D`.

**A restore takes qualification back.** [Rolling back](rolling-back.md) `U` with a RestoreActiveCommit says the commits
after the restored one should not have been trusted. `U`'s newest qualified commit becomes the restored commit, and
dependents can no longer take anything newer until `U` runs it successfully again. Tags that already moved stay where
they are; the gate holds those promotions. Moving `U` back with a validation, or by pushing its tag, takes nothing
back.

**Only commits on the DRY branch are qualified.** An environment running a validated hotfix from another branch
qualifies nothing, so the hotfix reaches the next environment only when someone validates it there too, or when it is
merged. After a force-push, anything an environment qualified on the old history is forgotten.

**No-op hydrations count.** When the hydrator reports a newer DRY commit for `U` through its git note without changing
the manifests, `U` is running that commit's manifests. If `U` has no promotion of its own pending and its active commit
statuses are `success`, the newer commit is qualified immediately. Nothing restarts: a soak timer that already elapsed
for the unchanged manifests does not run again.

**Tags only move forward on their own.** The controller advances a tag only to a commit that contains the commit it
currently points at. Only a person moves a tag backward, or off the DRY branch.

**Different environments sit on different commits.** That is the point, and it is new. Dashboards and habits built on
"every environment is converging on HEAD" need adjusting: compare an environment against its tag, not against its
siblings.
