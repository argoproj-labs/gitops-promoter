# Backporting fixes

Fixes land on `main` first and are then cherry-picked to one or more
`release-X.Y` branches. A release branch being available for backports does
not define how long that minor series will be supported.

## Automated backports

Add a `cherry-pick/X.Y` label to a pull request that targets `main`. The label
can be added before merge or to an already-merged PR:

- When a labeled PR is merged, the workflow processes all of its
  `cherry-pick/*` labels.
- When a label is added after merge, the workflow processes that label
  immediately.

For `cherry-pick/1.2`, the automation cherry-picks the merge commit onto a new
branch based on `release-1.2`, opens a PR back to `release-1.2`, and comments
with the backport PR number on the original PR. Review and merge the generated
PR normally. Multiple labels create one backport PR per release line.

If the cherry-pick conflicts, the workflow comments with a link to its logs.
Resolve the conflict manually from the target release branch and open a PR:

```bash
git fetch origin
git switch -c cherry-pick-123-to-release-1.2 origin/release-1.2
git cherry-pick -m 1 <merge-commit-sha>
# Resolve conflicts, then:
git add <resolved-files>
git cherry-pick --continue
git push origin cherry-pick-123-to-release-1.2
```

Do not merge a release branch back into `main`; the original fix should
already be present there.

## Repository configuration

The copied workflows authenticate as a GitHub App so they can push a branch,
open a PR, and comment on the original PR. Configure:

- repository variable `CHERRYPICK_APP_ID`;
- repository secret `CHERRYPICK_APP_PRIVATE_KEY`;
- an installed GitHub App with Contents, Issues, and Pull requests read/write
  permissions and Metadata read permission.

Apply the same required checks and review rules used for `main` to the
`release-*` branch pattern. The repository workflows run CI for both direct
pushes and PRs on release branches.

Release branches and their labels are normally created with a new `X.Y.0`
release. When a patch is requested for an older series, the version-bump
workflow creates the missing branch from the latest `vX.Y.Z` tag and creates
the label.

## Upstream workflow

The cherry-pick automation is copied from Argo CD so behavior and maintenance
can stay aligned:

- [Argo CD `cherry-pick.yml`](https://github.com/argoproj/argo-cd/blob/be6b24dd760b6eadfc09e80ae3cdaa82e1b50495/.github/workflows/cherry-pick.yml)
- [Argo CD `cherry-pick-single.yml`](https://github.com/argoproj/argo-cd/blob/be6b24dd760b6eadfc09e80ae3cdaa82e1b50495/.github/workflows/cherry-pick-single.yml)
- [Argo CD backporting guide](https://github.com/argoproj/argo-cd/blob/be6b24dd760b6eadfc09e80ae3cdaa82e1b50495/docs/developer-guide/backporting-fixes.md)

Our only workflow changes are the `main` default branch, a
repository-relative failure-log URL, omission of harden-runner, and
source-attribution comments. Compare against the pinned upstream files before
changing the copied workflows.
