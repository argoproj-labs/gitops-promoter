# Contributor skills

This page describes skills that are useful for working in the GitOps Promoter repository. Use it to assess your readiness, plan learning, or onboard new contributors.

---

## Core skills (must-have)

### 1. Go

- **Why**: Primary language (Go 1.25+). All controllers, SCM providers, and utilities are in Go.
- **Relevant areas**: `internal/`, `api/`, `cmd/`
- **Concepts**: modules, `context`, error handling, interfaces, struct tags, code generation (`//go:generate`)

**Self-check:**

- [ ] I can read and extend existing Go code confidently
- [ ] I'm comfortable with interfaces and dependency injection patterns
- [ ] I understand `context` for cancellation and request scope

---

### 2. Kubernetes operator development

- **Why**: GitOps Promoter is a Kubernetes operator. Controllers reconcile custom resources against desired state.
- **Relevant areas**: `internal/controller/`, `api/v1alpha1/`, `applyconfiguration/`
- **Concepts**:
  - **Controller-runtime / Kubebuilder**: `Reconcile()`, `SetupWithManager()`, watches, finalizers
  - **Custom resources**: type definitions, status subresources, conditions (e.g. `metav1.Condition`)
  - **Server-Side Apply (SSA)**: `Patch` with `ApplyConfiguration`, field ownership (see `internal/utils/apply_patch.go`)
  - **client-go**: `Client` (Get/List/Create/Patch/Delete), `Scheme`, informers if you touch caching

**Self-check:**

- [ ] I have built or modified at least one controller with controller-runtime
- [ ] I understand reconciliation loops and idempotency
- [ ] I know how to patch status using SSA or strategic merge patch

---

### 3. Git

- **Why**: The promoter drives promotion by reading and pushing to git (branches, notes, refs). Hydration metadata lives in git notes.
- **Relevant areas**: `internal/git/`, controller logic that consumes branch/SHA info
- **Concepts**: branches, refs, clone/fetch, **git notes** (e.g. `refs/notes/hydrator`), how the promoter infers "dry SHA" from hydrated commits

**Self-check:**

- [ ] I understand how branches and refs work beyond basic commit history
- [ ] I know what git notes are and how they're used in this project (see [Custom Hydrator](custom-hydrator.md))
- [ ] I can follow the dry → proposed → hydrated flow in the docs

---

## Important skills (high leverage)

### 4. Testing (Ginkgo/Gomega + envtest)

- **Why**: All controller logic is tested with BDD-style tests and a real API server (envtest). Adding or changing behavior requires writing or updating these tests.
- **Relevant areas**: `internal/controller/*_test.go`, `internal/controller/testdata/`, `internal/scms/fake/`
- **Concepts**: `Describe`/`Context`/`It`, `BeforeEach`/`AfterEach`, **envtest** (real apiserver/etcd), `Eventually` for async assertions, testdata YAML, the fake SCM provider

**Self-check:**

- [ ] I can run `make test` and interpret failures
- [ ] I have written or modified a Ginkgo test that uses envtest
- [ ] I understand how to clean up resources with `Eventually` and avoid flake

---

### 5. GitOps and promotion model

- **Why**: The code exists to implement a specific promotion workflow. Without this mental model, code changes are hard to reason about.
- **Relevant areas**: All controllers; see [Architecture](architecture.md) and [Gating Promotions](gating-promotions.md)
- **Concepts**: dry vs hydrated branches, proposed branches, promotion gates (CommitStatus), optional Argo CD health and soak time

**Self-check:**

- [ ] I can explain "dry branch" and "hydrated branch" to someone new
- [ ] I understand how PromotionStrategy → ChangeTransferPolicy → PullRequest relate
- [ ] I know how CommitStatus resources gate PR creation/merge (optional: Argo CD Application health)

---

### 6. SCM provider APIs

- **Why**: The promoter creates/updates/merges PRs and sets commit status via GitHub, GitLab, Azure DevOps, Bitbucket, Forgejo, Gitea. Bug fixes or new providers require API knowledge.
- **Relevant areas**: `internal/scms/<provider>/`, `api/v1alpha1/scmprovider_types.go`
- **Concepts**: REST APIs for PRs and commit statuses, auth (e.g. GitHub App, tokens), rate limits and idempotency

**Self-check:**

- [ ] I have used at least one provider’s API (e.g. GitHub or GitLab) for PRs or statuses
- [ ] I know how credentials are wired (ScmProvider / ClusterScmProvider)
- [ ] I can add a small improvement or fix to an existing provider

---

## Supporting skills (nice to have)

### 7. Build and code generation

- **Why**: CRDs and apply configs are generated. Changing types requires re-running codegen.
- **Relevant areas**: `Makefile`, `api/v1alpha1/`, `config/crd/`, `applyconfiguration/`
- **Concepts**: `make build`, `make manifests`, `make generate`, controller-gen markers, Kustomize for `config/`

**Self-check:**

- [ ] I can run `make generate` and `make manifests` after editing API types
- [ ] I know where CRD YAML and apply configurations are generated

---

### 8. Web / dashboard (optional)

- **Why**: There is a React dashboard and Argo CD UI extension. Only needed for UI work.
- **Relevant areas**: `ui/dashboard/`, `ui/extension/`, `ui/components-lib/`

**Self-check:**

- [ ] I can run and modify the dashboard or extension (if working on UI)

---

### 9. HTTP and webhooks (optional)

- **Why**: The webhook receiver triggers reconciliation on push events from SCMs.
- **Relevant areas**: `internal/webhookreceiver/`
- **Concepts**: Webhook payloads (e.g. GitHub/GitLab push), signature verification, idempotent handling

**Self-check:**

- [ ] I understand how a push event leads to a controller reconcile (if touching webhooks)

---

## Suggested learning path

1. **Go + Kubernetes basics** — Complete a Kubebuilder or controller-runtime tutorial (one controller, one CRD) so reconciliation and watches are familiar.
2. **This repo** — Read [AGENTS.md](../AGENTS.md) (in repo root), then trace one flow end-to-end (e.g. PromotionStrategy → ChangeTransferPolicy → PullRequest).
3. **Tests** — Run `make test`, open one controller test file, and see how envtest and the fake SCM are used; run a single test with `-ginkgo.focus`.
4. **SSA** — Read `internal/utils/apply_patch.go` and one controller that uses it (e.g. `commitstatus_controller.go`).
5. **One SCM** — Pick GitHub or GitLab, read the interfaces in `internal/scms/` and the implementation in `internal/scms/github/` or `internal/scms/gitlab/`.

---

## Quick reference

| Focus area        | Main skills                    | Key files / commands              |
|-------------------|--------------------------------|-----------------------------------|
| Controllers       | Go, operator patterns, SSA     | `internal/controller/*.go`, `make test` |
| SCM integrations  | Go, SCM APIs, auth             | `internal/scms/<provider>/`        |
| API / CRDs        | Go, Kubernetes types, codegen  | `api/v1alpha1/`, `make generate`   |
| Testing           | Ginkgo, envtest, fake SCM      | `*_test.go`, `internal/scms/fake/` |
| Git behavior      | Git, promotion model           | `internal/git/`, docs              |

For day-to-day commands and patterns, see [AGENTS.md](https://github.com/argoproj-labs/gitops-promoter/blob/main/AGENTS.md) in the repository root.
