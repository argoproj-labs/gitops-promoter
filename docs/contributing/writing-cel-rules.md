# Writing CEL Validation Rules

Our CRDs use [CEL validation rules](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules)
(`x-kubernetes-validations`) to enforce constraints the OpenAPI schema can't express on its own —
for example "this field must be a URL" or "this field is immutable". You add them as
[kubebuilder markers](https://book.kubebuilder.io/reference/markers/crd-validation) on the API
types in [`api/v1alpha1/`](https://github.com/argoproj-labs/gitops-promoter/tree/main/api/v1alpha1),
and `make build-installer` compiles them into the CRD bases under `config/crd/`.

```go
// +kubebuilder:validation:XValidation:rule="self == '' || isURL(self)",message="must be a valid URL"
RepoURL string `json:"repoURL,omitempty"`
```

## The apiserver cost budget

When a CRD is created or updated, the kube-apiserver estimates a **static worst-case cost** for
every CEL rule and rejects the CRD if it is too expensive. There are two limits
(from `k8s.io/apiextensions-apiserver`):

| Limit | Value | Applies to |
|---|---:|---|
| Per-rule | `10,000,000` | A single expression (`rule` or its `messageExpression`) |
| Per-schema | `100,000,000` | The sum of all expressions in one CRD version's schema |

The critical thing to understand is **cardinality**. A rule's cost is its base expression cost
multiplied by the worst-case number of times it can be evaluated. A rule on a scalar field is
evaluated once; the *same* rule on a field inside an unbounded (or large) array or map is multiplied
by that collection's maximum size — and the multiplier compounds through nested collections. So a
cheap-looking rule placed deep inside repeated `status` lists can dominate the budget.

## Keeping rules cheap

- **Bound your collections.** Add `+kubebuilder:validation:MaxItems` / `MaxProperties` (and string
  `MaxLength`) wherever practical. An unbounded collection makes the apiserver assume a very large
  worst-case cardinality.
- **Push rules up, not down.** Validating at the smallest necessary scope (a scalar field rather
  than every element of a nested list) avoids cardinality multiplication.
- **Prefer cheap operations.** Simple comparisons and library calls like `isURL()` are inexpensive;
  regex, `.all()`/`.exists()` over large collections, and string manipulation are not.
- **Watch `messageExpression`.** Its cost counts toward both limits too.

## Checking the cost

Estimate the cost of the current CRDs at any time:

```bash
make build-installer   # regenerates CRDs and the report
# or, if CRDs are already up to date:
make cel-cost-report
```

This runs [`hack/celcost`](https://github.com/argoproj-labs/gitops-promoter/tree/main/hack/celcost),
which reproduces the apiserver's budget calculation against `config/crd/bases` and writes the report
embedded below to `hack/celcost/report.md`. CI fails if that file is out of date, so after any change
that alters the CRDs (new fields, new rules, changed `MaxItems`, etc.) run **`make build-installer`**
and commit the full diff.

> [!NOTE]
> The estimate is the library's stricter, create-time, version-portable cost. A given kube-apiserver
> binary may be slightly more lenient (e.g. on updates that reuse already-stored expressions), so
> treat the report as a conservative early-warning signal rather than the exact server verdict.

## Current cost report

{!hack/celcost/report.md!}
