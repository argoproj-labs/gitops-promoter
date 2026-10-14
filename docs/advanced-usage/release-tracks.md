# Holding Back Upper Environments with Release Tracks

GitOps Promoter always promotes the latest DRY commit (see
[FAQs > How does GitOps Promoter handle concurrent releases?](../faqs.md#how-does-gitops-promoter-handle-concurrent-releases)).
That works well for continuous delivery, but it is a poor fit when upper environments have a much slower cadence than
lower ones. A common example is a UAT environment where customers test a release for several days before it is approved
for production. In the meantime, new changes keep landing on `main`. Each one replaces the release under test, and the
approved commit can't reach production.

This page shows how to split one PromotionStrategy into two **release tracks** using only existing features: Argo CD's
Source Hydrator, `dependsOn`, and a git tag that you move.

## How it works

* The **dev track** (for example `ci` and `nightly`) hydrates from `main`, just like today.
* The **release track** (for example `int`, `preprod`, `uat`, and `prod`) hydrates from a tag, `release-current`.
* To **cut a release**, move the tag to the DRY commit you want to ship.

Every environment within a track hydrates the same DRY ref, so each environment in the track sees the same DRY SHA, and
the [DependentsSuccessfulCommitStatus](../gating-promotions/built-in-gates/dependents-successful-commit-status.md) gate
works as usual within each track. The first release-track environment declares no `dependsOn`, so the dependency graph
has two roots: one per track. New commits on `main` only move the dev track's proposed branches. The release track stays
on the tagged commit until you move the tag.

```text
dev track:      main ──────────────► ci ──► nightly

release track:  release-current ──► int ──┬──► preprod
                (tag)                     └──► uat ──► prod
```

## Configuration

### Argo CD Applications

The only difference between the tracks is `drySource.targetRevision`. All Applications that hydrate to the same
environment branch must use the same `targetRevision`.

```yaml
# Dev track: environment/ci and environment/nightly
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app-ci
spec:
  sourceHydrator:
    drySource:
      repoURL: https://git.example.com/platform/deploy
      path: apps/my-app
      targetRevision: main
    hydrateTo:
      targetBranch: environment/ci-next
    syncSource:
      targetBranch: environment/ci
      path: apps/my-app
---
# Release track: environment/int, environment/preprod, environment/uat, and environment/prod
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app-prod
spec:
  sourceHydrator:
    drySource:
      repoURL: https://git.example.com/platform/deploy
      path: apps/my-app
      targetRevision: release-current
    hydrateTo:
      targetBranch: environment/prod-next
    syncSource:
      targetBranch: environment/prod
      path: apps/my-app
```

If you use a [custom hydrator](custom-hydrator.md), configure it the same way: hydrate each environment's proposed
branch from the DRY ref of that environment's track.

### PromotionStrategy

One PromotionStrategy covers both tracks. The release track's first environment (`environment/int`) declares no
`dependsOn`, which makes it a second root of the graph.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  gitRepositoryRef:
    name: deploy-repo
  orderCommitStatusRef:
    kind: DependentsSuccessfulCommitStatus
    name: my-app
  activeCommitStatuses:
    - key: argocd-health
  environments:
    # Dev track
    - branch: environment/ci
    - branch: environment/nightly
      dependsOn: [environment/ci]
    # Release track
    - branch: environment/int
    - branch: environment/preprod
      dependsOn: [environment/int]
    - branch: environment/uat
      dependsOn: [environment/int]
    - branch: environment/prod
      dependsOn: [environment/uat]
      autoMerge: false
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsSuccessfulCommitStatus
metadata:
  name: my-app
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: my-app
```

Add an [ArgoCDCommitStatus](../gating-promotions/built-in-gates/argocd-commit-status.md) for `argocd-health` and any
other gates as usual, for example a
[ScheduledCommitStatus](../gating-promotions/built-in-gates/scheduled-commit-status.md) on `environment/nightly`.

## Cutting a release

Releasing usually means shipping whatever the last dev-track environment is running. Read the DRY SHA from its
`hydrator.metadata`, confirm that the environment is healthy (in the dashboard or the Argo CD UI), and move the tag:

```bash
git fetch origin
DRY_SHA=$(git show origin/environment/nightly:apps/my-app/hydrator.metadata | jq -r .drySha)
git tag -f release-current "$DRY_SHA"
git push -f origin release-current
```

Adjust the `hydrator.metadata` path to match where your hydrator writes it (see
[Using a Custom Hydrator](custom-hydrator.md#3-include-hydratormetadata-file)).

The release track then promotes that commit like any other change: `int` first, then `preprod` and `uat`, and then a
pull request for `prod`. Because the tag points at a commit on `main`, the promotion pull requests describe the original
change. There is no intermediate merge commit.

> [!IMPORTANT]
> Promoter does not verify that the commit you tag was healthy in the dev track. Choosing a verified commit is the
> responsibility of whoever moves the tag. If a CI job moves the tag, have it check the dev track's commit statuses
> first, and restrict who can push to the tag (for example with a protected tag rule).

> [!NOTE]
> Argo CD resolves `targetRevision` to a commit SHA and caches the result. After the tag moves, the hydrator picks up
> the new commit on its next refresh of the DRY source. To make that happen right away, configure a git webhook for
> Argo CD or hard-refresh one of the release-track Applications.

### Example timeline

| Step | Action                                              | ci | nightly | int | preprod / uat | prod             |
|------|-----------------------------------------------------|----|---------|-----|---------------|------------------|
| 1    | Commit A is merged to `main`                        | A  | A       | –   | –             | –                |
| 2    | Tag moved to A                                      | A  | A       | A   | A             | PR for A open    |
| 3    | B and C are merged to `main` while UAT tests A      | C  | C       | A   | A             | PR for A open    |
| 4    | UAT approves; the `prod` PR is merged               | C  | C       | A   | A             | **A**            |
| 5    | Tag moved to C                                      | C  | C       | C   | C             | PR for C open    |

Step 3 is the important one. Under the default "release latest" model, B and C would replace A in the upper
environments. With release tracks, they only advance the dev track.

## Hotfixes

To ship a fix without the unreleased changes on `main`:

1. Branch from the current release, commit the fix, and push the branch:

    ```bash
    git switch -c hotfix/my-fix release-current
    # ...commit the fix...
    git push origin hotfix/my-fix
    ```

2. Move the tag to the hotfix commit:

    ```bash
    git tag -f release-current hotfix/my-fix
    git push -f origin release-current
    ```

    The hotfix goes through `int`, `preprod`, `uat`, and `prod` with all gates applied. You don't need to revert
    anything.

3. Cherry-pick the fix onto `main` so that the next release includes it.

> [!WARNING]
> If the fix never makes it to `main`, the next tag move removes it from the release track.

## Limitations

* **No cross-track verification.** The gate never checks the release track against the dev track, so Promoter can't
  stop you from tagging a commit that never passed `ci` or `nightly`.
* **Extra reconciles.** The PromotionStrategy controller expects every environment to end up on the same DRY SHA and
  periodically re-queues the ChangeTransferPolicies of environments that lag behind. While the tracks are on
  different commits, which is most of the time, this causes extra reconciles and git fetches for the release-track
  environments. Promotion behavior is not affected.
* **The dashboard has no notion of tracks.** Environments on the two tracks show different DRY commits, which is
  expected in this setup.
