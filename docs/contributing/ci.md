# Continuous Integration

This page describes the CI system for GitOps Promoter and explains how to resolve common CI failures.

## Overview

Every pull request runs the [`test` workflow](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/ci.yaml), which includes:

- **Go linting** — via `golangci-lint`
- **Go build and tests** — unit and integration tests via Ginkgo with `envtest`
- **Fuzz replay** — replays seeds and corpus without exploratory fuzzing
- **UI checks** — type-checking, linting, formatting, and `npm audit` for the dashboard, extension, and components-lib packages
- **Docs lint** — builds the MkDocs documentation and fails on any warning
- **Codegen verification** — ensures `go.sum`, mockery output (`internal/scms/mock/`), `make build-installer` output (CRDs, `applyconfiguration/`, deepcopy, extension icon styles, `hack/celcost/report.md`, the `dist/` install bundles), and `make generate-ui-types` output (`ui/shared/src/types/generated/view.gen.ts`) are up to date
- **Nilaway static analysis** — nil-safety analysis on non-test Go code
- **Spell checking**
- **GitHub Actions security analysis** — [zizmor](https://github.com/zizmorcore/zizmor) checks all workflow files for security issues

## Resolving security check failures

### npm audit failures

The `UI Checks` job runs `npm audit --omit=dev` for each of the three UI packages (`ui/dashboard`, `ui/extension`, `ui/components-lib`). If a vulnerability is reported in a transitive dependency, the job fails and blocks the PR.

To fix these failures, dispatch the [**npm audit fix**](https://github.com/argoproj-labs/gitops-promoter/actions/workflows/npm-audit-fix.yaml) workflow:

1. Go to **Actions → npm audit fix** in the repository.
2. Click **Run workflow**. Set **Target branch** when the fix belongs on a release branch; leave it empty to update `main`. Enable the **Force** option if the fix requires a major-version bump (breaking changes).
3. The workflow runs `npm audit fix` across all three UI packages and opens a pull request with the updated `package-lock.json` files.
4. **Close and reopen the generated PR** to trigger CI checks.
5. Review and merge the resulting PR. Once it merges, the `UI Checks` job will pass again.

> [!NOTE]
> Without the **Force** option, `npm audit fix` only upgrades packages within their declared semver range. Enable **Force** to allow major-version bumps, but review the diff carefully as it may introduce breaking changes.

### Zizmor findings

The `zizmor` workflow checks all GitHub Actions workflow files for security issues such as:

- Unpinned action references (use a full commit SHA with a version comment)
- Template-injection risks (avoid `${{ … }}` expressions directly in `run:` steps — pass them through environment variables instead)
- Overly broad permissions

If the zizmor job fails on your PR, review the SARIF output attached to the run to see exactly which workflow file and line triggered the finding, then address the issue before merging.

## Dependency auto-merge

Some dependency updates merge automatically once the required status checks pass. A dedicated GitHub App approves them. Nothing bypasses the rulesets.

- **Dependabot**: patch and minor version updates are approved and set to auto-merge by [`dependabot-auto-merge.yaml`](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/dependabot-auto-merge.yaml). Dependabot waits seven days after an upstream release before opening a PR. Security updates are not delayed.
- **Renovate**: the `automerge` rules in [`renovate.json5`](https://github.com/argoproj-labs/gitops-promoter/blob/main/renovate.json5) decide which updates qualify. Today that's the docs dependencies, Argo CD, kindest/node, Node.js, and Python, plus patch updates of kustomize, controller-tools, and kube-rbac-proxy, after a seven-day `minimumReleaseAge`. Renovate enables auto-merge, and [`renovate-auto-merge.yaml`](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/renovate-auto-merge.yaml) approves.
- Major updates, and Renovate's Go toolchain, Kubernetes platform, and release-tooling groups, still need a human review.
- Approval only happens when every commit on the PR was authored by the bot and signed by GitHub. If anyone else pushes to a bot PR, [`dependency-auto-merge-revoke.yaml`](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/dependency-auto-merge-revoke.yaml) disables auto-merge and dismisses the bot approval. It runs from `main`, so a PR can't edit it away.
