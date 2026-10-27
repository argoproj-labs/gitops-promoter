# Test SCM server

`test/scmserver` is a test-only server that impersonates every SCM provider GitOps Promoter supports:

- GitHub (Enterprise)
- GitLab
- Azure DevOps
- Bitbucket Cloud
- Gitea
- Forgejo
- Bitbucket Data Center

It serves each provider's REST API over HTTPS, hosts real git repositories over smart HTTP, merges pull requests with real merge commits, and sends provider-shaped push and pull request webhooks. Point an `ScmProvider`'s `domain` at it and trust its CA, and the **unmodified** provider implementations in `internal/scms` run against it.

It is used in three places:

- **The contract suite** (`test/scmserver/*_test.go`, Ginkgo). It runs in `make test-parallel` and drives the real provider implementations against an in-process server.
- **The e2e suite** (`test/e2e/scm_providers_test.go`). It deploys the server into Kind and promotes a hydrated change through every provider with the deployed controller.
- **Controller integration tests** (`internal/controller`, opt-in). By default those tests still use the in-process fake provider. You can point them at a running scmserver provider, and a weekly workflow does that for every provider with faults injected (see [Controller integration tests against scmserver](#controller-integration-tests-against-scmserver)).

> [!WARNING]
> The server is not hardened. Run it only in tests and on your own machine.

## Running it locally

```shell
make run-scm-server
```

This starts:

- the admin API on `http://localhost:9000`;
- the provider APIs on `https://localhost:9001` (GitHub) through `:9007` (override with `SCM_SERVER_PORT_BASE`, see below);
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
| Bitbucket Data Center | 9007 | `https://<domain>/rest/api/1.0` | `https://<domain>/scm/<projectKey>/<repo>.git` | `token`, or `username` and `password`      |

To run a local controller against it:

1. Create a repository and seed it through the admin API:

    ```shell
    curl -X POST localhost:9000/api/repos -d '{"provider":"gitlab","owner":"acme","name":"deploy","init":true}'
    curl -X POST localhost:9000/api/repos/1001/commits \
      -d '{"branch":"environment/development-next","files":{"hydrator.metadata":"{\"drySha\":\"n/a\"}"}}'
    ```

2. Point an `ScmProvider` at the listener with `domain: localhost:9002`.
3. Start the controller with `SSL_CERT_FILE` and `GIT_SSL_CAINFO` set to the CA file.

For GitHub, register an App and its installations with `POST /api/github/apps`; the server verifies the App's JWTs with the registered key. Bitbucket Cloud uses the optional `spec.bitbucketCloud.domain` field. A Bitbucket Data Center repository's `owner` is its project key, which may contain only letters, digits and `_`.

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
| `POST /api/repos/{id}/pulls/{n}/actions`                | `merge` (with `"squash": true` for a squash merge, which on GitLab squashes and then merges), `close`, `reopen`, `set-labels`, `hide`, `show`, `delete`, and for Azure DevOps `complete-now` and `fail-completion` |
| `GET /api/statuses?repo=&sha=`                          | Commit statuses, build statuses and check runs                                  |
| `GET/DELETE /api/requests`                              | The request log                                                                 |
| `GET /api/behavior`, `PUT /api/behavior/{provider}`     | Read or change provider behavior (see below)                                    |
| `GET/POST/DELETE /api/faults`                           | Fault injection rules                                                           |
| `GET/POST /api/webhooks`, `GET /api/webhooks/deliveries` | Webhook subscriptions and the delivery log                                      |
| `POST /api/reset`                                       | Delete all state                                                                |

`--config` preloads credentials, GitHub Apps, repositories, webhooks and behavior from a YAML or JSON file.

## Provider behavior it reproduces

The server reproduces the provider behavior the controller has to cope with. Each item is pinned by a spec in `test/scmserver/quirks_test.go`.

Where a provider is open source (GitLab, Gitea, Forgejo) the behavior follows its server code; otherwise it follows the official REST documentation and the SDK the promoter uses. Bitbucket Data Center follows Atlassian's REST API (OpenAPI spec for 10.0), event payload and webhook documentation; its error messages follow the documented exception names, but their wording is not verified against a real server.

- **Webhooks**
    - Every branch update sends the provider's push event, and every pull request transition sends its pull request event: GitHub's and Gitea's `pull_request` (a merge is `closed` with `merged: true`), GitLab's `Merge Request Hook` (`open`, `close`, `reopen`, `merge`), Azure DevOps' `git.pullrequest.created` and `git.pullrequest.updated`, Bitbucket Cloud's `pullrequest:created`, `pullrequest:rejected` and `pullrequest:fulfilled` (Bitbucket Cloud has no reopen), and Bitbucket Data Center's `repo:refs_changed` (one request per changed ref), `pr:opened`, `pr:declined` and `pr:merged` (Data Center has no reopen event). A pull request a push settles (its head reaches the target branch, or its source branch is deleted) sends the same event the provider would.
    - Deliveries carry the provider's headers and signatures, and the promoter's webhook receiver classifies each one the way it does the real provider's. The receiver acts on push events only; it accepts pull request events with 204 and ignores them for now.
- **Azure DevOps**
    - A completion request (`status=completed`) returns 200 while the pull request stays `active` with `mergeStatus: queued` and the *preview* merge commit. The real merge lands `completionDelay` later.
    - Pull requests have iterations: one when created and one for every update of the source branch (`GET …/pullRequests/{id}/iterations`). Pull request statuses (`POST …/pullRequests/{id}/statuses`) may name an iteration; every post adds a status.
    - Branch protection is a status policy, which reads pull request statuses, never commit statuses. A required status is `genre/name`, or `name` for any genre. The latest pull request status for it must be `succeeded` and posted for the latest iteration or for no iteration, as with the policy's "reset status whenever there are new changes" option.
    - A completion request for a conflicting pull request, or one whose required statuses have not passed, is refused with 400; a stale `lastMergeSourceCommit` gets 409. Azure DevOps does not document these responses. `azureAsyncCompletionFailures` switches to the other model: the request is accepted and fails later, leaving the pull request active with `mergeFailureMessage`.
    - The SDK's `OPTIONS /{org}/_apis` route discovery is served.
    - A pull request whose head is pushed to the target branch stays active.
- **GitLab**
    - `detailed_merge_status` is `checking` for `mergeabilityDelay`. The merge API checks mergeability synchronously, so a merge during that window still succeeds.
    - A `sha` that does not match the head, an empty one included, gets 409 before any other check; a closed, conflicting or pipeline-blocked merge request gets 405 (GitLab 16 to 18 answered a conflict with 422).
    - A squashed merge request reports both `squash_commit_sha` and `merge_commit_sha`: the default merge method merges the squash commit with a merge commit, which is the target branch tip.
    - Commit statuses follow GitLab's state machine; re-sending the same state gets 400 "Cannot transition status…".
    - A missing branch on create is a 400 validation error keyed by attribute.
- **Gitea and Forgejo**
    - A stale `head_commit_id` gets 409, a closed pull request 404, and a conflict, ongoing check or missing required status 405. Both SDKs report these as not merged with no error, which the providers turn into an error.
    - Label lists carry `X-Total-Count` but no `Link` header, and label names are not unique.
    - A pull request whose head is already in the base is created (status Ancestor), and `GET /pulls` without `state` lists every pull request.
    - Webhooks carry their GitHub compatibility headers, so the promoter's receiver classifies them as GitHub.
- **Bitbucket Cloud**
    - Merge responses carry abbreviated hashes.
    - There is no head SHA guard: the merge API takes no expected head commit, so a pull request whose source branch moved is merged at its new head. This follows the REST documentation and the clients the promoter could use; it is not yet verified against Bitbucket Cloud, including with a merge queue.
    - With `asyncMergeDelay` (or `?async=true`), merges answer 202 with the task-status link in the `Location` header and no body; merging a queued pull request again returns the same 202. Unknown tasks poll as `PENDING`, and a failed task answers 400.
- **Bitbucket Data Center**
    - A bearer token is accepted by the REST API and by git. A token sent as a basic auth password is accepted only with its owner's user name (`scmserver.TokenUser`), so `x-token-auth:<token>` is refused; this is why the provider sends `Authorization: Bearer` to git through `http.extraHeader`.
    - Every update, decline, reopen and merge must name the pull request's current `version`, and gets 409 `PullRequestOutOfDateException` otherwise. Edits, rescopes (pushes to the source branch), declines, reopens and merges increase the version. There is no head SHA guard.
    - A second open pull request for the same branches gets 409 `DuplicatePullRequestException`, and one with nothing to merge 409 `EmptyPullRequestException`.
    - Conflicting merges and failed merge checks get 409 `PullRequestMergeVetoedException`. Only the `no-ff` and `squash` merge strategies are enabled.
    - Branch protection is the Required Builds merge check. The protection's required statuses are build parent keys, matched against the `parent` of builds posted to `/rest/api/1.0/projects/{key}/repos/{slug}/commits/{sha}/builds`; the latest build with that parent on the head commit must be `SUCCESSFUL`. A build without a `parent` never satisfies it, and statuses posted to the deprecated `/rest/build-status/1.0/commits/{sha}` are stored and listed but never satisfy it either.
    - A merged pull request reports `properties.mergeCommit`, except after a squash merge; its `MERGED` activity always names the commit. Deleting a pull request's source branch declines it.
- **GitHub**
    - App JWT (App ID or client ID as issuer) and installation-token auth; JWT requests are rate limited too.
    - `mergeable: null` while mergeability is computed; `merge_commit_sha` keeps the test merge commit on a pull request closed unmerged.
    - `X-RateLimit-*` headers, and a 403 once the limit is spent.
    - Duplicate pull requests get 422, and a stale merge SHA gets 409. Squash merges are allowed; rebase merges are not.
    - Check runs are App-only (403 for a token), and reject an empty `details_url` or an `output` without `title` and `summary` with 422. Label colors must be hexadecimal without `#`.
- **All providers but Azure DevOps**
    - A pull request whose head commit is pushed to the target branch is marked merged, with the merge commit (or the head, after a fast-forward) as its merge commit.
    - GitHub, GitLab, Gitea, Forgejo and Bitbucket Data Center close a pull request whose source branch is deleted.
- **All providers**
    - Native error bodies and pagination.
    - Branch protection with each provider's rejection.
    - List lag: new pull requests are hidden from list endpoints for `listLag`. This is a synthetic fault; no provider's REST list endpoint documents such lag.

`Behavior` holds the per-provider knobs:

- `latency`, `latencyJitter`
- `listLag`, `mergeabilityDelay`, `completionDelay`, `asyncMergeDelay`, `azureAsyncCompletionFailures`
- `tokenTTL`, `rateLimit`, `rateLimitWindow`
- `webhookDelay`, `webhookDropRate`

The defaults (`scmserver.DefaultBehavior`) are asynchronous where the real provider is, but fast enough for happy-path promotions.

A `FaultRule` makes matching requests fail. It matches on provider, method and path regexp. It answers with a status and a provider-native error, adds a delay, or drops the connection. It can apply a limited number of times or with a probability.

## Controller integration tests against scmserver

PR CI and `make test-parallel` always use the fake provider. To exercise the real provider implementations from the envtest suite locally:

1. Start the server in one terminal (or run `make start-scm-server` to start it in the background and log to `bin/scm-server.log`):

    ```shell
    make run-scm-server
    ```

2. In another terminal, run the controller suite against one provider:

    ```shell
    make test-scm-provider SCM_PROVIDER=gitea
    ```

`SCM_PROVIDER` may be any of `github`, `gitlab`, `azuredevops`, `bitbucket`, `gitea`, `forgejo`, or `bitbucketdatacenter`. Specs that call the in-process fake package directly are labeled `fake-scm-only` and are skipped automatically (the Makefile also passes `--label-filter='!fake-scm-only'`).

| Environment variable | Default | Purpose |
|----------------------|---------|---------|
| `PROMOTER_TEST_SCM_PROVIDER` | `fake` (unset) | Provider name, or `fake` |
| `PROMOTER_TEST_SCM_ADMIN_URL` | `http://127.0.0.1:9000` | Admin API base URL |
| `PROMOTER_TEST_SCM_CA_FILE` | `bin/scm-server-ca.pem` | CA written by `make run-scm-server` |
| `PROMOTER_TEST_SCM_TOKEN` | `dev-token` | Token accepted by every provider |
| `PROMOTER_TEST_SCM_FAULT_PROBABILITY` | unset (no faults) | Fraction in (0, 1] of the provider's API and git requests to fail with a 500 |
| `K8S_FAULT_PROBABILITY` | unset (no faults) | Fraction in (0, 1] of the controllers' Kubernetes writes to fail (see `internal/k8sfault`) |

The suite creates a fresh repository on the server for each test factory call, registers a webhook to the in-process webhook receiver, and trusts the CA via `SSL_CERT_FILE` / `GIT_SSL_CAINFO`. It also turns off the server's rate limiting, since every spec shares one token. The Makefile target puts `hack/git` on `PATH` so git can authenticate through `promoter_askpass.sh`; add it yourself if you run ginkgo directly.

With `PROMOTER_TEST_SCM_FAULT_PROBABILITY` set, the first Ginkgo process replaces the server's fault rules with one rule for the selected provider. With either kind of fault enabled, the suite shortens every rate limiter's slow delay to 5s, so a reconcile that fails several times in a row retries within the specs' timeouts. The specs' own git commands send the `X-Scmserver-No-Fault` header, which exempts a request from every fault rule, so only the controllers see the faults. With either kind of fault enabled, specs stop asserting that Kubernetes Events were emitted: events are best-effort, client-go drops a faulted Event write, and an injected failure can change which events a controller sends.

The `fault injection scheduled` workflow (`.github/workflows/fault-injection-scheduled.yaml`) runs `make test-scm-provider` for every provider each Monday with both kinds of faults at 2%. When a scheduled run fails, it opens an issue labelled `fault-injection`, or comments on the open one, and it closes that issue after a run that passes. Run it by hand from the Actions tab to try other providers or rates; manual runs don't touch the issue.

If port 9000 is already taken on your machine (Zscaler, for example, listens on `127.0.0.1:9000`), set `SCM_SERVER_PORT_BASE`. The admin API listens on that port and the seven providers on the next seven, in the order of the table above. Pass the same value to both targets:

```shell
make run-scm-server SCM_SERVER_PORT_BASE=9100
make test-scm-provider SCM_PROVIDER=gitea SCM_SERVER_PORT_BASE=9100
```

`make run-scm-server` logs every provider API request (provider, method, path, status, duration and principal) at the `debug` level. Set `SCM_SERVER_LOG_LEVEL=info` to log only startup and errors. Git clone, fetch and push requests and admin API calls are not logged; `GET /api/requests` returns every provider request, git included, as JSON.

## The e2e suite

`make test-e2e` runs `test/e2e` against a Kind cluster. The "SCM providers" context:

1. builds and loads the image (`make docker-build-scm-server`);
2. issues a TLS certificate for `scm-server.scm-server.svc` and deploys `test/scmserver/deploy`;
3. mounts the CA into the controller and sets `SSL_CERT_FILE` and `GIT_SSL_CAINFO`;
4. port-forwards the admin API;
5. runs the plain promotion for every provider at once. Each provider gets a repository with a webhook to the promoter's receiver, seeded environment branches, and an `ScmProvider`, `GitRepository` and `PromotionStrategy`; the spec hydrates a change and waits for it to merge into development and staging.
6. runs the gated promotion for every provider at once. A proposed commit status gates development, and the controller reports it on the provider. The spec checks that the controller holds the pull request while the gate is pending, keeps the same pull request when a newer hydrated commit moves the proposed branch, keeps holding when the gate passes only on the stale commit, and merges the newer commit once the gate passes on it.
7. runs the hand-merged promotion for every provider at once, with auto-merge off. A person merges each pull request on the provider through the admin API: first with a merge commit, then with a squash commit, then with a merge commit again. After each merge the spec checks that the controller finalizes the `PullRequest`, records the merge in the environment's `ChangeTransferPolicyHistory`, and reports the dry commit as active. The last round follows a squash, so its pull request conflicts until the controller merges the environment branch back into the proposed branch.
8. closes the controller's pull request on every provider at once, and checks that the provider's pull request webhook reaches the receiver, and that the controller opens a new pull request for the same proposed commit, tracks only the new one, and promotes through it.
9. requires a status on the development branch of every provider at once. It checks that the controller keeps the pull request open while the provider refuses the merge, and merges it once the required status passes. A refused merge is retried on the PullRequest work queue's rate limiter, which waits 5 minutes between retries by default.

The suite runs the controller with every requeue interval set to an hour, longer than any scenario, so nothing but the server's push webhooks and the controllers' own watches can carry a promotion through. Every repository the suite creates is subscribed to the deployed controller's webhook receiver, and the specs check the server's delivery log: each hydration push, each merge and the closed pull request's event must have reached the receiver and been answered with 204. The one exception is the closed pull request scenario: closing a pull request on the provider pushes nothing, and the receiver does not act on pull request events yet, so the controller only notices on a requeue, and that scenario runs with a 5-second requeue. The rate limiter's slow delay is 5 seconds throughout.

Each scenario runs its providers concurrently, since every provider has its own repositories and promotion resources, so a scenario takes as long as its slowest provider.

## Adding a provider

When you add an SCM provider (see [Adding an SCM Provider](adding-an-scm-provider.md)):

- teach the server its API: a `test/scmserver/<provider>.go` with routes registered in `Server.providerHandler`;
- add its push and pull request webhook formats to `webhook.go`;
- add it to `AllProviders` and to the `Entry` lists of the contract, webhook and every e2e table;
- give any provider-specific behavior a spec in `quirks_test.go`.
