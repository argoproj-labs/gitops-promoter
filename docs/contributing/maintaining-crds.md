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

```markdown
### PromotionStrategy

…description…

```yaml
{!internal/controller/testdata/PromotionStrategy.yaml!}
```
```

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

## Regenerate and verify

After Go type and marker changes:

1. **`make build-installer`** — CRD bases, deepcopy, applyconfiguration, extension icon styles, `dist/install.yaml`.
2. **`go mod tidy`** if module deps changed.
3. **`make test-parallel`** — includes the strict unmarshal tests above.
4. **`make lint-docs`** if you edited `docs/`.

CI’s **Check Codegen** job runs `make build-installer` and fails on drift; see [Continuous Integration](ci.md).

## API design reminders

- Put validation on Go types with kubebuilder markers (`// +kubebuilder:validation:…`, CEL `XValidation`, and so on); do not hand-edit `config/crd/bases/` except via generation.
- Prefer **`// +k8s:immutable`** on fields that must not change after set (controller-gen v0.21+); it emits the same `self == oldSelf` rule as hand-written immutability `XValidation` (see `PullRequest` `sourceBranch` / `targetBranch`).
- Use **`// +k8s:enum`** on a `type Foo string` plus `const` block only when **every** field typed `Foo` shares the same allowed values. Remove redundant field `Enum` markers only in that case. Do **not** put `+k8s:enum` on a type that is also used from status fields allowing `""` or a subset of consts (controller-gen applies the type enum to all references; field `Enum` is not merged for extras like `""`). Example in-tree: `ContextMode` on `WebRequestCommitStatus`. Plain `string` fields still use field `Enum`.
- Express reconciler RBAC with **`// +kubebuilder:rbac`** on controllers; regenerate manifests rather than editing `config/rbac/role.yaml` by hand.
- For SCM-facing types and commit-status controllers, see [Adding an SCM Provider](adding-an-scm-provider.md) and [Commit status development best practices](../commit-status-controllers/development-best-practices.md).

## Related docs

| Topic | Page |
|--------|------|
| User-facing CR reference | [CRD Specs](../crd-specs.md) |
| Status subresource / SSA | [Maintaining resource status](updating-status.md) |
| CI codegen checks | [Continuous Integration](ci.md) |
| Contributor overview | [Contributing overview](index.md) |
