# Terraform

The Terraform integration promotes Terraform (or OpenTofu) configuration through environments
with pull requests, the same way the Argo CD Source Hydrator promotes Kubernetes manifests. It is a
reference implementation that lives in
[`integrations/terraform/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/integrations/terraform):
two shell scripts, two composite GitHub Actions, an example repository, and the Promoter manifests
that tie them together.

## How a change flows

1. **Hydrate.** A push to the DRY branch runs the hydrate workflow. For each environment, the
   hydrator exports `environments/<env>/` and `modules/` from the DRY commit, runs
   `terraform init` and `terraform plan`, and pushes a commit to `environment/<env>-next` that
   contains the root module, the provider lock file, a human-readable `plan.txt`, and the
   `hydrator.metadata` file required by the [custom hydrator contract](../advanced-usage/custom-hydrator.md).
   If the rendered sources did not change, it only updates the git note so no empty pull request
   is opened.
2. **Promote.** GitOps Promoter opens a pull request from `environment/<env>-next` to
   `environment/<env>`. Reviewers see `plan.txt` in the diff. The pull request merges when the
   gates pass (automatically, or by a human for `autoMerge: false` environments).
3. **Apply.** The merge is a push to `environment/<env>`, which runs the apply workflow:
   `terraform init -lockfile=readonly`, `terraform plan -detailed-exitcode`, and
   `terraform apply` when there are changes.
4. **Gate.** A `WebRequestCommitStatus` with `reportOn: active` polls the GitHub check-runs API
   for the apply job on the active commit and reports the `terraform-apply` commit status. The
   [ordering gate](../gating-promotions/built-in-gates/dependents-successful-commit-status.md) of
   the next environment waits for it, so staging is not promoted until dev applied successfully.

## Prerequisites

- A GitHub repository with the layout below and GitHub Actions enabled.
- GitOps Promoter configured for that repository with a GitHub App
  ([Getting Started](../getting-started.md)). The App's existing `Checks: Read and write`
  permission is what the gate uses to read check runs.
- Cloud credentials for the workflows: read access to state for the hydrate job (to produce
  `plan.txt`), read/write for the apply job.

## Repository layout

```
modules/<name>/            shared modules, copied into every hydrated commit
environments/<env>/        one root module per environment
  main.tf                  module calls, variables, outputs
  backend.tf               remote backend for this environment
  terraform.tfvars         environment-specific values
.github/workflows/
  hydrate.yaml             on push to the DRY branch
  apply.yaml               on push to environment/* (not *-next)
```

A complete example, usable offline, is in
[`integrations/terraform/examples/repo/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/integrations/terraform/examples/repo).
The hydrated commit keeps the same paths plus `hydrator.metadata` and `plan.txt` at the root, so
relative module sources keep working:

```
environments/dev/          main.tf, backend.tf, terraform.tfvars, .terraform.lock.hcl
modules/greeting/
hydrator.metadata
plan.txt
```

## Workflows

Copy both files from
[`integrations/terraform/examples/workflows/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/integrations/terraform/examples/workflows)
into `.github/workflows/` of the Terraform repository.

**Hydrate** runs on every push to the DRY branch and pushes the proposed branches. It checks out
with push credentials, installs Terraform, and calls the hydrate action:

```yaml
{!integrations/terraform/examples/workflows/hydrate.yaml!}
```

**Apply** runs on every push to an active environment branch. The job name `terraform-apply` is
the check run the gate looks for:

```yaml
{!integrations/terraform/examples/workflows/apply.yaml!}
```

Both actions are thin wrappers around `hydrate.sh` and `apply.sh`. All inputs, their defaults,
and the scripts' behaviour are documented in the
[integration README](https://github.com/argoproj-labs/gitops-promoter/blob/main/integrations/terraform/README.md).

> [!NOTE]
> Pushes made with the workflow's `GITHUB_TOKEN` do not trigger other workflows. The hydrator's
> first run creates `environment/<env>` as well as `environment/<env>-next` (GitOps Promoter needs
> both, and GitHub cannot open a pull request between unrelated histories), and that bootstrap push
> will not start the apply workflow. Run it once with **Run workflow** (`workflow_dispatch`), or
> check out with a GitHub App token so the push triggers it. Merges performed by GitOps Promoter
> use the App and trigger the apply workflow normally.

## Promoter manifests

Apply these in the namespace where the repository's `GitRepository` and `ScmProvider` live
(see [Getting Started](../getting-started.md)). They are in
[`integrations/terraform/examples/promoter/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/integrations/terraform/examples/promoter).

The `PromotionStrategy` lists `terraform-apply` as an active commit status, so an environment only
counts as successful once its apply run succeeded:

```yaml
{!integrations/terraform/examples/promoter/promotionstrategy.yaml!}
```

The required ordering gate:

```yaml
{!integrations/terraform/examples/promoter/dependentssuccessful.yaml!}
```

The `WebRequestCommitStatus` that turns the apply check run into the `terraform-apply` status.
Replace `ORG/REPO`, and keep `check_name` equal to the apply job's name:

```yaml
{!integrations/terraform/examples/promoter/webrequestcommitstatus.yaml!}
```

### Gate semantics

| Apply check run on the active commit | `terraform-apply` phase | Description |
|---|---|---|
| Completed with conclusion `success` | `success` | `terraform-apply: success` |
| Queued or in progress | `pending` | `terraform-apply: in_progress` |
| Completed with `failure`, `cancelled`, ... | `pending` | `terraform-apply: failure` |
| No run yet (bootstrap, or before the first promotion) | `pending` | `terraform-apply: waiting for apply run` |

A `WebRequestCommitStatus` never reports `failure`, so a failed apply leaves the environment
pending, which blocks downstream promotion until a re-run succeeds. The status URL in the SCM
links to the run.

The example uses `mode.polling` with a one-minute interval. Because `reportOn` is `active`, the
gate keeps polling, so it also notices a later re-run of the apply workflow.

## Things to be aware of

- **Plans go stale.** `plan.txt` is the plan at hydration time. State can change before the pull
  request merges, so the applier plans again and warns (never fails) when the summary differs. The
  apply run's log is the authoritative plan.
- **Plans need state access.** Producing `plan.txt` requires the hydrate job to read remote state.
  Set the hydrate action's `plan` input to `"false"` when it cannot; the hydrated commit is still
  valid, just without `plan.txt`.
- **`plan.txt` is visible in the pull request.** Terraform redacts values marked `sensitive`;
  other attribute values appear in plain text. Mark sensitive variables and outputs and keep the
  configuration repository private.
- **Pin versions.** Pin `terraform_version` in both workflows and `required_version` in the
  configuration. `init -lockfile=readonly` makes the applier fail on provider drift instead of
  silently upgrading.
- **Renaming the apply job** silently breaks the gate: `check_name` must equal the job's `name`.
- Plans use `-lock=false` so hydration never blocks a running apply.

## Not covered yet

- Shared-active-branch mode (`activePath`), where several applications share one environment
  branch and `hydrator.metadata` lives under the application path.
- SCMs other than GitHub for the applier signal. The scripts are SCM-agnostic; on GitLab or
  Bitbucket, point the `WebRequestCommitStatus` at the equivalent pipeline API, or create a
  `CommitStatus` resource from the apply job.

## Running the tests

```
make lint-integrations   # shellcheck
make test-integrations   # script tests with a stub binary, then a real terraform/tofu when on PATH
```
