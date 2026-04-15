# Metrics

GitOps Promoter produces metrics counter and histogram metrics for all network-bound operations.

> [!IMPORTANT]
> The metrics produced by GitOps Promoter are subject to change as the project evolves until the 1.0 release. 
> Please refer to this document for the latest metrics.

## git_operations_total

A counter of git clone operations.

Labels:

* `git_repository`: The name of the GitRepository resource associated with the operation.
* `scm_provider`: The name of the ScmProvider resource associated with the operation.
* `operation`: The type of git operation (clone, fetch, pull, push, ls-remote).
* `result`: Whether the operation succeeded (success, failure).

## git_operations_duration_seconds

A histogram of the duration of git clone operations.

Labels:

* `git_repository`: The name of the GitRepository resource associated with the operation.
* `scm_provider`: The name of the ScmProvider resource associated with the operation.
* `operation`: The type of git operation (clone, fetch, pull, push, ls-remote).
* `result`: Whether the operation succeeded (success, failure).

## scm_calls_total

A counter of SCM API calls.

Labels:

* `git_repository`: The name of the GitRepository resource associated with the operation.
* `scm_provider`: The name of the ScmProvider resource associated with the operation.
* `api`: The SCM API being called (CommitStatus, PullRequest)
* `operation`: The type of SCM operation.
  * For CommitStatus, this is always create.
  * For PullRequest, this is create, update, merge, close, or list.
* `response_code`: The HTTP response code.

## scm_calls_duration_seconds

A histogram of the duration of SCM API calls.

Labels:

* `git_repository`: The name of the GitRepository resource associated with the operation.
* `api`: The SCM API being called (CommitStatus, PullRequest)
* `operation`: The type of SCM operation.
  * For CommitStatus, this is always create.
  * For PullRequest, this is create, update, merge, close, or list.
* `response_code`: The HTTP response code.

## webrequest_commit_status_http_requests_total

A counter of outbound HTTP requests from `WebRequestCommitStatus` reconciliation: one increment each time `http.Client.Do` runs for the resource (including when `Do` returns an error or a nil response). Failures before `Do` (template render, auth, and so on) are not counted.

Labels:

* `namespace`: Namespace of the `WebRequestCommitStatus` resource.
* `name`: Name of the `WebRequestCommitStatus` resource.
* `response_code`: The HTTP status code from the response when a status line was received. The value **`0`** (label string `"0"`) means `Do` ran but no HTTP status was available—for example a transport error before headers, or a nil response.

Each distinct combination of labels is its own Prometheus time series. Clusters with very many `WebRequestCommitStatus` objects or many different status codes should expect proportionally more series.

## webrequest_commit_status_http_request_duration_seconds

A histogram of elapsed time from `http.Client.Do` through reading the response body (`io.ReadAll` on the response) for the same attempts counted by `webrequest_commit_status_http_requests_total`. If `Do` returns an error, the observation covers only the `Do` call; if a response is received, it includes reading the full body. No observation is recorded when reconciliation fails before `Do`.

Labels:

* `namespace`: Namespace of the `WebRequestCommitStatus` resource.
* `name`: Name of the `WebRequestCommitStatus` resource.
* `response_code`: Same semantics as for `webrequest_commit_status_http_requests_total`.

## scm_calls_rate_limit_limit

A counter for the rate limit of SCM API calls.

This metric is currently only produced for GitHub.

Labels:

* `scm_provider`: The name of the ScmProvider resource associated with the operation.

## scm_calls_rate_limit_remaining

A counter for the remaining rate limit of SCM API calls.

This metric is currently only produced for GitHub.

Labels:

* `scm_provider`: The name of the ScmProvider resource associated with the operation.

## scm_calls_rate_limit_reset_remaining_seconds

A gauge for the remaining seconds until the SCM API rate limit resets.

This metric is currently only produced for GitHub.

Labels:

* `scm_provider`: The name of the ScmProvider resource associated with the operation.

## webhook_processing_duration_seconds

A histogram of the duration of webhook processing.

"Processing" refers to the time taken to handle a webhook request, including any logic to find a ChangeTransferPolicy.
It excludes the time taken to call the k8s API to set the reconcile annotation on the ChangeTransferPolicy, since that
isn't in the webhook server's control.

Labels:

* `ctp_found`: Whether a ChangeTransferPolicy was found for the webhook (true, false). May be false for error conditions, so check the response code.
* `response_code`: The HTTP response code of the webhook processing. 204 is the success code, which may be returned even if no ChangeTransferPolicy was found.

## promoter_finalizer_dependent_resources

A gauge of the current number of dependent resources preventing deletion of a resource.

This metric is set when a deletion is blocked and shows how many dependent resources are preventing the deletion. The gauge is automatically cleared (deleted) when the finalizer is successfully removed.

Labels:

* `resource_type`: The type of resource being deleted (GitRepository, ScmProvider, ClusterScmProvider).
* `resource_name`: The name of the resource being deleted.
* `namespace`: The namespace of the resource being deleted (empty for cluster-scoped resources).

## application_watch_events_handled_total

A counter for the number of times the ArgoCD application watch event handler is called. This metric increments each time the controller processes an Argo CD application event.

No labels.

## promoter_kubernetes_resources

A gauge of how many `promoter.argoproj.io` custom resources currently exist in the **local** Kubernetes cluster, broken out by API kind (for example `PromotionStrategy`, `GitRepository`).

The controller refreshes this metric on a fixed interval (30 seconds) by listing each kind via the manager client (same view as the controller cache). It does **not** count resources on remote clusters that are reconciled only through multicluster configuration.

If a list request fails for a kind, that kind's gauge is set to `0` and an error is logged.

Labels:

* `kind`: Kubernetes API kind of the custom resource (matches the thirteen root CRDs reconciled by GitOps Promoter, such as `ArgoCDCommitStatus`, `ChangeTransferPolicy`, `ClusterScmProvider`, `CommitStatus`, `ControllerConfiguration`, `GitCommitStatus`, `GitRepository`, `PromotionStrategy`, `PullRequest`, `RevertCommit`, `ScmProvider`, `TimedCommitStatus`, `WebRequestCommitStatus`).
