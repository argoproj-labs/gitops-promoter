# Updating resource status

Every reconciled CRD has its `status` subresource written through a single chokepoint:
the deferred [`utils.HandleReconciliationResult`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/utils/utils.go)
call at the top of each `Reconcile` function. This page is for **contributors** who are
writing or modifying controllers and need to know how to populate status correctly.

## How status gets written

1. Each controller starts its `Reconcile` with:
    ```go
    defer utils.HandleReconciliationResult(ctx, startTime, &obj,
        r.Client, r.Recorder,
        constants.<Kind>ControllerFieldOwner,
        &result, &err)
    ```
2. During reconciliation, the controller mutates `obj.Status` in memory. **Do not call
   `r.Status().Update` or `r.Status().Patch` yourself.**
3. At the end of the reconcile, the deferred helper sets the `Ready` condition based on
   whether `*err` is nil, then applies the whole status subresource via
   **Server-Side Apply** under the per-controller `FieldOwner` with `ForceOwnership`.
4. If the full apply is rejected (for example, OpenAPI schema or CEL validation on some
   status field), the helper retries with a conditions-only SSA so the `Ready=False`
   condition describing the failure still reaches the user. The next successful reconcile
   naturally re-owns all fields.

## Per-controller `FieldOwner`

Every reconciled CRD has its own stable field-owner string declared in
[`internal/types/constants/configurations.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/types/constants/configurations.go).
The deferred helper uses that constant as the SSA `FieldOwner`. When adding a new
controller:

1. Add a `<Kind>ControllerFieldOwner = "promoter.argoproj.io/<kind>-controller"` constant.
2. Pass it as the sixth argument to `utils.HandleReconciliationResult`.
3. If the CRD has any status SSA fallback test expectations, reuse that same constant —
   do **not** invent per-call owners.

## Apply-config dispatch

The generic helper builds the SSA patch body by dispatching on object type in
[`internal/utils/status_apply.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/utils/status_apply.go).
For each reconciled CRD there is one case that:

- Constructs the typed root apply configuration (e.g. `acv1alpha1.ChangeTransferPolicy(name, ns)`).
- Populates the status apply configuration. The full-apply path uses a JSON round-trip
  from `obj.Status` into the typed status apply configuration, so every field with a
  `json` tag is included automatically.
- Returns the combined apply configuration to `HandleReconciliationResult`.

When you add a **new reconciled CRD**:

1. Generate apply configurations with `make build-installer`.
2. Add a new `case *promoterv1alpha1.<Kind>:` branch to `statusApplyConfig` in
   `internal/utils/status_apply.go` that mirrors an existing case.
3. Add the corresponding field-owner constant (above).

When you add a **new field to an existing status struct**, no code changes are needed in
`status_apply.go` — the JSON round-trip picks up the new field automatically. If the
field has custom JSON marshaling that does not mirror the apply-config shape (rare for
controller-gen output), add a test that exercises the round-trip.

## What you should *not* do

- **Do not** write `r.Status().Update` or `r.Status().Patch` from inside a reconciler.
  Mutate `obj.Status` and let the defer flush it.
- **Do not** use a different field owner for conditions vs. other status fields. The
  helper uses one owner per controller for the whole subresource; mixing owners breaks
  the "subsequent apply re-owns everything" guarantee of the conditions-only fallback.
- **Do not** add `ForceOwnership` guard logic to individual controllers — SSA with
  `ForceOwnership` is the project-wide contract for status subresource writes.

## Where to look

- [`internal/utils/utils.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/utils/utils.go) — `HandleReconciliationResult` implementation.
- [`internal/utils/status_apply.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/utils/status_apply.go) — per-kind apply-configuration dispatch.
- [`internal/types/constants/configurations.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/types/constants/configurations.go) — field-owner constants.
- [`applyconfiguration/api/v1alpha1/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/applyconfiguration/api/v1alpha1) — generated apply configurations.
