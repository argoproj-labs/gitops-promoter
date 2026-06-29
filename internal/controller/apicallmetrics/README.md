# API call metrics tests

Dedicated Ginkgo/envtest suite that exercises promotion flows and **collects git CLI and SCM API call counts** from the controller via Prometheus metrics. Specs are **collect-and-report only** (no golden assertions yet).

This package is separate from `internal/controller/suite_test.go` so it can use a long `ControllerConfiguration.requeueDuration` and drive reconciles through webhooks instead of periodic timer requeues.

## What is measured

Git subprocesses recorded on `git_operations_total` for the scenario `GitRepository`:

- All commands through `internal/git.runCmdWithEnv` (clone, fetch, push, rev-parse, merge-tree, etc.)
- `interpret-trailers` from `AddTrailerToCommitMessage` and `ParseTrailersFromMessage`

Counts are read from the controller-runtime Prometheus registry and reported as **deltas** since a pre-scenario snapshot (counters are not reset between specs).

### SCM API (`scm_calls_total`)

Logical SCM operations recorded by the **fake** provider through `metrics.RecordSCMCall` (same metric as production providers):

| API | Operations |
|-----|------------|
| `CommitStatus` | `set` |
| `PullRequest` | `create`, `update`, `merge`, `close`, `list` |

Breakdown keys in reports use `api/operation` (for example `PullRequest/create`). Counts sum across `response_code` labels.

No-gates scenarios (`single_env_no_gates`, `three_env_no_gates`) may report `scm.total: 0` if promotion completes without pushing commit statuses or opening SCM pull requests within the wait window. The flagship `full_gate_stack_three_env` scenario always exercises `CommitStatus/set` (and typically `PullRequest/*` during merges).

## What is not measured

- Git run by test bootstrap (`suite_test.runGitCmd`, `makeChangeAndHydrateRepo`)
- Git inside the fake SCM provider (PR merge clone/push path)
- Outbound HTTP from `WebRequestCommitStatus` (separate `webrequest_commit_status_http_requests_total` metric)

## Scenarios

Specs run **serially** (`Serial`) because they share one in-process gitkit server.

| Scenario | Spec | Environments | Gates | Purpose |
|----------|------|--------------|-------|---------|
| `single_env_no_gates` | `collects git CLI metrics for single_env_no_gates` | dev only | none | Minimal CTP baseline |
| `three_env_no_gates` | `collects git CLI metrics for three_env_no_gates` | dev → staging → prod | none | Multi-env promotion without gates |
| `full_gate_stack_three_env` | `collects git CLI metrics for full_gate_stack_three_env` | dev → staging → prod | all types | Flagship integration (full promotion) |
| `full_gate_three_env_open_prs_soak` | `collects git CLI metrics for full_gate_three_env_open_prs_soak` | dev → staging → prod | all types | Full gate stack; **3 open PRs**; fixed soak (default **5m**) with **15s** requeue |
| `full_gate_three_env_closed_prs_soak` | `collects git CLI metrics for full_gate_three_env_closed_prs_soak` | dev → staging → prod | all types | Full gate stack; **no PRs** (no promotion push); fixed soak (default **5m**) with **15s** requeue |
| `full_gate_three_env_open_prs_soak_60m_requeue` | `collects git CLI metrics for full_gate_three_env_open_prs_soak_60m_requeue` | dev → staging → prod | all types | Same as open PR soak but **60m** requeue (event-driven during soak; matches suite default) |
| `full_gate_three_env_closed_prs_soak_60m_requeue` | `collects git CLI metrics for full_gate_three_env_closed_prs_soak_60m_requeue` | dev → staging → prod | all types | Same as closed PR soak but **60m** requeue |

### `full_gate_stack_three_env` (flagship)

Wires every commit-status gate type on three environments in one run:

**Active gates (all environments)**

| Key | Resource | Notes |
|-----|----------|-------|
| `argocd-health` | `ArgoCDCommitStatus` | 3 Argo CD `Application` objects; test patches Synced+Healthy |
| `timer` | `TimedCommitStatus` | Duration configurable (default **5m** per env) |

**Proposed gates (per environment)**

| Branch | GitCommitStatus | WebRequestCommitStatus |
|--------|-----------------|------------------------|
| `environment/development` | `dev-gcs` | `wrcs-dev` |
| `environment/staging` | `staging-gcs` | `wrcs-staging` |
| `environment/production` | `prod-gcs` | `wrcs-prod` |

**Flow (with phase snapshots)**

1. Bootstrap git repo (unmeasured), snapshot `gitBefore`
2. Create PromotionStrategy + gate CRs + Argo apps + httptest approval servers
3. `makeChangeAndHydrateRepo` + `sendWebhookForPush` per hydrated branch
4. Wait for proposed gates (GCS + WRCS) → snapshot `after_proposed_gates`
5. Per env (dev → staging → prod): wait timed gate → simulate Argo health → snapshot `after_dev` / `after_staging` / `after_prod`
6. Emit final report

Phase snapshots store **cumulative deltas since `gitBefore`**, not increments between phases.

## Running

### Makefile (recommended)

**Parallel (default)** — one isolated ginkgo process per scenario; wall clock ≈ slowest scenario:

```bash
make test-api-call-metrics
```

Each subprocess gets its own git port (`5101`–`5107`) and webhook port (`3401`–`3407`) via `PROMOTER_API_METRICS_GIT_PORT` / `PROMOTER_API_METRICS_WEBHOOK_PORT` (read by both the apicallmetrics gitkit server and `internal/scms/fake` clone URLs). Ginkgo `--focus` uses a `$` suffix so `open_prs_soak` does not also run `open_prs_soak_60m_requeue`.

Per-scenario logs: `/tmp/api-metrics-test.d/<scenario>.log`. Combined log: `/tmp/api-metrics-test.log`.

**Serial** — one process, all specs in order (lower CPU/RAM):

```bash
make test-api-call-metrics-serial
```

Both print the same combined summary on stdout (suite result, git/scm/network totals, report paths). JSON reports default to `$TMPDIR/promoter-api-metrics-<scenario>.json` unless `PROMOTER_API_METRICS_REPORT` is set.

### Direct ginkgo

```bash
KUBEBUILDER_ASSETS="$(./bin/setup-envtest-release-0.24 use 1.31.0 --bin-dir "$PWD/bin" -p path)" \
  PROMOTER_API_METRICS_REQUEUE=60m \
  PROMOTER_API_METRICS_GINKGO_TIMEOUT=30m \
  go tool ginkgo -v --timeout 30m ./internal/controller/apicallmetrics/ \
  > /tmp/api-metrics-test.log 2>&1
```

### Focus one scenario

```bash
KUBEBUILDER_ASSETS="$(./bin/setup-envtest-release-0.24 use 1.31.0 --bin-dir "$PWD/bin" -p path)" \
  go tool ginkgo -v --timeout 30m --focus=full_gate_stack_three_env ./internal/controller/apicallmetrics/
```

### Fast local iteration

Use a short timed-gate duration so the flagship spec finishes in minutes instead of ~20+:

```bash
PROMOTER_API_METRICS_TCS_DURATION=1s \
PROMOTER_API_METRICS_GINKGO_TIMEOUT=10m \
  make test-api-call-metrics
```

First run on a machine needs envtest binaries: `make setup-envtest`.

## Configuration

All variables are optional. Values use Go `time.ParseDuration` syntax (`5m`, `1s`, `90s`, …).

### Suite isolation

| Variable | Default | Description |
|----------|---------|-------------|
| `PROMOTER_API_METRICS_REQUEUE` | `60m` | `ControllerConfiguration.spec.*.workQueue.requeueDuration` for every controller. Suppresses periodic background reconciles during the test window; progress is event-driven (webhooks, watches, gate CR updates). |

### Timed commit status gates (`full_gate_stack_three_env`)

| Variable | Default | Description |
|----------|---------|-------------|
| `PROMOTER_API_METRICS_TCS_DURATION` | `5m` | Timed gate duration for **all** environments |
| `PROMOTER_API_METRICS_TCS_DURATION_DEV` | inherits global | Override for `environment/development` |
| `PROMOTER_API_METRICS_TCS_DURATION_STAGING` | inherits global | Override for `environment/staging` |
| `PROMOTER_API_METRICS_TCS_DURATION_PROD` | inherits global | Override for `environment/production` |

While a timed gate is pending, the TimedCommitStatus controller requeues every **1 minute** (hardcoded in production code). The suite auto-computes wait timeouts to account for TCS duration + that requeue interval + ACS health threshold.

### Argo CD app health simulation (`full_gate_stack_three_env`)

| Variable | Default | Description |
|----------|---------|-------------|
| `PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY` | `0` | Sleep before patching each env's Argo `Application` to Synced+Healthy |
| `PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY_DEV` | inherits global | Per-env override |
| `PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY_STAGING` | inherits global | Per-env override |
| `PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY_PROD` | inherits global | Per-env override |

ArgoCDCommitStatus also enforces a fixed **5s** post-`LastTransitionTime` threshold in the controller (not configurable here).

### PR soak scenarios (`full_gate_three_env_*_prs_soak`)

These specs use the **full gate stack** but do **not** complete promotion. They establish a PR posture across all three environments, then sleep for a fixed soak window.

**15s requeue variants** (`*_open_prs_soak`, `*_closed_prs_soak`) patch `ControllerConfiguration` to **15s** requeue for that spec only (restored afterward) so controllers poll aggressively during soak.

**60m requeue variants** (`*_60m_requeue`) keep the suite default **60m** requeue — no CC patch — so soak traffic is mostly event-driven (webhooks, watches, gate CR updates) rather than periodic timer requeues.

| Variable | Default | Description |
|----------|---------|-------------|
| `PROMOTER_API_METRICS_SOAK_DURATION` | `5m` | Wall-clock soak after PR posture is ready |
| `PROMOTER_API_METRICS_SOAK_REQUEUE` | `15s` | Per-spec `requeueDuration` for **15s requeue soak specs only** |
| `PROMOTER_API_METRICS_SOAK_TCS_DURATION` | `30m` | Timed gate duration during soak (longer than soak so timers stay pending) |
| `PROMOTER_API_METRICS_SOAK_TCS_DURATION_DEV` | inherits global | Per-env override |
| `PROMOTER_API_METRICS_SOAK_TCS_DURATION_STAGING` | inherits global | Per-env override |
| `PROMOTER_API_METRICS_SOAK_TCS_DURATION_PROD` | inherits global | Per-env override |

**Open PRs:** `autoMerge: false`, trigger a dry→hydrated promotion, wait until each CTP has an open SCM PR; active gates stay pending (Argo apps never patched healthy; timed gate longer than soak).

**No PRs (closed soak):** same full gate stack but **no** `makeChangeAndHydrateRepo` push — CTP proposed/active dry SHAs stay in sync so PullRequest CRs are never created and the SCM never sees open/close PR traffic during soak.

Quick local run:

```bash
PROMOTER_API_METRICS_SOAK_DURATION=30s \
PROMOTER_API_METRICS_SOAK_REQUEUE=15s \
  go tool ginkgo -v --timeout 15m --focus=full_gate_three_env_open_prs_soak ./internal/controller/apicallmetrics/
```

### Timeouts

| Variable | Default | Description |
|----------|---------|-------------|
| `PROMOTER_API_METRICS_WAIT_TIMEOUT` | auto (~23m with 5m TCS) | `Eventually` timeout for full-gate wait helpers |
| `PROMOTER_API_METRICS_GINKGO_TIMEOUT` | `45m` | Ginkgo `--timeout` for the whole suite (`make test-api-call-metrics` passes this through; 7 specs with two 5m soaks) |

Baseline scenarios (`single_env_no_gates`, `three_env_no_gates`) use the standard controller `EventuallyTimeout` (90s).

### Report output

| Variable | Default | Description |
|----------|---------|-------------|
| `PROMOTER_API_METRICS_REPORT` | unset | If set, write JSON report to this path. If it is a directory, writes `<scenario>_<phase>_<unix>.json` inside it. |

When unset, reports go to:

```
$TMPDIR/promoter-api-metrics-<scenario>.json
```

(`$TMPDIR` is `/tmp` on Linux; on macOS it is usually `/var/folders/.../T/`.)

## Report format

Each scenario emits:

1. **Ginkgo log** — `APICallMetrics report` with `git_cli_total`, `git_cli_breakdown`, `scm_total`, and `scm_breakdown`
2. **Ginkgo report entry** — `AddReportEntry("APICallMetrics", …)`
3. **JSON file** — machine-readable artifact

Example fields:

```json
{
  "scenario": "full_gate_stack_three_env",
  "phase": "final",
  "controller_requeue_duration": "1h0m0s",
  "reconcile_policy": "event_driven",
  "wait_timeout": "23m35s",
  "timed_gate_by_branch": {
    "environment/development": "5m0s",
    "environment/staging": "5m0s",
    "environment/production": "5m0s"
  },
  "git_repository": "metrics-full-gate-stack-three-env-…-gr",
  "git_cli": {
    "total": 1571,
    "breakdown": {
      "fetch": 88,
      "interpret-trailers": 174,
      "ls-remote": 8,
      "push": 4
    }
  },
  "scm": {
    "total": 42,
    "breakdown": {
      "CommitStatus/set": 18,
      "PullRequest/create": 2,
      "PullRequest/list": 12,
      "PullRequest/merge": 2
    }
  },
  "phases": [
    { "phase": "after_proposed_gates", "git_cli": { … }, "scm": { … } },
    { "phase": "after_dev", "git_cli": { … }, "scm": { … } }
  ]
}
```

## Package layout

| File | Role |
|------|------|
| `suite_test.go` | envtest, gitkit, webhook receiver, reconcilers, 60m ControllerConfiguration |
| `api_call_metrics_test.go` | Scenario specs |
| `metrics_scenario.go` | PromotionStrategy + gate CR builders |
| `metrics_env.go` | Env-based timing configuration |
| `git_metrics.go` | Git CLI Prometheus snapshot/delta helpers |
| `scm_metrics.go` | SCM API Prometheus snapshot/delta helpers |
| `api_call_metrics_report.go` | Report struct + JSON/log emission |
| `controllerconfiguration_test.go` | Load shipped CC + apply requeue |

## Related docs

- Git operation metrics: [`docs/monitoring/metrics.md`](../../../docs/monitoring/metrics.md)
- Main controller test guide: [`CLAUDE.md`](../../../CLAUDE.md)
