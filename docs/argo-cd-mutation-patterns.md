# Argo CD UI mutation pattern (repo creation example)

This note summarizes how Argo CD handles a mutation from UI → API server → repo server using repository creation as the concrete example. It’s focused on the handoff after the UI hits the API server and the patterns that enable repo-server work.

## 1) UI entry point and REST call

In the UI, repository creation goes through a service layer that wraps REST endpoints:

- The UI service posts to `/repositories` or `/write-repositories` with a serialized `Repository` payload (URL, creds, project, etc.). See the `RepositoriesService` methods in `repo-service.ts` (e.g., `createHTTPS`, `createSSH`, `createGitHubApp`, and their `Write` variants):
  - https://github.com/argoproj/argo-cd/blob/main/ui/src/app/shared/services/repo-service.ts#L91-L140
  - https://github.com/argoproj/argo-cd/blob/main/ui/src/app/shared/services/repo-service.ts#L191-L246
  - https://github.com/argoproj/argo-cd/blob/main/ui/src/app/shared/services/repo-service.ts#L247-L268

In the settings UI, “Connect Repo” uses this service to send the mutation and then refreshes the list:
- https://github.com/argoproj/argo-cd/blob/main/ui/src/app/settings/components/repos-list/repos-list.tsx#L339-L370

**Pattern:** UI calls a thin `services/*` layer that maps user inputs → REST POSTs. The UI uses a loader pattern to refresh list views after mutation.

## 2) API server repository service (RBAC + validation + persistence)

The API server’s repository service is in `server/repository/repository.go`.

Core patterns in `CreateRepository` and `CreateWriteRepository`:

- **RBAC enforced at the API boundary** using `enf.EnforceErr` and a repo-scoped RBAC object (`project/repo`):
  - https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go#L434-L457
  - https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go#L78-L92

- **Input validation** (payload presence, credentials required for write repos, etc.):
  - https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go#L434-L457
  - https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go#L493-L516

- **Repo connectivity test** prior to persistence, via `testRepo` (this is where repo-server is invoked):
  - https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go#L762-L785
  - https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go#L434-L457

- **Persistence** is through the Argo DB abstraction, which stores repos as Kubernetes Secrets. The DB backend’s `CreateRepository` writes a secret and resyncs informers:
  - https://github.com/argoproj/argo-cd/blob/main/util/db/repository_secrets.go#L28-L59

**Pattern:** API server handles authorization, validation, and orchestration. It does not directly clone or validate repositories; it delegates that to repo-server before persisting credentials.

## 3) API server → repo-server call path

The `testRepo` helper in the API server is the key entry point. It uses a repo-server clientset to open a gRPC connection, then calls `TestRepository`:

- Client creation + gRPC call:
  - https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go#L762-L785

- Clientset implementation (connects to repo-server and returns a gRPC client):
  - https://github.com/argoproj/argo-cd/blob/main/reposerver/apiclient/clientset.go#L48-L55

**Pattern:** API server invokes repo-server via a shared gRPC clientset, with a very narrow “work” surface (e.g., `TestRepository`, `ListRefs`, `GetAppDetails`). API server remains the RBAC and validation boundary; repo-server does the expensive git/helm/oci operations.

## 4) Repo-server side behavior

Repo-server’s `TestRepository` is implemented in `reposerver/repository/repository.go`. It runs repo-type specific validation (git, helm, oci) and uses credentials provided in the request:

- https://github.com/argoproj/argo-cd/blob/main/reposerver/repository/repository.go#L2786-L2806

**Pattern:** repo-server functions are pure “backend tasks” (fetch, list refs, generate manifests, validate) that are triggered by API server gRPC calls; they do not manage auth or persistence themselves.

## 5) Architectural context (component responsibilities)

The operator manual explicitly documents the API server and repo-server responsibilities:
- https://github.com/argoproj/argo-cd/blob/main/docs/operator-manual/architecture.md#L7-L28

This aligns with the observed code paths:
- API server: auth + mutation orchestration + persistence
- Repo server: git/helm/oci I/O and computation

## 6) What this implies for “Merge proposed into active”

For GitOps Promoter, a mutation like “merge proposed into active” should mirror Argo CD’s pattern:

1) **UI service** calls a REST endpoint (or gRPC gateway) such as `/promotion/merge` with payload (env, PR id, strategy).
2) **API server handler** performs RBAC/validation, resolves SCM credentials, and **delegates the merge operation** to an internal service (or a repo-server-like worker) that does the SCM action.
3) **Worker/repo service** executes the merge (GitHub/GitLab/etc.), returns result/metadata.
4) **API server persists status** on the Promotion CR and returns a concise response for UI updates.

This keeps the same separation of concerns: UI → API server (auth & orchestration) → specialized worker (side-effectful git/SCM operations).

## 7) Suggested coding pattern to adopt

- **Service layer in UI** (single file per domain) with typed methods and clear REST endpoints.
- **Server-side service object** (like `repository.Server`) that:
  - Enforces RBAC at the API boundary
  - Validates requests
  - Calls a narrow worker/repo API (gRPC or internal interface)
  - Persists to CRs / secrets / DB
- **Repo/SCM worker interface** with small, testable RPC surface:
  - `TestAccess`, `MergePullRequest`, `CreatePullRequest`, `GetRefs`
- **Controller or reconciler** updates status asynchronously where needed.

## Key references

- UI REST calls for repo creation: https://github.com/argoproj/argo-cd/blob/main/ui/src/app/shared/services/repo-service.ts
- API server repo creation and repo-server delegation: https://github.com/argoproj/argo-cd/blob/main/server/repository/repository.go
- Repo-server `TestRepository`: https://github.com/argoproj/argo-cd/blob/main/reposerver/repository/repository.go
- Architecture responsibilities: https://github.com/argoproj/argo-cd/blob/main/docs/operator-manual/architecture.md
