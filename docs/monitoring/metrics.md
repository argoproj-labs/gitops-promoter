# Metrics

GitOps Promoter produces metrics counter and histogram metrics for all network-bound operations.

!!!important "Metrics Subject to Change"

    The metrics produced by GitOps Promoter are subject to change as the project evolves until the 1.0 release. 
    Please refer to this document for the latest metrics.

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

## DORA Metrics

GitOps Promoter provides metrics that form the basis for calculating [DORA (DevOps Research and Assessment) metrics](https://dora.dev/). These metrics are tracked on a per-PromotionStrategy and per-environment basis. See [DORA Metrics Guide](dora.md) for more information on how to use these metrics.

### dora_deployments_total

A counter of deployments to an environment. This metric increments every time a change is merged to the active branch of an environment.

Labels:

* `promotion_strategy`: The name of the PromotionStrategy resource.
* `namespace`: The namespace of the PromotionStrategy resource.
* `environment`: The name of the environment (branch name).
* `is_terminal`: Whether this is the terminal (last) environment in the promotion sequence (true, false).

### lead_time_seconds

A gauge tracking the lead time for changes in seconds. This measures the time from when a DRY commit is created to when it is successfully deployed in the environment (active commit status becomes successful).

If a change does not make it to the terminal environment and become successful before a new commit arrives, the lead time tracking continues from the original commit time rather than resetting to the new commit time.

Labels:

* `promotion_strategy`: The name of the PromotionStrategy resource.
* `namespace`: The namespace of the PromotionStrategy resource.
* `environment`: The name of the environment (branch name).
* `is_terminal`: Whether this is the terminal (last) environment in the promotion sequence (true, false).

### dora_change_failure_rate_total

A counter tracking the number of failed deployments. This metric increments once per commit SHA when the active commit status enters a failed state. It only increments once per commit, even if the status fluctuates.

Labels:

* `promotion_strategy`: The name of the PromotionStrategy resource.
* `namespace`: The namespace of the PromotionStrategy resource.
* `environment`: The name of the environment (branch name).
* `is_terminal`: Whether this is the terminal (last) environment in the promotion sequence (true, false).

### mean_time_to_restore_seconds

A gauge tracking the mean time to restore in seconds. This measures the time from when an environment enters a failed state to when it returns to a healthy state.

Labels:

* `promotion_strategy`: The name of the PromotionStrategy resource.
* `namespace`: The namespace of the PromotionStrategy resource.
* `environment`: The name of the environment (branch name).
* `is_terminal`: Whether this is the terminal (last) environment in the promotion sequence (true, false).
