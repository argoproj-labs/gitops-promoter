# Threat model

GitOps Promoter promotes Git config across environment branches by opening and merging pull requests, gated on commit statuses. Two workloads: the controller in `cmd/main.go` (reconcilers, admission webhooks, an SCM webhook receiver on port 3333, `git` against bare clones, and GitHub, GitLab, Forgejo, Gitea, Bitbucket Cloud, and Azure DevOps) and `gitops-promoter apiserver`, which serves `view.promoter.argoproj.io` through `APIService v1alpha1.view.promoter.argoproj.io`.

## Trust

- Install manifests under `config/` and `dist/` (CRDs, RBAC, Deployments, Services, the APIService object) are trusted. Only a cluster admin can change them. Cluster-admin is not an attacker.
- `ControllerConfiguration` and `ClusterScmProvider` are admin config. Every other promoter CR is untrusted tenant input. A namespace is the tenant boundary; tenants are not fully trusted and must not affect each other.
- CRD validation holds. Markers in `api/v1alpha1` are OpenAPI and CEL on the shipped CRDs. A unit-test object the API server would reject is not a finding.
- Git objects and ref names (`internal/git`), SCM responses (`internal/scms/`), and webhook bodies (`internal/webhookreceiver`) are untrusted. A branch, path, or message that becomes a `git` argument is in scope, as is anything from the repo that gets executed. Webhook signatures are not checked yet ([issue 1285](https://github.com/argoproj-labs/gitops-promoter/issues/1285)); restating that, with no effect beyond enqueueing a reconcile, is a known gap.
- A secret named by a tenant `GitRepository` or `ScmProvider` belongs to that tenant. A cross-namespace secret read, or a tenant using a `ClusterScmProvider` credential it should not have, is in scope.

## Where to look

`internal/git` (including `hack/git/promoter_askpass.sh`), the push and credential path, the webhook receiver, `internal/scms/`, `internal/apiserver`, and `ui/`. Cross-namespace effects and SCM or git data rendered without encoding matter. `test/e2e`, docs, and generated deepcopy and OpenAPI are out of scope.

Code execution or theft of an SCM credential is critical. A commit that skipped a configured gate, or any cross-namespace effect, is high. A crash that ends with that request is low. Name the input and the commit it was tried against.

From `/src`, with `GOPROXY=off` and `GOTOOLCHAIN=local`: `make test-parallel`, `make test-ui-test-dashboard`, `make test-unit-test-extension`, `make fuzz-replay`. The manager is `bin/manager` (`/usr/local/bin/gitops-promoter`). `make test-e2e` needs Kind and is not runnable here.
