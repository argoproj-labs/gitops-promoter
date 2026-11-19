# Terraform integration

A hydrator and an applier for promoting Terraform (or OpenTofu) configuration through
environments with GitOps Promoter. Both are shell scripts wrapped in composite GitHub Actions;
the scripts run anywhere `bash`, `git`, `jq`, and a Terraform-compatible binary are available.

The user guide lives in the docs site under
[Integrations > Terraform](https://gitops-promoter.readthedocs.io/en/latest/integrations/terraform/).
This file is the reference for the scripts and actions.

## How it works

```
DRY branch (main)                        environment/dev-next   environment/dev
───────────────────                      ────────────────────   ───────────────
push ──► hydrate.yaml ──► hydrate.sh ──► hydrated commit ─PR─► merge (Promoter)
                                         + hydrator.metadata          │
                                         + plan.txt                   ▼
                                                               apply.yaml ──► apply.sh
                                                                     │
                                            WebRequestCommitStatus ◄─┘ check run
                                                     │
                                       terraform-apply CommitStatus (active gate)
                                                     │
                                       next environment may promote
```

1. A push to the DRY branch runs `hydrate.sh`. For each environment it exports
   `environments/<env>` and `modules/` from the DRY commit, runs `terraform init` and `plan`,
   writes the plan as `plan.txt` and the DRY commit details as `hydrator.metadata`, and pushes the
   result to `environment/<env>-next`. Identical output only updates a git note.
2. GitOps Promoter opens a pull request from `environment/<env>-next` to `environment/<env>` and
   merges it when the gates pass.
3. The merge is a push to `environment/<env>`, which runs `apply.sh`: `init -lockfile=readonly`,
   `plan -detailed-exitcode`, and `apply` when there are changes.
4. A `WebRequestCommitStatus` polls the GitHub check-runs API for the `terraform-apply` job on the
   active commit and reports the `terraform-apply` commit status, which the next environment's
   ordering gate waits for.

## Repository layout

```
modules/<name>/            shared modules, copied into every hydrated commit
environments/<env>/        one root module per environment
  main.tf                  module calls, variables, outputs
  backend.tf               remote backend configuration for this environment
  terraform.tfvars         environment-specific values
```

`examples/repo/` is a complete, offline-capable example of this layout (local backend and the
built-in `terraform_data` resource). The hydrated tree keeps the same paths, so relative module
sources such as `../../modules/greeting` keep working.

## `hydrate.sh`

Runs from a checkout of the DRY branch and pushes the proposed branches. Configuration is by
environment variable; the composite action in `actions/hydrate` maps its inputs to these.

| Variable | Default | Description |
|---|---|---|
| `ENVIRONMENTS` | required | Space- or comma-separated environment names |
| `DRY_SHA` | `HEAD` | DRY commit to hydrate |
| `REPO_URL` | GitHub repository URL, else the remote URL | Recorded as `repoURL`; credentials and `.git` are stripped, ssh is rewritten to https |
| `TF_BINARY` | `terraform` | Use `tofu` for OpenTofu |
| `REMOTE` | `origin` | Remote to fetch from and push to |
| `BRANCH_PREFIX` | `environment/` | Active branch is `<prefix><env>`, proposed branch is `<prefix><env>-next` |
| `ENV_DIR` | `environments` | Directory holding one root module per environment |
| `INCLUDE_PATHS` | `modules` | Extra paths copied verbatim into every hydrated commit |
| `PLAN` | `true` | `true`: init with the backend and commit `plan.txt`; `false`: `init -backend=false` and `validate` only |
| `PLAN_ARGS` | `-lock=false` | Extra arguments for `plan` |
| `BACKEND_CONFIG` | | File passed as `-backend-config` to `init`, relative to the root module |
| `LOCK_PLATFORMS` | | Platforms for `providers lock`, so the lock file covers the apply runners |
| `BOOTSTRAP_ACTIVE` | `true` | Create the active branch from the first hydrated commit when it is missing |
| `NOTES_REF` | `refs/notes/hydrator.metadata` | Notes ref used for no-op hydrations |
| `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL` | `github-actions[bot]` | Commit identity |

Behaviour worth knowing:

- **Duplicate detection** compares everything except `plan.txt` and `hydrator.metadata`. A DRY
  commit that changes neither the root module nor the shared modules does not create a hydrated
  commit; the new `drySha` is recorded in the git note on the existing tip.
- **Bootstrap.** GitOps Promoter requires both `environment/<env>` and `environment/<env>-next` to
  exist, and GitHub cannot open a pull request between unrelated histories. On the first run the
  script pushes the hydrated commit to the active branch as well, so the first hydration counts as
  already promoted. Pushes made with `GITHUB_TOKEN` do not trigger workflows, so run the apply
  workflow once by hand after bootstrap (or check out with a GitHub App token).
- **Planning needs state.** With `PLAN=true` the hydrate job must be able to read remote state.
  Use `PLAN=false` when it cannot; the hydrated commit is still valid, just without `plan.txt`.
- **Failures are per environment.** One failing environment does not stop the others; the exit
  code is non-zero at the end.
- Requires git 2.32 or newer (for `commit --trailer`).

## `apply.sh`

Runs from a checkout of an environment's active branch. Never commits.

| Variable | Default | Description |
|---|---|---|
| `ROOT_DIR` | the single directory under `ENV_DIR` | Root module to apply |
| `ENV_DIR` | `environments` | Where to look for the root module |
| `TF_BINARY` | `terraform` | Use `tofu` for OpenTofu |
| `APPLY` | `true` | `false` stops after `plan` |
| `PLAN_ARGS` | | Extra arguments for `plan` |
| `BACKEND_CONFIG` | | File passed as `-backend-config` to `init` |
| `WARN_ON_STALE_PLAN` | `true` | Warn when the plan summary differs from the reviewed `plan.txt` |
| `REPO_ROOT` | current directory | Checkout root holding `hydrator.metadata` |

Exit codes follow Terraform: a plan error fails the job; no changes and a successful apply both
succeed, so re-running the workflow is safe. A summary table is appended to `$GITHUB_STEP_SUMMARY`
when running in GitHub Actions.

## Composite actions

- `actions/hydrate` wraps `hydrate.sh`; inputs mirror the variables above in kebab-case
  (`environments`, `tf-binary`, `plan`, ...).
- `actions/apply` wraps `apply.sh` (`root-dir`, `tf-binary`, `apply`, ...).

Neither action checks out code, installs Terraform, or configures cloud credentials. The caller
does that, which keeps the actions free of third-party dependencies. See `examples/workflows/`.

## Promoter manifests

`examples/promoter/` contains a `PromotionStrategy` with `activeCommitStatuses: [terraform-apply]`,
the required `DependentsSuccessfulCommitStatus`, and the `WebRequestCommitStatus` that reads the
apply check run. Replace `ORG/REPO` in the latter and keep its `check_name` equal to the apply
job's `name`.

## Tests

```
make lint-integrations   # shellcheck
make test-integrations   # tests/run.sh
```

`tests/run.sh` runs the script tests against a recording stub `terraform` binary (no network,
no cloud credentials), then `tests/e2e_real_binary.sh` with a real `terraform` or `tofu` when one
is on `PATH`, using the example repository's local backend.

## Out of scope for now

- Shared-active-branch mode (`activePath`): proposed branches named `<active>-next/<path>` with
  `hydrator.metadata` under `<path>/`.
- SCMs other than GitHub for the applier signal. The scripts are SCM-agnostic; only the
  `WebRequestCommitStatus` example targets the GitHub check-runs API. On another SCM, point it at
  the equivalent pipeline API or create a `CommitStatus` resource from the apply job.

## Changelog

- Initial release: hydrator, applier, composite actions, example repository and manifests.
