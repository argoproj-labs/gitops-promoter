# UI TypeScript types from CRDs

The dashboard and Argo CD extension use TypeScript types generated from the same CRD OpenAPI schemas that the Kubernetes API server validates.

## Source of truth

1. Go API types in `api/v1alpha1/`
2. `make manifests` → `config/crd/bases/promoter.argoproj.io_*.yaml`
3. `make generate-ui-types` → `ui/shared/src/types/generated/crds.gen.ts`

Do not edit `crds.gen.ts` by hand.

## Regenerating after API changes

```bash
make manifests
make generate-ui-types
```

Commit the updated CRD YAML and `ui/shared/src/types/generated/crds.gen.ts`.

CI runs `make generate-ui-types` in the **Check Codegen** job and fails if the generated file drifts.

## Layout

| Path | Purpose |
|------|---------|
| `hack/crd2ts/` | Go tool: CRD YAML → OpenAPI 3.0 JSON |
| `ui/codegen/` | `openapi-typescript` runner (dev-only npm package) |
| `ui/shared/src/types/generated/crds.gen.ts` | Generated CRD types (committed) |
| `ui/shared/src/types/crds.ts` | Named exports: required `apiVersion`/`kind`, `KubernetesObjectMeta` from `@kubernetes/client-node` |
| `ui/shared/src/types/kubernetes.ts` | Re-export of `V1ObjectMeta` |
| `ui/shared/src/types/promotion.ts` | UI view types and aliases (e.g. `Environment` = status slice item) |

## Post-processing

`hack/crd2ts` adds `required: [apiVersion, kind, metadata]` on each CRD root schema before TypeScript codegen so identity fields are not optional.

`metadata` is still an empty `type: object` in CRD YAML (Kubernetes convention). [`crds.ts`](../../ui/shared/src/types/crds.ts) replaces it with `KubernetesObjectMeta` (`V1ObjectMeta` plus required `name` and `namespace` for namespaced kinds).

## View types vs CRD types

Generated `crds.gen.ts` mirrors CRD OpenAPI for `spec`/`status`. The UI also uses **view types** that are not in the API:

- `EnrichedEnvDetails`, `Check`, `PromotionPhase` in `promotion.ts`
- Enrichment logic in `ui/shared/src/utils/PSData.ts`

Aliases in `promotion.ts` avoid name clashes with CRD kinds:

- `Environment` — `PromotionStrategy.status.environments[]` (not spec `environments`)
- `BranchCommitStatus` — inline branch status (not the `CommitStatus` CRD)
- `EnvironmentPullRequest` — embedded PR state (not the `PullRequest` CRD)

Use `CommitStatusResource` and `PullRequestResource` from `crds.ts` when typing those CRD resources.
