# CRD Development Best Practices

This document describes best practices for maintaining Custom Resource Definitions (CRDs) and their examples in GitOps Promoter. **These practices apply to all CRDs in the project.**

## Example CRs in testdata

Example manifests live under `internal/controller/testdata/` and are embedded into controller tests. They serve as both documentation (included in [CRD Specs](../crd-specs.md)) and as executable spec adherence tests.

### Completeness

- **Populate all required fields** in each example. Required fields are marked in the API types with `+kubebuilder:validation:Required` or `+required`. Omit optional fields when not needed for the example, but ensure required ones have valid values.
- **Match the API types.** When adding or changing fields in `api/v1alpha1/*_types.go`, update the corresponding testdata YAML so the example stays valid and reflects the full shape of the spec where helpful.
- **Use valid values.** Respect validation rules (patterns, enums, min/max length, etc.) so examples would pass both client-side decoding and server-side admission.

### ExactlyOneOf: show all options for documentation

Several CRDs use `+kubebuilder:validation:ExactlyOneOf` (e.g. `GitRepositorySpec` and `ScmProviderSpec`), where exactly one of a set of fields must be set at apply time (e.g. `github`, `gitlab`, `forgejo`, …).

- **Keep all options present in the example** so readers can see at a glance what fields are available for each provider. Example files only need to pass JSON unmarshaling (strict decode), not full CRD validation.
- **Comment in the YAML** that at apply time only one provider should be set.

### One example per CRD

- Every user-facing CRD that has a controller and is fully implemented should have a corresponding example file in `internal/controller/testdata/`, e.g. `PromotionStrategy.yaml`, `GitRepository.yaml`, `GitCommitStatus.yaml`. Placeholder or non-functional CRDs can be omitted from testdata and the CRD Specs page until they are implemented.
- Naming: `internal/controller/testdata/<Kind>.yaml` (e.g. `GitRepository.yaml`).

## Spec adherence tests

Each controller test suite should include a test that ensures the embedded testdata example decodes strictly into the CRD’s Go type. This catches drift between the example YAML and the type definitions (including renames, removals, and new required fields).

### Pattern

1. **Embed the testdata file** in the controller’s `_test.go` file:

   ```go
   //go:embed testdata/GitRepository.yaml
   var testGitRepositoryYAML string
   ```

2. **Add a test** that unmarshals into the CRD type with strict decoding (no unknown fields):

   ```go
   Context("When unmarshalling the test data", func() {
       It("should unmarshal the GitRepository resource", func() {
           err := unmarshalYamlStrict(testGitRepositoryYAML, &promoterv1alpha1.GitRepository{})
           Expect(err).ToNot(HaveOccurred())
       })
   })
   ```

3. **Use the shared helper** `unmarshalYamlStrict` from `internal/controller/suite_test.go`, which decodes YAML to a map, converts to JSON, then decodes into the target struct with `DisallowUnknownFields()`. This ensures the example has no extra or misspelled keys and matches the current Go types.

Apply this pattern for **every CRD that has an example** in testdata (e.g. PromotionStrategy, ChangeTransferPolicy, PullRequest, CommitStatus, GitRepository, ScmProvider, ClusterScmProvider, ArgoCDCommitStatus, TimedCommitStatus, ControllerConfiguration, GitCommitStatus).

## CRD documentation page

- **Every CRD must be represented on the [CRD Specs](../crd-specs.md) page.** For each CRD, include:
  - A short description of what the resource does and when to use it.
  - The example YAML from testdata using the include directive, e.g. `{!internal/controller/testdata/<Kind>.yaml!}`.

When you add a new CRD:

1. Add the type in `api/v1alpha1/`.
2. Add (or generate) the CRD manifest under `config/crd/bases/`.
3. Add a complete example under `internal/controller/testdata/<Kind>.yaml`.
4. In the controller test, embed the example and add the unmarshal test as above.
5. Add a section for the CRD on [CRD Specs](../crd-specs.md) with description and included example.

## Summary checklist for new or updated CRDs

- [ ] Types in `api/v1alpha1/` define all required/optional fields and validation.
- [ ] Example in `internal/controller/testdata/<Kind>.yaml` is complete (all provider options or fields visible as desired for docs; required fields have valid values so unmarshaling succeeds).
- [ ] Controller test embeds the example and has a “When unmarshalling the test data” test using `unmarshalYamlStrict`.
- [ ] `docs/crd-specs.md` has a section for the CRD with description and included example.

Applying these practices keeps CRD examples, types, tests, and documentation in sync and reduces invalid or outdated examples.
