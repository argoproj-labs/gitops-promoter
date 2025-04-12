# Metrics

GitOps Promoter produces metrics counter and histogram metrics for all network-bound operations.

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
