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

## promoter_finalizer_blocked_deletions_total

A counter for the number of resource deletions that were blocked by finalizers due to dependent resources.

When a user attempts to delete a resource (GitRepository, ScmProvider, or ClusterScmProvider) that still has dependent resources, the deletion is blocked by a finalizer. This metric counts how many times this blocking occurred.

**Use Cases:**
* Alert on high rates of blocked deletions (may indicate user confusion or automation issues)
* Identify resource types that frequently have deletion issues
* Track patterns of failed deletion attempts

Labels:

* `resource_type`: The type of resource being deleted (GitRepository, ScmProvider, ClusterScmProvider).
* `namespace`: The namespace of the resource being deleted (empty for cluster-scoped resources).

**Example Queries:**

```promql
# Rate of blocked deletions per hour
rate(promoter_finalizer_blocked_deletions_total[1h])

# Total blocked deletions by resource type
sum by (resource_type) (promoter_finalizer_blocked_deletions_total)

# Blocked deletions in specific namespace
sum by (resource_type) (
  promoter_finalizer_blocked_deletions_total{namespace="production"}
)
```

## promoter_finalizer_dependent_resources

A gauge of the current number of dependent resources preventing deletion of a resource.

This metric is set when a deletion is blocked and shows how many dependent resources are preventing the deletion. The gauge is automatically cleared (deleted) when the finalizer is successfully removed.

**Use Cases:**
* Monitor resources stuck in deletion state
* Identify deletion backlog
* Alert on resources that remain stuck for too long
* Understand the magnitude of dependency issues

Labels:

* `resource_type`: The type of resource being deleted (GitRepository, ScmProvider, ClusterScmProvider).
* `resource_name`: The name of the resource being deleted.
* `namespace`: The namespace of the resource being deleted (empty for cluster-scoped resources).

**Example Queries:**

```promql
# Resources currently blocked from deletion
promoter_finalizer_dependent_resources > 0

# Count of stuck resources by type
count by (resource_type) (promoter_finalizer_dependent_resources > 0)

# Top 10 resources with most dependents
topk(10, promoter_finalizer_dependent_resources)

# Resources stuck for more than 1 hour
promoter_finalizer_dependent_resources > 0 and 
  time() - timestamp(promoter_finalizer_dependent_resources) > 3600
```

**Example Alerts:**

```yaml
# Alert if a resource is stuck deleting for too long
- alert: ResourceStuckDeleting
  expr: promoter_finalizer_dependent_resources > 0
  for: 1h
  annotations:
    summary: "{{ $labels.resource_type }} stuck deleting for 1h+"
    description: "{{ $labels.resource_type }} {{ $labels.namespace }}/{{ $labels.resource_name }} is blocked by {{ $value }} dependent resources"

# Alert on high deletion block rate
- alert: HighFinalizerBlockedDeletions
  expr: rate(promoter_finalizer_blocked_deletions_total[1h]) > 10
  annotations:
    summary: "High rate of blocked deletions"
    description: "{{ $value }} deletion blocks per hour for {{ $labels.resource_type }}"
```
