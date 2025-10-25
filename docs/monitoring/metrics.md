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

## application_watch_events_handled

A counter for the number of times the ArgoCD application watch event handler is called. This metric increments each time the controller processes an Argo CD application event.

No labels.
