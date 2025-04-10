# Metrics

## git_operations_total

A counter of git clone operations.

Labels:

* `url`: The URL of the git repository.
* `operation`: The type of git operation (clone, fetch, pull, push).
* `status`: The status of the operation (success, failure).

## git_operations_duration_seconds

A histogram of the duration of git clone operations.

Labels:

* `url`: The URL of the git repository.
* `operation`: The type of git operation (clone, fetch, pull, push).
* `status`: The status of the operation (success, failure).

## scm_calls_total

A counter of SCM API calls.

Labels:
* `hostname`: The hostname of the SCM instance.
* `operation`: The type of SCM operation (create, update, delete).
* `status`: The status of the operation (success, failure).
