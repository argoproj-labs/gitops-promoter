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
   commit to clear every lower environment. If commits land faster than the lower environments can verify them, `prod`
   waits indefinitely.
2. **No manual pinning.** You cannot ship to `dev` and `test` continuously while holding `prod` at a version QA has
   blessed, except by [splitting changes across DRY commits](../structuring-your-changes.md#multiple-dry-commits-intentional-drift),
   which gives up Promoter's verification.

Environment tags solve both. Instead of hydrating from HEAD, every environment hydrates from its own **environment
tag** in the DRY repository. The
[DependentsQualifiedCommitStatus](../gating-promotions/built-in-gates/dependents-qualified-commit-status.md) ordering
gate moves those tags for you: an environment's tag only ever points at a commit its upstream environments have already
run successfully. A higher environment's candidate is therefore always something it is allowed to take, no matter what
has landed on the DRY branch since. If you want to decide the timing yourself, you take over an environment's tag and
push it by hand.

## How it works

```text
DRY branch (main):   A ─ B ─ C ─ D ─ E ─ F   (HEAD)
                                 ▲       ▲
                              env/prod  env/dev
                              env/test

dev   hydrates env/dev   → F is live, checks still running
test  hydrates env/test  → D is live and successful
prod  hydrates env/prod  → D is proposed; test qualified it, so prod may merge
```

1. **Advance the roots.** The controller watches the DRY branch. When HEAD moves, it moves the tag of every root
   environment (one with no `dependsOn` upstreams) to the new HEAD. The hydrator hydrates the root from its tag and
   Promoter opens the promotion PR as usual.
2. **Qualify.** The controller watches each environment's active commit statuses. Whenever a DRY commit is live in an
   environment and every active commit status is `success`, that commit is **qualified** for the environment.
   Qualifying a commit qualifies every commit before it on the DRY branch: the environment does not need to run every
   commit, only a newer one that contains it.
3. **Advance the dependents.** When an environment qualifies a newer commit, the controller moves the tag of each
   environment that depends on it to the newest commit all of that environment's upstreams have qualified. The hydrator
   picks up the moved tag and the next promotion PR opens.
4. **Gate.** The controller posts the ordering commit status on each environment's proposed commit. It reports
   `success` once the hydrator has caught up to the tag **and** the tagged commit is qualified in every upstream. For a
   tag the controller moved, the second check is true by construction. It matters when a person moves the tag.

The gate sits in the usual place: the PromotionStrategy's `orderCommitStatusRef` points at the
DependentsQualifiedCommitStatus, and the PromotionStrategy controller injects its key onto every ChangeTransferPolicy's
`proposedCommitStatuses`. The PR, auto-merge, and the active commit statuses on each environment are unchanged.

### What happens to an environment that is busy

The controller does not wait for an environment to finish what it is doing before moving its tag.

- **A promotion PR is open.** The hydrator pushes the new commit to the proposed branch and the open PR picks it up,
  exactly as it does today when a new DRY commit lands.
- **Active checks are still running.** The tag moves anyway, the new commit is hydrated, and a new PR opens. The
  environment's own active checks never gate its own promotions, which is also today's behavior.

What changed is what the new candidate is. Today it is HEAD, which lower environments may not have finished verifying.
With environment tags it is the newest commit the upstreams have qualified, so the replacement is as mergeable as the
commit it replaced.

## Configuring the hydrator

Point each Application's `drySource.targetRevision` at its environment's tag rather than the DRY branch.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo-prod
spec:
  sourceHydrator:
    drySource:
      repoURL: https://github.com/example/demo.git
      targetRevision: env/prod        # a tag, instead of main
      path: environments/prod
    syncSource:
      targetBranch: environment/prod
      path: .
    hydrateTo:
      targetBranch: environment/prod-next
```

The tag names are declared on the gate, one per environment. The PromotionStrategy gains no new fields; it only
references the gate through `orderCommitStatusRef`.

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
  environments:
    - branch: environment/dev
      tag: env/dev
    - branch: environment/test
      tag: env/test
    - branch: environment/prod
      tag: env/prod
```

Choose tag names that do not collide with branch names in the same repository. A tag and a branch that are both called
`environment/prod` make `targetRevision: environment/prod` ambiguous. When several PromotionStrategies share a DRY
repository, give each its own tags (for example `env/<app>/prod`); qualification is tracked per PromotionStrategy.

### Your hydrator must report no-op hydrations

A DRY commit often changes nothing for a given environment. The hydrator then has no new commit to push, and it must
record the DRY SHA it processed in a [git note](custom-hydrator.md#advanced-with-git-notes) on the proposed branch tip
instead. The Argo CD Source Hydrator does this.

The controller relies on that note. An environment whose manifests did not change is qualified for the new DRY commit as
soon as the note arrives, because it is already running those manifests successfully, and its dependents' tags then
advance. A hydrator that skips unchanged output **without** writing a note gives the controller nothing to observe:
the environment never reports the tagged commit, never qualifies it, and every environment downstream waits until a
later commit changes that environment's manifests.

### Promoter needs write access to the DRY repository

The controller pushes tags with the credentials of the GitRepository that holds the DRY branch. Those credentials need
permission to create and update tags. If the repository protects tags (GitHub rulesets, GitLab protected tags), exempt
your environment tag pattern or grant Promoter's identity bypass. A rejected push sets the gate's `Ready` condition to
`False` with reason `TagPushFailed` and names the tag.

The controller also records each qualification as a git note under `refs/notes/promoter.qualified` in the hydrated
repository. Promoter already pushes to that repository, so this needs no new permission.

### Send tag pushes to the webhook receiver

If you use the [webhook receiver](../getting-started.md#scm-provider-configuration), make sure the webhook on the DRY
repository sends tag pushes as well as branch pushes. The controller then reacts to a new DRY commit, or to a tag you
moved, right away. Without the webhook it notices on its requeue interval.

### Adopting environment tags on an existing PromotionStrategy

1. Create the DependentsQualifiedCommitStatus. The controller creates every missing environment tag.
2. Change each Application's `targetRevision` from the DRY branch to its tag.
3. Point the PromotionStrategy's `orderCommitStatusRef` at the new gate.

A new tag starts at the DRY commit that is live in its environment, so hydrating from the tag changes nothing. Two
cases differ:

- An automatically advanced tag is created where the controller would move it anyway, when that is newer than what is
  live: the DRY branch HEAD for a root, the newest commit the upstreams have qualified for a dependent.
- An environment with nothing live yet (a new PromotionStrategy) gets its tag at the DRY branch HEAD.

Every environment that is [successful](../gating-promotions/index.md#environment-success) when the gate is created
qualifies its live commit on the first reconcile. There is no record of anything an environment ran before that.

### Removing environment tags

Deleting the gate does not delete the tags, and nothing moves them afterward. Before you delete it, point each
Application's `targetRevision` back at the DRY branch and the PromotionStrategy's `orderCommitStatusRef` back at a
DependentsSuccessfulCommitStatus. Then delete the tags yourself if you no longer want them.

## Taking control of an environment's tag

To decide for yourself when an environment gets a new version, set `advance: Manual` on it in the gate. The controller stops
moving that tag and you move it.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsQualifiedCommitStatus
metadata:
  name: demo
spec:
  # ...
  environments:
    - branch: environment/dev
      tag: env/dev
    - branch: environment/test
      tag: env/test
    - branch: environment/prod
      tag: env/prod
      advance: Manual
```

```shell
git fetch origin main
git tag --force env/prod <dry-sha>
git push --force origin refs/tags/env/prod
```

The hydrator hydrates `prod` from the new tag and Promoter opens the promotion PR. Nothing about the PR is different
from an automatically advanced one: the ordering gate verifies the commit, your other proposed gates apply, and the
PR merges automatically if the environment has `autoMerge` enabled.

### What the gate checks for a tag you pushed

The same two things it checks for every environment:

- **Hydration.** The proposed commit was hydrated from the commit the tag points at. Until the hydrator catches up, the
  gate is `pending`, so an older proposed commit cannot merge after you have moved the tag somewhere else.
- **Qualification.** The tagged commit, or a newer commit that contains it, is qualified in every upstream environment.
  If you tag a commit your upstreams have not finished verifying, the gate stays `pending` until they do. The gate's
  `description` names the upstream it is waiting for.

Any commit at or behind the newest commit your upstreams have qualified passes, including commits the upstreams skipped
over. `status.environments[].qualified` on the gate shows the newest qualified commit for each environment.

You can also move a tag backward. The older commit is hydrated, the gate passes because it is behind what upstream has
qualified, and the PR proposes the older manifests. To undo a bad promotion quickly, prefer
[rolling back](rolling-back.md), which does not wait for the hydrator.

The tag must point at a commit on the gate's DRY branch. For a tag anywhere else, the gate stays `pending` and says
the tag is not on the DRY branch.

### Overriding the gate

The gate is an ordinary commit status. Someone with permission to merge the promotion PR in the SCM can merge it while
the gate is `pending`, as with any other gate. A commit promoted this way is treated like any other once it is live: if
the environment's active checks pass, the commit is qualified for that environment and its dependents may take it.

### Tags move; most tooling assumes they don't

Git treats tags as immutable by convention, which is why the commands above need `--force`. Keep two things in mind:

- **Stale local tags.** A clone that already fetched `env/prod` will not update it on a plain `git fetch`. Use
  `git fetch --force --tags` (or `git fetch origin +refs/tags/env/prod:refs/tags/env/prod`) when inspecting a tag.
  Argo CD is unaffected because it does not rely on cached tags.
- **No "move" button.** SCM web UIs can create and delete tags but not move them. Advancing a tag by hand is a CLI or
  CI job.

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
they are; the gate holds those promotions. The controller only recognizes a RestoreActiveCommit. A revert pushed to the
active branch by hand takes nothing back.

**Only commits on the DRY branch count.** An environment qualifies a commit only if it is reachable from the HEAD of
the gate's DRY branch. After a force-push, anything an environment qualified on the old history is forgotten, an
automatically advanced tag left on the old history is put back on the branch, and an `advance: Manual` tag is left for
you to move.

**No-op hydrations count.** When the hydrator reports a newer DRY commit for `U` through its git note without changing
the manifests, `U` is running that commit's manifests. If `U` has no promotion of its own pending and its active commit
statuses are `success`, the newer commit is qualified immediately. Nothing restarts: a soak timer that already elapsed
for the unchanged manifests does not run again.

**Tags only move forward on their own.** The controller advances a tag only to a descendant of the commit it currently
points at. It never moves a tag backward, and it never moves a tag on an environment set to `advance: Manual`.

**Each environment needs a quiet window.** An environment qualifies a commit only if its active checks all pass while
that commit is live. If `test` has a one-hour soak and `dev` qualifies a new commit every twenty minutes, `test` merges
each of them, restarts its soak each time, and never qualifies anything, so `prod` never advances. Environment tags do
not fix this on their own. Shorten the checks, turn off `autoMerge` on `test`, or set `test` to `advance: Manual` and
move its tag on your own schedule.

**Different environments sit on different commits.** That is the point, and it is new. Dashboards and habits built on
"every environment is converging on HEAD" need adjusting: compare an environment against its tag, not against its
siblings.
