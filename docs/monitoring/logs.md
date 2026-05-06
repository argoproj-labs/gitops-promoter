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

**Scope:** only requests that go through the shared metrics hook are logged here. Other SCM traffic (for example GitHub App **installation listing** during client setup) is not included. Provider-specific messages such as `github rate limit` may still appear at `info` when enabled by that provider.

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
| `info` | Default level. Logs informational messages and errors. |
| `debug` | Logs additional debug messages. Equivalent to level `1`. |
| `5` | Highly verbose output useful for diagnosing bugs. |

Any positive integer can be used as a log level; higher values produce more output. The most commonly used value for 
diagnosing bugs is `5`.
