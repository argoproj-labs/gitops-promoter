# Contributing to GitOps Promoter

Thanks for helping improve GitOps Promoter. The project is still young; we keep this guide short so you can ship a fix or feature without a long checklist.

## From idea to merge

1. **Open an issue or discussion** when it helps: bugs and feature work benefit from an [issue](https://github.com/argoproj-labs/gitops-promoter/issues); questions and design chat fit [Discussions](https://github.com/argoproj-labs/gitops-promoter/discussions). Small or obvious changes can go straight to a PR when the intent is clear.
2. **[Fork the repository](https://github.com/argoproj-labs/gitops-promoter/fork)** on GitHub, clone your fork, and create a branch from **`main`**. Push work to your fork only.
3. **Make focused changes** — one logical change per PR when possible.
4. **Run checks** that match what you changed:
   - **Go only** (no CRD, webhook, or generated install / apply-config churn): `go mod tidy`, `make lint`, `make test-parallel`.
   - **APIs, CRDs, webhooks, or bundled install YAML** (anything `make build-installer` regenerates—`config/`, `dist/install.yaml`, deepcopy/applyconfiguration, extension icon output): **`make build-installer`**, commit the full diff, then `go mod tidy`, `make lint`, `make test-parallel`.
   - **`ui/`**: `make lint-ui`, `make test-unit-test-extension`, `make test-ui-test-dashboard`.
   - **`docs/`** (this site): `make lint-docs`.
   - CI also runs **Nilaway** and **spell checking**; use **`make nilaway-no-test`** locally if you want parity before pushing.
5. **Open a pull request from your fork** into `main` on `argoproj-labs/gitops-promoter`, with a short title and enough context for reviewers (what changed, why). Use `Fixes #123` / `Closes #123` when it applies. Every commit must be **DCO sign-off** (see below).
6. **Iterate on review** — CI must be green; maintainers will help get it over the line.

## DCO sign-off

Pull requests must pass the [**Developer Certificate of Origin (DCO)**](https://github.com/apps/dco/) check: every commit needs a `Signed-off-by` line (this is not GPG signing). The email in that line should match your GitHub account. Use **`git commit -s`** / **`git commit --amend -s`** when you forget.

To add a **`prepare-commit-msg`** hook that signs off automatically, follow [Argoproj’s instructions](https://github.com/argoproj/argoproj/blob/main/community/CONTRIBUTING.md#legal): copy [`community/dco-signoff-hook/prepare-commit-msg`](https://github.com/argoproj/argoproj/blob/main/community/dco-signoff-hook/prepare-commit-msg) from [`argoproj/argoproj`](https://github.com/argoproj/argoproj) into **`.git/hooks/prepare-commit-msg`** in your clone (merge with an existing hook if you already have one), and ensure it is executable (`chmod +x`).

## Setup

- **Go** — Match `go.mod` / CI (currently **Go 1.26** in `.github/workflows/ci.yaml`).
- **Make** — Run `make help` from the repo root; most tasks are wired there.
- **Node.js** — Needed only for `ui/` work (CI uses **Node 24**).
- **Python 3.13** — Only if you run `make lint-docs` locally (same flow as CI: venv + `docs/requirements-hashed.txt`).

## Where things live

| Area | Path |
|------|------|
| Controller entrypoint | [`cmd/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/cmd) |
| Reconcilers | [`internal/controller/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/internal/controller) |
| API types (CRDs) | [`api/v1alpha1/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/api/v1alpha1) |
| Install / RBAC / CRD bases | [`config/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/config) |
| Metrics | [`internal/metrics/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/internal/metrics) |
| User docs (MkDocs) | [`docs/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/docs) |
| Dashboard & Argo CD extension | [`ui/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/ui) |
| Envtest-based integration tests | [`internal/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/internal) (via Ginkgo) |

## Design patterns

- **Reconcilers** — Follow controller-runtime style used in `internal/controller/`: idempotent reconcile loops, structured logging, and the same client/scheme/recorder patterns as neighboring controllers.
- **Tests** — Extend Ginkgo suites under `internal/` and reuse envtest patterns from [`internal/controller/suite_test.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/controller/suite_test.go) when testing cluster behavior; mirror how similar resources are covered today.
- **APIs and RBAC** — Express API permissions with **`// +kubebuilder:rbac`** on the reconcilers in **`internal/controller/*_controller.go`** (not by editing `config/rbac/role.yaml`). Regenerate via **`make build-installer`** so rules stay aligned with what the binary actually needs.
- **Commit status controllers** — If you create or update `CommitStatus` objects, follow [Commit status development best practices](commit-status-controllers/development-best-practices.md) (standard labels, owner references, and keys that match `PromotionStrategy` config).
- **Metrics** — Register operational metrics in `internal/metrics` and describe them for operators in [Metrics](monitoring/metrics.md) (see the note in [`internal/metrics/metrics.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/metrics/metrics.go)).

## Questions?

Use [Discussions](https://github.com/argoproj-labs/gitops-promoter/discussions) or comment on your PR. Thanks again.
