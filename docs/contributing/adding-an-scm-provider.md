# Adding an SCM provider

GitOps Promoter talks to source control systems through small Go packages under `internal/scms/`. Each integration typically implements:

- `scms.CommitStatusProvider` (`internal/scms/commitstatus.go`) — create or update commit statuses / checks for promotion gates.
- `scms.PullRequestProvider` (`internal/scms/pullrequest.go`) — open, update, merge, close, and list pull requests for change transfer.

You also wire the provider into the controller layer (constructor selection from `ScmProvider` / `ClusterScmProvider` spec, RBAC, and tests). Follow existing providers (for example `internal/scms/github/`) as a template for structure and error handling.

## Record every SCM API call with `metrics.RecordSCMCall`

Any code path that performs an **SCM HTTP API** request that should show up in operator metrics and optional debug logs **must** call `metrics.RecordSCMCall` in `internal/metrics/metrics.go` once per logical request, after the call completes (success or failure), with the HTTP status code and elapsed time. That keeps [`scm_calls_total`](../monitoring/metrics.md#scm_calls_total) / [`scm_calls_duration_seconds`](../monitoring/metrics.md#scm_calls_duration_seconds) accurate and emits the structured **`SCM API call`** debug line described in [SCM API call logs](../monitoring/logs.md#scm-api-call-logs).

Do **not** skip this for “small” calls: if it hits the provider’s REST API and counts toward rate limits, record it.

- Use the `context.Context` passed into your provider method so logging inherits reconcile fields where applicable.
- Pass the resolved [`GitRepository`](../crd-specs.md) object (same as other providers: load via `utils.GetGitRepositoryFromObjectKey` in `internal/utils/utils.go` or equivalent).
- Set `api` to `metrics.SCMAPICommitStatus` or `metrics.SCMAPIPullRequest`, and `operation` to the closest `metrics.SCMOperation` value in `internal/metrics/metrics.go` (`create`, `update`, `merge`, `close`, `list`, `get`).
- If the client returns no response on error, map to a sensible status code (existing providers often use `500`) so the metric still has a code label.
- **GitHub only:** you can pass non-nil `rateLimit` built from the GitHub client’s rate object (see `internal/scms/github/utils.go`); other providers usually pass `nil`.

### Example: call the SCM API, record metrics, then handle errors

Structure each operation like this: start a timer, invoke the provider SDK, choose an HTTP status code for the metric (from the response or a safe default on failure), call **`RecordSCMCall` once** for that round trip, then return or wrap **`err`**.

**Pattern when the SDK returns a response with `StatusCode`** (most common: record the real code when you have it; still handle `err` after):

```go
start := time.Now()
pullRequest, resp, err := giteaClient.CreatePullRequest(owner, repo, opts)
if resp != nil {
    metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate,
        resp.StatusCode, time.Since(start), nil)
}
if err != nil {
    return "", err
}
// use pullRequest…
```

See `internal/scms/gitea/pullrequest.go` (`Create`) for this shape. If `err != nil` and `resp == nil`, add a fallback `RecordSCMCall` with a synthetic status code so failures still appear in metrics (several providers use `500` or parse errors as in Bitbucket Cloud’s `parseErrorStatusCode`).

**Pattern when the SDK does not expose an HTTP response** (single `RecordSCMCall`, same code path for success and failure):

```go
start := time.Now()
createdPR, err := gitClient.CreatePullRequest(ctx, createArgs)

statusCode := 201 // success status for this RPC; pick what matches the API
if err != nil {
    statusCode = 500 // or map from err / provider-specific helpers
}
metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate,
    statusCode, time.Since(start), nil)
if err != nil {
    return "", fmt.Errorf("failed to create pull request: %w", err)
}
// use createdPR…
```

This matches `internal/scms/azuredevops/pullrequest.go` (`Create`) and `internal/scms/bitbucket_cloud/commit_status.go` (`Set`).

Pick `metrics.SCMAPICommitStatus` / `SCMAPIPullRequest` and the right `metrics.SCMOperation` for each method. For GitHub, pass `getRateLimitMetrics(response.Rate)` as the last argument instead of `nil` where the client exposes rate metadata.

## User-facing metrics and docs

If you introduce **new** Prometheus metrics (unusual for a new provider), register them in `internal/metrics/metrics.go` and document them in [Metrics](../monitoring/metrics.md). For `RecordSCMCall`, the existing `scm_calls_*` metrics already cover new providers.
