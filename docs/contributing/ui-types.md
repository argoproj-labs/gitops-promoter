# UI TypeScript types from the view APIService

The dashboard consumes the aggregated `PromotionStrategyDetails` resource (`view.promoter.argoproj.io/v1alpha1`). The Argo CD extension still loads raw `PromotionStrategy` CRDs from the Argo CD tree. Both shapes are generated from the view APIService OpenAPI definitions.

## Source of truth

1. Go API types in `api/v1alpha1/` and `api/view/v1alpha1/`
2. `make generate-apiserver` → `api/view/v1alpha1/zz_generated.openapi.go`
3. `make generate-ui-types` → `ui/shared/src/types/generated/view.gen.ts`

Do not edit `view.gen.ts` by hand.

## Regenerating after API changes

```bash
make generate-apiserver   # when api/view/ or embedded api/v1alpha1 types change
make generate-ui-types
```

Commit updated OpenAPI Go output and `ui/shared/src/types/generated/view.gen.ts`.

When adding a **new commit-status gate** to `PromotionStrategyDetails`, also follow the manual view steps in [Dashboard view bundle](developing-a-commitstatus.md#dashboard-view-bundle) (bundle field, `buildBundle` list, apiserver RBAC, `make build-installer`).

CI runs `make generate-ui-types` in the **Check Codegen** job and fails if the generated file drifts.

## Layout

| Path | Purpose |
|------|---------|
| `hack/view2ts/` | Go tool: openapi-gen definitions → OpenAPI 3.0 JSON (PromotionStrategyDetails closure) |
| `ui/codegen/` | `openapi-typescript` runner (dev-only npm package) |
| `ui/shared/src/types/generated/view.gen.ts` | Generated view API types (committed) |
| `ui/shared/src/types/view.ts` | Named exports: `PromotionStrategyDetails`, embedded CRD shapes, `KubernetesObjectMeta` |
| `ui/shared/src/types/kubernetes.ts` | Re-export of `V1ObjectMeta` |
| `ui/shared/src/types/promotion.ts` | UI view types and aliases (e.g. `Environment` = `EnvironmentStatus`) |

## Post-processing

`hack/view2ts` walks the openapi-gen schema closure rooted at `PromotionStrategyDetails` and `PromotionStrategyDetailsList` (embedded types such as `PromotionStrategy` are included transitively). It adds `required: [apiVersion, kind, metadata]` on `PromotionStrategyDetails` and `PromotionStrategy` before TypeScript codegen.

`metadata` uses the upstream `ObjectMeta` schema from openapi-gen. `ui/shared/src/types/view.ts` replaces it with `KubernetesObjectMeta` (`V1ObjectMeta` plus required `name` and `namespace` for namespaced resources).

## View types vs API types

Generated `view.gen.ts` mirrors the view APIService OpenAPI. The UI also uses **view types** that are not in the API:

- `EnrichedEnvDetails`, `Check`, `PromotionPhase` in `promotion.ts`
- Enrichment logic in `ui/shared/src/utils/PSData.ts`

Aliases in `promotion.ts` avoid name clashes:

- `Environment` — per-environment status assembled for Card/PSData (`EnvironmentStatus`)
- `BranchCommitStatus` — inline branch status (not the `CommitStatus` CRD)
- `EnvironmentPullRequest` — embedded PR state (not the `PullRequest` CRD)

Use `CommitStatusResource` and `PullRequestResource` from `ui/shared/src/types/view.ts` when typing those CRD resources embedded in the bundle.

## Linting

Dashboard and extension ESLint enable `@typescript-eslint/no-unnecessary-condition` (type-aware; requires `tsconfig.eslint.json`). Shared sources are linted via the dashboard `npm run lint` from the `ui/` directory. Tests disable this rule for partial fixtures.
