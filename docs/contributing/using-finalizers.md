# Using finalizers

Kubernetes [finalizers](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/) delay removal of an object until asynchronous work finishes. GitOps Promoter controllers use them for ordering (for example closing a pull request in the SCM, or clearing cross-resource dependencies). Operators troubleshooting stuck deletes should read [Finalizers](../debugging/finalizers.md) first; this page is for **contributors** adding or changing controller behavior.

## Naming

Define finalizer strings as exported constants in [`api/v1alpha1/constants.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/api/v1alpha1/constants.go) so operators, RBAC authors, and tests share one source of truth.

Follow these conventions (use the **singular, lowercase** API resource kind in the DNS label, matching common Kubernetes practice):

| Where the finalizer is stored | String pattern | Example |
| ------------------------------ | -------------- | ------- |
| On a resource **for its own** lifecycle / cleanup | `<kind>.<group>/finalizer` | `pullrequest.promoter.argoproj.io/finalizer` on `PullRequest`; `changetransferpolicy.promoter.argoproj.io/finalizer` on `ChangeTransferPolicy` |
| On a resource **placed by another controller kind** (cross-resource) | `<controller-kind>.<group>/<finalized-kind>-finalizer` | `changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer` on `PullRequest` when `ChangeTransferPolicy` must observe PR status before the PR object goes away |

The controller that **owns** the cleanup logic is responsible for **adding** and **removing** the finalizer it defines. Names should stay stable across releases once shipped; changing a string strands objects that still list the old value in `metadata.finalizers`.

## Who sets finalizers

**Cluster users should not be asked to add promoter finalizers by hand.** Manifests and Helm values are not the right place to wire in cleanup finalizers.

The usual pattern is:

1. On reconcile, if the object is **not** deleting and does not yet carry the controller’s own finalizer, **add** it (typically with `controllerutil.AddFinalizer` and an `Update`).
2. When `deletionTimestamp` is set, run cleanup (external calls, clearing finalizers on other objects, and so on), then **remove** the finalizer so the API server can complete deletion.

That keeps behavior idempotent and matches what operators expect from a Kubernetes controller.

## Coordinating deletion across kinds

Sometimes reconciler A must **wait** until reconciler B clears a finalizer on a related object before A can clear **its** finalizer (for example ordering between a parent policy and child pull requests).

If A only watches B with predicates that ignore metadata-only updates (such as **generation-only** filters), B’s finalizer list can change while generation stays the same, and A may not run again until some unrelated event—leaving A stuck in `Terminating`.

**Best practice:** when A depends on B’s finalizers during B’s deletion, ensure A is enqueued when B’s **finalizer count changes** while B is being deleted. For example combine your existing predicates with a small custom predicate whose `Update` path returns true when `B.DeletionTimestamp` is non-nil and `len(B.Finalizers)` changes. That makes cross-resource cleanup converge quickly without widening reconcile on every unrelated B update.

For the primary resource A itself, continue to reconcile on generation (and any other signals your controller already uses); the extra predicate applies where you **watch** the dependency.

## See also

- [Finalizers](../debugging/finalizers.md) — operator-facing list of promoter finalizers and risks of removing them manually.
