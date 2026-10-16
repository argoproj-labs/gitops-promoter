# Test SCM server

`test/scmserver` is a test-only server that impersonates every SCM provider GitOps Promoter supports:

- GitHub (Enterprise)
- GitLab
- Azure DevOps
- Bitbucket Cloud
- Gitea
- Forgejo

It serves each provider's REST API over HTTPS, hosts real git repositories over smart HTTP, merges pull requests with real merge commits, and sends provider-shaped push webhooks. Point an `ScmProvider`'s `domain` at it and trust its CA, and the **unmodified** provider implementations in `internal/scms` run against it.

It is used in two places:

- **The contract suite** (`test/scmserver/*_test.go`, Ginkgo). It runs in `make test-parallel` and drives the real provider implementations against an in-process server.
- **The e2e suite** (`test/e2e/scm_providers_test.go`). It deploys the server into Kind and promotes a hydrated change through every provider with the deployed controller.

Controller integration tests under `internal/controller` keep using the in-process fake provider.

> [!WARNING]
> The server is not hardened. Run it only in tests and on your own machine.

## Running it locally

```shell
make run-scm-server
```

This starts:

- the admin API on `http://localhost:9000`;
- the provider APIs on `https://localhost:9001` (GitHub) through `:9006`;
- a generated CA, written to `bin/scm-server-ca.pem`;
- a token, `dev-token`, accepted by every provider.

`go run ./test/scmserver/cmd --help` lists every flag: listen addresses, `--tls-cert`/`--tls-key`, `--public-host`, `--config`, `--token` and more.

| Provider        | Port | API base                        | Git clone URL                                    | Credentials (Secret keys)                       |
|-----------------|------|---------------------------------|--------------------------------------------------|-------------------------------------------------|
| GitHub          | 9001 | `https://<domain>/api/v3`       | `https://<domain>/<owner>/<repo>.git`            | GitHub App (`githubAppPrivateKey`)              |
| GitLab          | 9002 | `https://<domain>/api/v4`       | `https://<domain>/<namespace>/<repo>.git`        | `token`                                         |
| Azure DevOps    | 9003 | `https://<domain>/<org>`        | `https://<domain>/<org>/<project>/_git/<repo>`   | `token` (PAT)                                   |
| Bitbucket Cloud | 9004 | `https://<domain>/2.0`          | `https://<domain>/<workspace>/<repo>.git`        | `token`                                         |
| Gitea           | 9005 | `https://<domain>/api/v1`       | `https://<domain>/<owner>/<repo>.git`            | `token`, or `username` and `password`           |
| Forgejo         | 9006 | `https://<domain>/api/v1`       | `https://<domain>/<owner>/<repo>.git`            | `token`, or `username` and `password`           |

To run a local controller against it:

1. Create a repository and seed it through the admin API:

    ```shell
    curl -X POST localhost:9000/api/repos -d '{"provider":"gitlab","owner":"acme","name":"deploy","init":true}'
    curl -X POST localhost:9000/api/repos/1001/commits \
      -d '{"branch":"environment/development-next","files":{"hydrator.metadata":"{\"drySha\":\"n/a\"}"}}'
    ```

2. Point an `ScmProvider` at the listener with `domain: localhost:9002`.
3. Start the controller with `SSL_CERT_FILE` and `GIT_SSL_CAINFO` set to the CA file.

For GitHub, register an App and its installations with `POST /api/github/apps`; the server verifies the App's JWTs with the registered key. Bitbucket Cloud uses the optional `spec.bitbucketCloud.domain` field.

## Admin API

The admin API is plain HTTP. `scmserver.AdminClient` wraps it for Go tests.

| Endpoint                                                | Purpose                                                                         |
|---------------------------------------------------------|---------------------------------------------------------------------------------|
| `GET /healthz`, `GET /ca.pem`, `GET /api/info`          | Health, the CA to trust, and each provider's address and `domain` value          |
| `POST /api/credentials`                                 | Accept a token, or a username and password, for a provider                      |
| `POST /api/github/apps`                                 | Register a GitHub App (public key) and its installations                        |
| `POST /api/repos`, `GET/DELETE /api/repos/{id}`         | Create, inspect (including branches) or delete repositories                     |
| `POST /api/repos/{id}/commits`                          | Write a commit (files, deletions, git notes) to a branch, as a push would        |
| `PUT /api/repos/{id}/branches/{branch}`                 | Point a branch at an existing commit                                            |
| `PUT /api/repos/{id}/protection/{branch}`               | Require statuses before pull requests into the branch can merge                 |
| `GET /api/pulls`, `GET /api/repos/{id}/pulls/{n}`       | Server-side pull request state, including merge commits and completion queues   |
| `POST /api/repos/{id}/pulls/{n}/actions`                | `merge` (with `"squash": true` for a squash commit), `close`, `reopen`, `set-labels`, `hide`, `show`, `delete`, and for Azure DevOps `complete-now` and `fail-completion` |
| `GET /api/statuses?repo=&sha=`                          | Commit statuses, build statuses and check runs                                  |
| `GET/DELETE /api/requests`                              | The request log                                                                 |
| `GET /api/behavior`, `PUT /api/behavior/{provider}`     | Read or change provider behavior (see below)                                    |
| `GET/POST/DELETE /api/faults`                           | Fault injection rules                                                           |
| `GET/POST /api/webhooks`, `GET /api/webhooks/deliveries` | Push webhook subscriptions and the delivery log                                 |
| `POST /api/reset`                                       | Delete all state                                                                |

`--config` preloads credentials, GitHub Apps, repositories, webhooks and behavior from a YAML or JSON file.

## Provider behavior it reproduces

The server reproduces the provider behavior the controller has to cope with. Each item is pinned by a spec in `test/scmserver/quirks_test.go`.

- **Azure DevOps**
    - A completion request (`status=completed`) returns 200 while the pull request stays `active` with `mergeStatus: queued` and the *preview* merge commit. The real merge lands `completionDelay` later.
    - Conflicts and failed policies leave the pull request active with a merge failure.
    - The SDK's `OPTIONS /{org}/_apis` route discovery is served.
- **GitLab**
    - `detailed_merge_status` is `checking` for `mergeabilityDelay`, and merges during that window get 405.
    - Commit statuses follow GitLab's state machine; re-sending the same state gets 400 "Cannot transition status…".
- **Gitea and Forgejo**
    - A stale `head_commit_id` gets 409, a conflict or ongoing check gets 405, and both SDKs report these as success.
    - Webhooks carry their GitHub compatibility headers, so the promoter's receiver classifies them as GitHub.
- **Bitbucket Cloud**
    - Merge responses carry abbreviated hashes.
    - With `asyncMergeDelay`, merges answer 202 with a task-status link.
- **GitHub**
    - App JWT and installation-token auth.
    - `mergeable: null` while mergeability is computed.
    - `X-RateLimit-*` headers, and a 403 once the limit is spent.
    - Duplicate pull requests get 422, and a stale merge SHA gets 409.
- **All providers**
    - Native error bodies and pagination.
    - Branch protection with each provider's rejection.
    - List lag: new pull requests are hidden from list endpoints for `listLag`.

`Behavior` holds the per-provider knobs:

- `latency`, `latencyJitter`
- `listLag`, `mergeabilityDelay`, `completionDelay`, `asyncMergeDelay`
- `tokenTTL`, `rateLimit`, `rateLimitWindow`
- `webhookDelay`, `webhookDropRate`

The defaults (`scmserver.DefaultBehavior`) are asynchronous where the real provider is, but fast enough for happy-path promotions.

A `FaultRule` makes matching requests fail. It matches on provider, method and path regexp. It answers with a status and a provider-native error, adds a delay, or drops the connection. It can apply a limited number of times or with a probability.

## The e2e suite

`make test-e2e` runs `test/e2e` against a Kind cluster. The "SCM providers" context:

1. builds and loads the image (`make docker-build-scm-server`);
2. issues a TLS certificate for `scm-server.scm-server.svc` and deploys `test/scmserver/deploy`;
3. mounts the CA into the controller and sets `SSL_CERT_FILE` and `GIT_SSL_CAINFO`;
4. port-forwards the admin API;
5. runs one table entry per provider. Each entry creates a repository with a webhook to the promoter's receiver, seeds the environment branches, applies an `ScmProvider`, `GitRepository` and `PromotionStrategy`, hydrates a change, and waits for it to merge into development and staging.
6. runs a second table entry per provider with auto-merge off. A person merges each pull request on the provider through the admin API: first with a merge commit, then with a squash commit, then with a merge commit again. After each merge the entry checks that the controller finalizes the `PullRequest`, records the merge in the environment's `ChangeTransferPolicyHistory`, and reports the dry commit as active. The last round follows a squash, so its pull request conflicts until the controller merges the environment branch back into the proposed branch.

## Adding a provider

When you add an SCM provider (see [Adding an SCM Provider](adding-an-scm-provider.md)):

- teach the server its API: a `test/scmserver/<provider>.go` with routes registered in `Server.providerHandler`;
- add its push webhook format to `webhook.go`;
- add it to `AllProviders` and to the `Entry` lists of the contract, webhook and both e2e tables;
- give any provider-specific behavior a spec in `quirks_test.go`.
