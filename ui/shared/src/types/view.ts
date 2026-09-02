import type { ClusterKubernetesObjectMeta, KubernetesObjectMeta } from './kubernetes';
import type { components } from './generated/view.gen';

export type { components };
export type { ClusterKubernetesObjectMeta, KubernetesObjectMeta } from './kubernetes';

/** Namespaced API resource with standard Kubernetes identity fields and ObjectMeta. */
export type KubernetesResource<T> = Omit<T, 'apiVersion' | 'kind' | 'metadata'> & {
  apiVersion: string;
  kind: string;
  metadata: KubernetesObjectMeta;
};

/** Cluster-scoped API resource (namespace is not set on metadata). */
export type ClusterKubernetesResource<T> = Omit<T, 'apiVersion' | 'kind' | 'metadata'> & {
  apiVersion: string;
  kind: string;
  metadata: ClusterKubernetesObjectMeta;
};

type Schemas = components['schemas'];

/** Dashboard aggregation resource (view.promoter.argoproj.io/v1alpha1). */
export type PromotionStrategyDetails = KubernetesResource<Schemas['PromotionStrategyDetails']>;

/** PromotionStrategy CRD (embedded in the bundle; also used by the Argo CD extension). */
export type PromotionStrategy = KubernetesResource<Schemas['PromotionStrategy']>;

/** Embedded in PromotionStrategyDetails.changeTransferPolicies. */
export type ChangeTransferPolicy = Schemas['ChangeTransferPolicy'];

/** Embedded in PromotionStrategyDetails (and list responses). */
export type PullRequestResource = Schemas['PullRequest'];

export type CommitStatusResource = Schemas['CommitStatus'];

export type GitRepository = Schemas['GitRepository'];

export type ScmProvider = Schemas['ScmProvider'];

export type ClusterScmProvider = Schemas['ClusterScmProvider'];
