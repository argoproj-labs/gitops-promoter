# Integrations

Community-maintained hydrators and appliers that work with GitOps Promoter.

GitOps Promoter promotes **hydrated** commits through environment branches. It does not render
those commits itself and it does not deploy them. Anything that follows the
[custom hydrator contract](https://gitops-promoter.readthedocs.io/en/latest/advanced-usage/custom-hydrator/)
can produce them, and anything that reports a
[CommitStatus](https://gitops-promoter.readthedocs.io/en/latest/gating-promotions/) can gate them.
This directory collects runnable reference implementations of both halves so you can copy one
instead of starting from the contract.

| Integration | Hydrator | Applier | Runs on |
|---|---|---|---|
| [`terraform/`](terraform/) | DRY Terraform repo to per-environment root modules with `plan.txt` | `terraform apply` on the active branch, reported through a check run | GitHub Actions (shell scripts usable anywhere) |

## The contract in short

- **Proposed branches.** For each environment branch `environment/<env>` the hydrator pushes to
  `environment/<env>-next`. The `-next` suffix is fixed.
- **`hydrator.metadata`.** Every hydrated commit carries a JSON file at the repository root whose
  `drySha` names the DRY commit it was rendered from. The other fields (`repoURL`, `author`, `date`,
  `subject`, `body`, `references`) are optional and improve the UI.
- **Git note (optional).** When the rendered output did not change, record the new `drySha` as a
  note on `refs/notes/hydrator.metadata` instead of pushing an empty commit, so the dry SHA still
  advances.
- **Duplicate detection.** Do not create a commit, and therefore a pull request, when nothing
  changed.
- **Gating.** GitOps Promoter reads only `CommitStatus` resources matched by key and commit SHA. A
  status posted straight to the SCM is not read. Appliers report through a `CommitStatus`, either
  directly or through a built-in gate such as `WebRequestCommitStatus`.

## Adding an integration

1. Create `integrations/<name>/` with a `README.md` that explains the layout, inputs, and the
   Promoter manifests the integration needs.
2. Prefer shell scripts plus thin composite GitHub Actions. Composite actions must not contain
   `uses:` steps (no dependency bot watches this directory) or `${{ }}` expressions inside `run:`.
   Keep inputs in `env:` instead.
3. Scripts must pass `shellcheck` (`make lint-integrations`).
4. Add tests under `integrations/<name>/tests/` that run offline (`make test-integrations` must
   succeed without network access or cloud credentials). The Terraform integration shows the
   pattern: a recording stub binary for script logic, plus an optional smoke test with the real
   tool when it is on `PATH`.
5. Add a row to the table above and a page under `docs/integrations/`.

## Support

Integrations are maintained by the community and reviewed by the GitOps Promoter maintainers.
They are not part of the controller release, its images, or its versioning: pin them to a commit
SHA of this repository. Breaking changes are noted in each integration's README.
