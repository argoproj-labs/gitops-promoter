GitOps Promoter produces structured logs. All log messages emitted by controllers use the default controller-runtime log 
fields. Non-controller components (such as the webhook handler) use the same log format, but will not include 
controller-specific fields.

## SCM API call logs

For each SCM REST API request that GitOps Promoter records for metrics (the same calls that increment `scm_calls_total` in the [metrics reference](metrics.md)), the controller emits a structured log line with the message **`SCM API call`**. These lines are emitted at **verbosity level 2** (`V(2)` in code), so they are visible at the default level: each line pairs with a metrics increment and is the canonical record of the controller's SCM traffic.

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

**Scope:** only requests that go through the shared metrics hook are logged here. Other SCM traffic (for example GitHub App **installation listing** during client setup) is not included. Provider-specific messages such as `github rate limit`, `GitLab rate limits`, per-request response statuses, and `ls-remote called` are emitted at verbosity level 6, so `--zap-log-level=6` surfaces the full per-request SCM and git traffic picture beyond the summary `SCM API call` lines.

## Log Verbosity

The controller uses [controller-runtime's zap logger](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/log/zap), 
which supports configurable log verbosity via the `--zap-log-level` flag.

Log statements follow the [Kubernetes community logging conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md): higher verbosity levels carry progressively more detail, and each statement is assigned the level matching its usefulness for debugging.

**The default verbosity level is `2`**, matching the default used by Kubernetes components such as the kubelet. At the default level you see errors, warnings, notable one-off events, and significant state changes (promotions, pull requests created/merged, gates transitioning). For debugging, raise the level to `4` (controller decision logic), `5` (git and template plumbing), or `6` (per-request SCM/HTTP traffic).

### Increasing the log level in Kubernetes

To increase the log level, edit the controller's `Deployment` and add `--zap-log-level=<n>` to the container's `args`:

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

To make the controller quieter than the default, set `--zap-log-level=1` (one-off events and warnings only) or `--zap-log-level=0` / `info` (warnings and errors only).

### Log level conventions

Levels follow the Kubernetes convention. Each level includes everything below it.

| Level | What lands here |
|-------|-----------------|
| `0` (`info`) | Always visible: actionable warnings and anomalies — missing or deleted secrets, saturation (full enqueue channels, webhook retry capacity exhausted), programmer errors, controller shutdown triggers. |
| `1` | Notable one-off events: server and manager lifecycle, repository clones, GitHub App installation listing, status-apply fallback recoveries, unexpected-but-recovered SCM states (for example a 404 on a check run update). |
| `2` (**default**) | Significant state changes and side effects: pull requests created/updated/merged/closed, promotions and branch merges, merge conflicts detected and resolved, gates transitioning to success, commit statuses pushed for a phase change, orphaned and legacy resource cleanup, finalizer holds that block deletion, and the [`SCM API call`](#scm-api-call-logs) telemetry line for each SCM REST request. |
| `3` | Extended reconcile flow: reconcile start/end (with duration), per-environment processing results, cross-resource reconcile triggers and enqueues, finalizer removal steps, promotion history notes written. |
| `4` | Debug — the logic behind decisions: gate evaluations (for example `Proposed commit status is not success`, DAG gate results), promotion-needed checks, finalizer wait reasons, requeue and rate-limit decisions, best-effort fallback failures. |
| `5` | Trace — plumbing detail: git command internals (fetches, notes, trailers, cat-file), expression evaluation results, rendered templates, HTTP client auth setup, provider-level operation logs, routine skip reasons. |
| `6` | Wire — per-request traffic: SCM HTTP response statuses, rate-limit headers, `ls-remote` calls, webhook/metrics/dashboard HTTP access logs, per-message stream filtering. |

Any positive integer can be used as a log level; higher values produce more output.

### Guidance for adding log statements

When adding a new log statement, pick the level by asking who needs it and how often it fires:

- An operator watching a healthy system should see it → `2` (state change) or `0`–`1` (warning / one-off).
- Someone debugging "why is my change not promoting" needs it → `3` (what the controller did) or `4` (why it decided that).
- Someone tracing a specific git or SCM interaction needs it → `5`, or `6` if it fires once per HTTP request.

A line that fires on every reconcile in a healthy steady state should never be below level `3`; a line that fires once per external request should be `6`.
