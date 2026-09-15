GitOps Promoter produces structured logs. All log messages emitted by controllers use the default controller-runtime log 
fields. Non-controller components (such as the webhook handler) use the same log format, but will not include 
controller-specific fields.

## SCM API call logs

For each SCM REST API request that GitOps Promoter records for metrics (the same calls that increment `scm_calls_total` in the [metrics reference](metrics.md)), the controller emits a structured log line with the message **`SCM API call`**. These lines are emitted at **verbosity level 1** (`V(1)` in code), not at the default `info` level.

**How to enable:** set `--zap-log-level` to **`1`** or **`debug`** (equivalent to level `1`). Higher values such as `5` also include these lines. See [Log verbosity](#log-verbosity) for deployment examples; use `--zap-log-level=1` instead of `5` if you only want SCM call lines without the rest of the controller’s most verbose output.

**Fields** (all keys are stable for filtering and parsing):

| Field | Description |
|-------|-------------|
| `git_repository` | Name of the `GitRepository` resource associated with the call. |
| `git_repository_namespace` | Namespace of that `GitRepository`. |
| `scm_provider` | Name from `GitRepository.spec.scmProviderRef.name` (same as metric labels). |
| `scm_provider_kind` | Kind from `GitRepository.spec.scmProviderRef.kind`: `ScmProvider` or `ClusterScmProvider` (defaults to `ScmProvider` when unset). |
| `api` | `CommitStatus` or `PullRequest`, matching the SCM integration surface. |
| `operation` | Operation type, for example `create`, `update`, `merge`, `close`, `list`, or `get`, depending on the call. |
| `response_code` | HTTP status code returned for that request (or a sentinel such as `500` when the client maps errors to a synthetic code). |
| `duration_seconds` | Time spent on the request, in seconds. |

**Scope:** only requests that go through the shared metrics hook are logged here. Other SCM traffic (for example GitHub App **installation listing** during client setup) is not included. Provider-specific messages such as `github rate limit` and `GitLab rate limits` are also emitted at verbosity level 1, so `--zap-log-level=1` surfaces both the API call lines and rate-limit telemetry.

## Log Verbosity

The controller uses [controller-runtime's zap logger](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/log/zap), 
which supports configurable log verbosity via the `--zap-log-level` flag.

The default log level is `info`. For debugging, it is common to increase the log level to `5`, which enables verbose 
debug logging throughout the controller.

### Increasing the log level in Kubernetes

To increase the log level, edit the controller's `Deployment` and add `--zap-log-level=5` to the container's `args`:

```yaml
containers:
  - command:
      - /usr/bin/tini
      - '--'
      - /gitops-promoter
      - controller
    args:
      - --leader-elect
      - --zap-log-level=5
```

You can patch an existing deployment with:

```bash
kubectl patch deployment controller-manager -n gitops-promoter \
  --type='json' \
  -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--zap-log-level=5"}]'
```

### Log level values

The `--zap-log-level` flag accepts the following values:

| Value | Description |
|-------|-------------|
| `info` | Default level. Logs errors plus significant state changes and side effects. |
| `debug` | Logs additional debug messages. Equivalent to level `1`. |
| `5` | Highly verbose output useful for diagnosing bugs. |

Any positive integer can be used as a log level; higher values produce more output. The most commonly used value for 
diagnosing bugs is `5`.

### How log levels are assigned

Log statements in the controller are leveled by how useful they are for debugging versus day-to-day operation:

| Level | Tier | What lands here |
|-------|------|-----------------|
| `info` (0) | Operational | State changes and side effects an operator should see by default: pull requests created/merged/closed, promotions and branch merges, merge conflicts detected, gates transitioning to success, orphaned resource cleanup, repository clones, saturation warnings (full enqueue channels, webhook retry capacity), misconfiguration warnings (missing secrets), and server/manager lifecycle. |
| `1` (`debug`) | Debug | The reconcile narrative: reconcile start/end (with duration), per-environment gate evaluations and promotion decisions (for example `Proposed commit status is not success`), cross-resource reconcile triggers/enqueues, finalizer wait reasons, SCM API telemetry (`SCM API call`, rate limits, `ls-remote called`), and unusual best-effort fallback paths. |
| `4`+ | Trace | Full wire-level and plumbing detail: per-request SCM HTTP response statuses, git command plumbing (fetches, notes, trailers, cat-file), expression evaluation results, rendered templates, HTTP client auth setup, metrics scrape access logs, and routine skip reasons. |

When adding a new log statement, pick the level by asking who needs it: an operator watching a healthy system (`info`),
someone debugging "why is my change not promoting" (`V(1)`), or someone tracing a specific SCM/git interaction (`V(4)`).
