# Continuous Integration

This page describes the CI system for GitOps Promoter and explains how to resolve common CI failures.

## Overview

Every pull request runs the [`test` workflow](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/ci.yaml). Independent checks run as separate jobs so Ginkgo is not blocked on UI, docs, or the manager binary:

- **Tests** — Ginkgo integration tests (`make test-parallel`) and fuzz replay. Compiles against the committed `ui/web/static` placeholder; it does not rebuild the dashboard.
- **Build** — embeds the real dashboard (`make build-dashboard`) and links `cmd/main.go` (`go build -o bin/manager`).
- **UI Checks** — dashboard/extension production builds, UI lint (`make lint-ui`, including type-check, formatting, and `npm audit`), dashboard and extension unit tests, and docs lint (MkDocs, fails on any warning).
- **Continuous Integration** — aggregator that succeeds only when Tests, Build, and UI Checks all succeeded. Keep this as the required GitHub check for the former monolithic job.
- **Lint** — Go linting via `golangci-lint`
- **Check Codegen** — ensures `go.sum`, mockery output (`internal/scms/mock/`), `make build-installer` output (CRDs, `applyconfiguration/`, deepcopy, extension icon styles, `hack/celcost/report.md`, the `dist/` install bundles), and `make generate-ui-types` output (`ui/shared/src/types/generated/view.gen.ts`) are up to date
- **Nilaway Static Analysis** — nil-safety analysis on non-test Go code
- **Dead Code Analysis**
- **Spell checking** (separate workflow)
- **GitHub Actions security analysis** — [zizmor](https://github.com/zizmorcore/zizmor) checks all workflow files for security issues (separate workflow)

## Resolving security check failures

### npm audit failures

The `UI Checks` job runs `npm audit --omit=dev` for each of the three UI packages (`ui/dashboard`, `ui/extension`, `ui/components-lib`). If a vulnerability is reported in a transitive dependency, the job fails and blocks the PR.

To fix these failures, dispatch the [**npm audit fix**](https://github.com/argoproj-labs/gitops-promoter/actions/workflows/npm-audit-fix.yaml) workflow:

1. Go to **Actions → npm audit fix** in the repository.
2. Click **Run workflow**. Enable the **Force** option if the fix requires a major-version bump (breaking changes).
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
