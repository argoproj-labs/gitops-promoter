# WebRequestCommitStatus Simulator

The `webrequestsimulator` package lets you run a single `WebRequestCommitStatus` reconcile in a Go test without a live cluster or real HTTP server. You supply mock HTTP responses; the simulator returns the rendered HTTP request, the `CommitStatus` resources the controller would have upserted, and the `Status` the controller would have written.

**Primary use cases:**

- Validate that `trigger.when.expression`, `success.when.expression`, and `success.when.output.expression` behave as expected before deploying
- Verify that URL, header, body, and description templates render correctly
- Write regression tests for complex WRCS configurations

## Quick start

```go
import (
    "context"

    promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
    "github.com/argoproj-labs/gitops-promoter/webrequestsimulator"
    "github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMyWRCS(t *testing.T) {
    ctx := context.Background()

    wrcs := &promoterv1alpha1.WebRequestCommitStatus{
        ObjectMeta: metav1.ObjectMeta{Name: "my-gate", Namespace: "default"},
        Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
            PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "my-ps"},
            Key:                  "my-gate",
            ReportOn:             "proposed",
            HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
                URLTemplate: "https://api.example.com/validate/{{ .Branch }}",
                Method:      "GET",
            },
            Success: promoterv1alpha1.SuccessSpec{
                When: promoterv1alpha1.WhenWithOutputSpec{
                    Expression: "Response.StatusCode == 200",
                },
            },
            Mode: promoterv1alpha1.ModeSpec{
                Polling: &promoterv1alpha1.PollingModeSpec{
                    Interval: metav1.Duration{Duration: 0},
                },
            },
        },
    }

    ps := &promoterv1alpha1.PromotionStrategy{
        ObjectMeta: metav1.ObjectMeta{Name: "my-ps", Namespace: "default"},
        Spec: promoterv1alpha1.PromotionStrategySpec{
            RepositoryReference: promoterv1alpha1.ObjectReference{Name: "repo"},
            Environments: []promoterv1alpha1.Environment{
                {Branch: "dev",  ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "my-gate"}}},
                {Branch: "prod", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "my-gate"}}},
            },
        },
        Status: promoterv1alpha1.PromotionStrategyStatus{
            Environments: []promoterv1alpha1.EnvironmentStatus{
                {Branch: "dev",  Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "aaa..."}}},
                {Branch: "prod", Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "bbb..."}}},
            },
        },
    }

    result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
        WebRequestCommitStatus: wrcs,
        PromotionStrategy:      ps,
        HTTPResponses: []simulatortypes.HTTPResponse{
            {Branch: "dev",  Response: simulatortypes.Response{StatusCode: 200}},
            {Branch: "prod", Response: simulatortypes.Response{StatusCode: 200}},
        },
    })
    if err != nil {
        t.Fatal(err)
    }

    // result.Status.Environments — per-branch Phase, TriggerOutput, ResponseOutput, SuccessOutput
    // result.RenderedRequests    — the HTTP request(s) the controller would have sent
    // result.CommitStatuses      — the CommitStatus CRs the controller would have upserted
}
```

## The two contexts

`WebRequestCommitStatus` has two modes controlled by `spec.mode.context`. The simulator mirrors each exactly.

### `environments` (default)

One HTTP request fires per applicable environment branch. `HTTPResponses` must contain one entry per branch whose trigger fires; `Branch` on each entry selects which environment it applies to.

```go
result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
    WebRequestCommitStatus: wrcs, // spec.mode.context omitted or "environments"
    PromotionStrategy:      ps,
    HTTPResponses: []simulatortypes.HTTPResponse{
        {Branch: "dev",  Response: simulatortypes.Response{StatusCode: 200, Body: map[string]any{"ok": true}}},
        {Branch: "prod", Response: simulatortypes.Response{StatusCode: 503}},
    },
})
// result.Status.Environments[*].Phase — "success" for dev, "pending" for prod
// result.RenderedRequests — two entries, one per branch
```

### `promotionstrategy`

At most one HTTP request fires per reconcile, shared across all applicable environments. Only `HTTPResponses[0]` is consulted; `Branch` on that entry is ignored. `result.RenderedRequests[0].Branch` is always `""`.

```go
wrcs.Spec.Mode = promoterv1alpha1.ModeSpec{
    Context: promoterv1alpha1.ContextPromotionStrategy,
    Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}},
}

result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
    WebRequestCommitStatus: wrcs,
    PromotionStrategy:      ps,
    HTTPResponses: []simulatortypes.HTTPResponse{
        {Response: simulatortypes.Response{StatusCode: 200, Body: map[string]any{"approved": true}}},
        // extra entries ignored in promotionstrategy context
    },
})
// result.Status.PromotionStrategyContext — phase and per-branch phases
// result.Status.Environments — nil (not populated in promotionstrategy context)
// result.RenderedRequests — one entry with Branch == ""
```

## Round-tripping across reconciles

`Result.Status` is exactly what the controller would write to `WebRequestCommitStatus.Status`. Feed it back into the next call to model a follow-up reconcile with accumulated `TriggerOutput`, `ResponseOutput`, and `SuccessOutput` available to expressions.

```go
// First reconcile — trigger fires, HTTP runs, outputs are captured
r1, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
    WebRequestCommitStatus: wrcs,
    PromotionStrategy:      ps,
    HTTPResponses:          []simulatortypes.HTTPResponse{{Branch: "dev", Response: simulatortypes.Response{StatusCode: 200}}},
})

// Second reconcile — feed previous Status back in
wrcs.Status = r1.Status
r2, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
    WebRequestCommitStatus: wrcs,
    PromotionStrategy:      ps,
    // No HTTPResponses needed if the trigger won't fire this reconcile
})
// r2 sees r1's TriggerOutput/ResponseOutput/SuccessOutput in template and expression data
```

## Per-branch HTTP mocks

In `environments` context, the first `HTTPResponses` entry whose `Branch` matches the environment branch wins. You can give different branches different responses to test partial-success scenarios:

```go
result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
    WebRequestCommitStatus: wrcs,
    PromotionStrategy:      ps,
    HTTPResponses: []simulatortypes.HTTPResponse{
        {Branch: "dev",  Response: simulatortypes.Response{StatusCode: 200}},
        {Branch: "prod", Response: simulatortypes.Response{StatusCode: 503}},
    },
})
// dev → success, prod → pending
```

If the trigger fires for a branch and no matching `HTTPResponses` entry exists, `Simulate` returns an error naming the branch.

## Namespace metadata

`Input.NamespaceMetadata` is forwarded to all template and expression data as `.NamespaceMetadata.Labels` and `.NamespaceMetadata.Annotations`, matching how the controller reads the live namespace:

```go
result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
    WebRequestCommitStatus: wrcs,
    PromotionStrategy:      ps,
    NamespaceMetadata: simulatortypes.NamespaceMetadata{
        Labels:      map[string]string{"team": "payments", "env": "prod"},
        Annotations: map[string]string{"cost-center": "cc-42"},
    },
    HTTPResponses: ...,
})
```

## Inspecting the result

| Field | What it contains |
|---|---|
| `result.Status` | Exact `WebRequestCommitStatus.Status` the controller would write. Feed back as `Input.WebRequestCommitStatus.Status` for round-tripping. |
| `result.Status.Environments` | Per-branch `Phase`, `TriggerOutput`, `ResponseOutput`, `SuccessOutput`, `LastResponseStatusCode`, `LastSuccessfulSha`. Populated in `environments` context only. |
| `result.Status.PromotionStrategyContext` | Aggregate phase, `TriggerOutput`, `ResponseOutput`, `SuccessOutput`, `PhasePerBranch`, `LastSuccessfulShas`. Populated in `promotionstrategy` context only. |
| `result.RenderedRequests` | The HTTP request(s) the controller would have sent — `Method`, `URL`, `Headers`, `Body`, `Branch`. Use to verify template rendering. |
| `result.CommitStatuses` | The `*v1alpha1.CommitStatus` CRs the controller would have upserted — one per applicable environment. `ObjectMeta` (Name/Namespace/Labels) and `Spec` are fully populated; `OwnerReferences` and `Status` are left empty. |

## Limitations

- **Single reconcile per call.** Each `Simulate` call models exactly one reconcile loop iteration. Chain calls manually to model a sequence.
- **No live cluster.** `CommitStatuses` in the result have no `OwnerReferences` and no observed `Status` — the simulator has no Kubernetes API server to reference.
- **No real HTTP.** The simulator never opens a network connection. All HTTP behavior comes from `HTTPResponses`.
- **No rate limiting or requeue.** `requeueDuration` and `pollingInterval` affect whether a request fires in a given reconcile, but the simulator does not model actual time passage. Set `Interval: 0` / `RequeueDuration: 0` to make the trigger always eligible.

## Field reference

For field-level documentation (types, defaults, required/optional, all template variables), use:

```bash
kubectl explain webrequestcommitstatus.spec
kubectl explain webrequestcommitstatus.spec.mode
kubectl explain webrequestcommitstatus.spec.success
```

Or browse the source: `api/v1alpha1/webrequestcommitstatus_types.go`.

The `simulatortypes` package godoc (`webrequestsimulator/simulatortypes/simulatortypes.go`) documents `Input`, `Result`, `HTTPResponse`, and `RenderedRequest` in full.
