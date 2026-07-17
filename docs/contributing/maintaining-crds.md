# Maintaining CRDs

Use this checklist whenever you add a **new** API type or make a **breaking or structural** change to an existing CRD (new fields, renames, validation changes). It keeps user docs, OpenAPI metadata, and tests aligned.

## Checklist for each CRD kind

### 1. Example manifest in `internal/controller/testdata/`

Add or update `internal/controller/testdata/<Kind>.yaml` (PascalCase filename matching the kind, for example `PromotionStrategy.yaml`).

- Include a realistic **`spec`** with as many fields populated as practical so the example doubles as documentation and catches schema drift.
- When the type has a **`status`** subresource, populate representative **`status`** fields too (conditions, phase, nested state) so status shape changes break tests early.
- Use valid values that pass kubebuilder validation markers on the Go types.
- Keep names, namespaces, and references consistent with how controllers and envtest suites create related objects.

### 2. Embed the example on the CRD Specs page

Add or update a `### <Kind>` section in [`docs/crd-specs.md`](../crd-specs.md) with a short narrative, then include the example:

````markdown
### PromotionStrategy

…description…

```yaml
{!internal/controller/testdata/PromotionStrategy.yaml!}
```
````

MkDocs pulls the file in at build time via `markdown_include` (see `mkdocs.yml`). The heading text defines the anchor used by `externalDocs` (Material slugifies to lowercase, for example `### PromotionStrategy` → `#promotionstrategy`).

### 3. `externalDocs` on the root API type

On the **`//+kubebuilder:object:root=true`** struct in `api/v1alpha1/<kind>_types.go`, add a marker pointing at the stable docs URL for that section:

```go
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#promotionstrategy",description="CRD reference (examples and behavior)"
```

Regenerate CRDs with **`make manifests`** (or **`make build-installer`**). The URL must be a valid absolute URL; the fragment must match the CRD Specs heading anchor.

On **Kubernetes 1.36+**, clusters and kubectl can surface this link in `kubectl explain` as an `EXTERNAL DOCS` block (see [kubernetes/kubernetes#136988](https://github.com/kubernetes/kubernetes/pull/136988)). Older clusters still store the metadata in the CRD OpenAPI schema for other tooling.

### 4. Strict unmarshal test in the controller suite

In `internal/controller/<kind>_controller_test.go` (or the controller that owns the type), add:

```go
//go:embed testdata/<Kind>.yaml
var test<Kind>YAML string

var _ = Describe("<Kind> Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the <Kind> resource", func() {
			err := unmarshalYamlStrict(test<Kind>YAML, &promoterv1alpha1.<Kind>{})
			Expect(err).ToNot(HaveOccurred())
		})
	})
	// …
})
```

`unmarshalYamlStrict` (in [`internal/controller/suite_test.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/controller/suite_test.go)) unmarshals YAML and decodes with **`DisallowUnknownFields`**, so typos, removed fields, or wrong types in the example fail CI immediately.

Follow an existing controller test as a template, for example [`promotionstrategy_controller_test.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/controller/promotionstrategy_controller_test.go) or [`commitstatus_controller_test.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/internal/controller/commitstatus_controller_test.go).

If the type is reconciled and has status SSA behavior, also follow [Maintaining resource status](updating-status.md) for field owners, `observedGeneration`, and status apply tests.

### 5. Field index for commit-status gate kinds

When the new kind is a **commit-status gate** CRD with `spec.promotionStrategyRef` (same shape as `ArgoCDCommitStatus`, `TimedCommitStatus`, and the other built-in gates):

1. Register the type on the promoter scheme (`AddToScheme` / `SchemeBuilder`). That is enough for field indexes, instance-id cache partitioning, and resource-count metrics — you do not maintain separate kind lists for those.
2. In the gate controller, watch `PromotionStrategy` and list your kind with `client.MatchingFields{controller.PromotionStrategyRefField: ps.Name}` (do not namespace-list and filter in memory). See [Watching PromotionStrategy](developing-a-commitstatus.md#watching-promotionstrategy).

Skip this step for types that do not reference a `PromotionStrategy` or never use field selectors on the cache client.

### 6. Dashboard view bundle for commit-status gate kinds

In-tree gate managers are also exposed on the aggregated `PromotionStrategyDetails` resource. After the CRD is on the scheme, follow [Dashboard view bundle](developing-a-commitstatus.md#dashboard-view-bundle) (bundle field, `buildBundle` list, apiserver RBAC, and codegen).

## Regenerate and verify

After Go type and marker changes:

1. **`make build-installer`** — CRD bases, deepcopy, applyconfiguration, extension icon styles, and the `dist/` install bundles (includes apiserver RBAC from `config/apiserver/base/`).
2. **`make generate-apiserver`** and **`make generate-ui-types`** when `api/view/v1alpha1` or embedded promoter types in the view bundle change (see [UI TypeScript types](ui-types.md)).
3. **`go mod tidy`** if module deps changed.
4. **`make test-parallel`** — includes the strict unmarshal tests above.
5. **`make lint-docs`** if you edited `docs/`.

CI’s **Check Codegen** job runs `make build-installer`, `make generate-apiserver`, and `make generate-ui-types` and fails on drift; see [Continuous Integration](ci.md).

## API design reminders

- Put validation on Go types with kubebuilder markers (`// +kubebuilder:validation:…`, CEL `XValidation`, and so on); do not hand-edit `config/crd/bases/` except via generation.
- Prefer **`// +k8s:immutable`** on fields that must not change after set (controller-gen v0.21+) when that field has **no other** `XValidation` rules. It emits the same `self == oldSelf` CEL rule as a hand-written immutability `XValidation`. If the field also needs custom CEL checks, use explicit `XValidation` for immutability too — **do not mix `+k8s:` markers with `XValidation` on one field** ([controller-tools#1429](https://github.com/kubernetes-sigs/controller-tools/issues/1429); see [Writing CEL Validation Rules](writing-cel-rules.md)). Example in-tree: `PullRequest` `sourceBranch` / `targetBranch`.
- Use **`// +k8s:enum`** on a `type Foo string` plus `const` block only when **every** field typed `Foo` shares the same allowed values. Remove redundant field `Enum` markers only in that case. Do **not** put `+k8s:enum` on a type that is also used from status fields allowing `""` or a subset of consts (controller-gen applies the type enum to all references; field `Enum` is not merged for extras like `""`). Example in-tree: `ContextMode` on `WebRequestCommitStatus`. Plain `string` fields still use field `Enum`.
- Express reconciler RBAC with **`// +kubebuilder:rbac`** on the controller that performs each API call; regenerate manifests rather than editing `config/rbac/role.yaml` by hand. CRDs and built-in types such as Secrets have no writable `/finalizers` API subresource; update `metadata.finalizers` via `Update` on the main resource. For least-privilege RBAC, grant `*/finalizers: update` instead of `update` on the primary resource when the controller only changes finalizers.
- For SCM-facing types and commit-status controllers, see [Adding an SCM Provider](adding-an-scm-provider.md) and [Developing a CommitStatus](developing-a-commitstatus.md).

## Related docs

| Topic | Page |
|--------|------|
| User-facing CR reference | [CRD Specs](../crd-specs.md) |
| CEL rules and marker mixing | [Writing CEL Validation Rules](writing-cel-rules.md) |
| Status subresource / SSA | [Maintaining resource status](updating-status.md) |
| Dashboard aggregation API | [Dashboard Aggregation API](../advanced-usage/dashboard-apiserver.md) |
| UI types from the view API | [UI TypeScript types](ui-types.md) |
| CI codegen checks | [Continuous Integration](ci.md) |
| Contributor overview | [Contributing overview](index.md) |
