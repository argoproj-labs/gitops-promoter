import type { KubernetesObjectMeta } from './kubernetes';
import type { components } from './generated/view.gen';

export type { KubernetesObjectMeta } from './kubernetes';

/** Namespaced API resource with standard Kubernetes identity fields and ObjectMeta. */
export type KubernetesResource<T> = Omit<T, 'apiVersion' | 'kind' | 'metadata'> & {
  apiVersion: string;
  kind: string;
  metadata: KubernetesObjectMeta;
};

type Schemas = components['schemas'];

/** Dashboard aggregation resource (view.promoter.argoproj.io/v1alpha1). */
export type PromotionStrategyDetails = KubernetesResource<Schemas['PromotionStrategyDetails']>;

/** PromotionStrategy CRD (embedded in the bundle; also used by the Argo CD extension). */
export type PromotionStrategy = KubernetesResource<Schemas['PromotionStrategy']>;

/** Embedded in PromotionStrategyDetails.changeTransferPolicies. */
export type ChangeTransferPolicy = Schemas['ChangeTransferPolicy'];

/** Embedded in PromotionStrategyDetails.changeTransferPolicyHistories. */
export type ChangeTransferPolicyHistory = Schemas['ChangeTransferPolicyHistory'];

/** Embedded in PromotionStrategyDetails.restoreActiveCommits. */
export type RestoreActiveCommit = Schemas['RestoreActiveCommit'];
