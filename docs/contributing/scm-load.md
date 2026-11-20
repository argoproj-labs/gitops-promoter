# SCM API load harness

`make test-scm-load` measures how many requests GitOps Promoter sends to GitHub: per hour in steady states, and in total for events such as a promotion. Use it to see what a change does to API load before it ships, or to size a GitHub App installation's rate limit against a number of PromotionStrategies.

The harness runs the controllers in an envtest control plane against the GitHub listener of the [test SCM server](test-scm-server.md), and counts requests in the server's request log. Nothing is read from the promoter's own metrics, so the numbers include what those metrics leave out, such as installation token requests and the calls a WebRequestCommitStatus makes with the ScmProvider's credentials.

> [!NOTE]
> The test SCM server counts every request the promoter sends, but it is not GitHub. It does not reproduce GitHub's accounting, such as secondary rate limits or which requests GitHub exempts from the primary one.

## Running it

```shell
make test-scm-load
```

The target starts the test SCM server if none is listening (and leaves it running), runs the specs labeled `scm-load` in `internal/controller`, and writes `report.md` and `report.json` to `bin/scm-load`. A full run takes about 45 minutes. The specs are skipped everywhere else: `make test`, `make test-parallel` and `make test-scm-provider` never run them.

| Variable | Default | Purpose |
|----------|---------|---------|
| `SCM_LOAD_OUT` | `bin/scm-load` | Directory the reports are written to |
| `SCM_LOAD_FOCUS` | all | Regular expression; only scenarios whose name matches it run, for example `'^steady/held'` |
| `SCM_LOAD_COMPRESSION` | `10` | How many times faster than shipped the periodic timers run in steady states |
| `SCM_LOAD_WINDOW` | `5m` | How long steady states are observed |
| `SCM_SERVER_PORT_BASE` | `9000` | Port of the test SCM server's admin API, as for `make run-scm-server` |

Nothing else may use the test SCM server during a run: the harness changes the GitHub listener's behavior and injects faults.

## Reading the report

Every scenario has its own repository owner, GitHub App installation and ScmProvider, shared by the scenario's repositories. That is how an organization usually installs the promoter's App, and it lets the harness attribute every request in the server's log to one scenario.

The report sorts requests into buckets by who sent them and which budget they spend:

| Bucket | Requests |
|--------|----------|
| `rest.installation.read`, `rest.installation.write` | The promoter's own GitHub API calls, made with the installation token. They count against the installation's rate limit. |
| `rest.webrequest` | Calls a WebRequestCommitStatus makes with `authentication.scm`. They use the same installation token and count against the same rate limit. |
| `rest.app` | Calls authenticated as the App itself: installation lookups and installation token requests. |
| `git.read`, `git.write` | Git smart HTTP requests of fetches, `ls-remote`s and pushes. |
| `git.auth-challenge` | The unauthenticated request git sends first, which the server answers with 401. |

"Git commands" counts fetches, `ls-remote`s and pushes. Each is several HTTP requests, which "Git HTTP" counts.

The report has these groups of scenarios:

- **Steady states** are reported per hour at the shipped intervals. "Stable: no" means the promoter wrote to the SCM or a request failed during the observation, so the state was not steady and its rates should not be trusted.
- **Scale** repeats the idle and held states with more PromotionStrategies and more environments.
- **Events** are reported as totals, from the trigger until the promoter has sent nothing for 10 seconds. They run at the shipped intervals.
- **Throttled** shows what the promoter keeps sending while GitHub refuses its requests, at real speed.

**Consistency checks** are the harness checking itself. A rate measured at two compression factors must agree, and so must per-strategy and per-environment rates at different scales. A failed check means the extrapolation does not hold for that scenario.

## How steady states are measured

With the shipped ControllerConfiguration every controller requeues after 5 minutes, so watching an idle promoter for a few minutes would show almost nothing. The harness instead sets every `requeueDuration`, and the polling interval of the WebRequestCommitStatuses it creates, to a fraction of the shipped value: 30 seconds and 6 seconds at the default compression of 10. It then observes for `SCM_LOAD_WINDOW` and divides the observed rates by the compression factor.

Because every periodic timer is compressed by the same factor, the result does not depend on which timer causes a request. Two things do not follow those timers, and the report counts them without extrapolating:

- An installation token is requested about once an hour per installation.
- The installation of the App on a repository owner is looked up once every 30 minutes, unless `spec.github.installationID` is set on the ScmProvider.

The requests of a steady state come in bursts, one per requeue. An observation that began or ended inside a burst would count part of a cycle as a whole one, so requests in the first and last requeue interval of the observation are weighted down linearly. Every phase of the cycle then gets the same total weight, and a periodic stream is measured exactly wherever the observation starts. The "Count" columns of the report are plain counts; the rates use the weights.

## Scenarios

The scenarios are defined in `internal/controller/scmload_test.go`:

- `loadSteadyScenarios`: nothing to promote, and a promotion held open, once per kind of gate.
- `loadEventScenarios`: onboarding a repository, a promotion with each kind of gate, a new change while pull requests are open, gates and Argo CD Applications changing state, pull requests merged or closed by a person, a PromotionStrategy deleted, and a conflict.
- `loadThrottleScenarios`: primary and secondary rate limits, and server errors.

To add one, add an entry to the matching function. A steady scenario's `prepare` returns a fixture that is already in the state to observe. An event's `prepare` returns a fixture at rest, and its `trigger` starts the event and returns once its outcome is reached. `internal/controller/scmload_fixture_test.go` and `scmload_gates_test.go` hold the fixture: its options select the number of strategies and environments and the gates, and it seeds and hydrates repositories through the test SCM server's admin API, so that the fixture's own git traffic never reaches the provider listener.

`test/scmserver/loadreport` turns the request log into the report. It has no test logic, so it can analyze a request log captured any other way, such as from the e2e suite's test SCM server.

## Limitations

- A controller restart is not measured. The controllers run inside the test process, whose GitHub clients and git clones cannot be reset. On start every CommitStatus is reconciled once, which is one check run write each.
- Only `event/promotion/hydrator-notes` has the git notes Argo CD's hydrator writes. The other scenarios run without them.
- Error retries are not compressed: the workqueue rate limiter is read once when a controller starts. The throttled scenarios therefore run at real speed and cover the first minutes only.
- The numbers are for GitHub. The other providers make different calls.
