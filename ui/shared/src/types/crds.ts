import type { ClusterKubernetesObjectMeta, KubernetesObjectMeta } from './kubernetes';
import type { components } from './generated/crds.gen';

export type { components };
export type { ClusterKubernetesObjectMeta, KubernetesObjectMeta } from './kubernetes';

/** Namespaced CRD resource with standard Kubernetes identity fields and ObjectMeta. */
export type KubernetesResource<T> = Omit<T, 'apiVersion' | 'kind' | 'metadata'> & {
  apiVersion: string;
  kind: string;
  metadata: KubernetesObjectMeta;
};

/** Cluster-scoped CRD resource (namespace is not set on metadata). */
export type ClusterKubernetesResource<T> = Omit<T, 'apiVersion' | 'kind' | 'metadata'> & {
  apiVersion: string;
  kind: string;
  metadata: ClusterKubernetesObjectMeta;
};

type Schemas = components['schemas'];

export type ArgoCDCommitStatus = KubernetesResource<Schemas['ArgoCDCommitStatus']>;
export type ChangeTransferPolicy = KubernetesResource<Schemas['ChangeTransferPolicy']>;
export type ClusterScmProvider = ClusterKubernetesResource<Schemas['ClusterScmProvider']>;
export type CommitStatusResource = KubernetesResource<Schemas['CommitStatus']>;
export type ControllerConfiguration = KubernetesResource<Schemas['ControllerConfiguration']>;
export type GitCommitStatus = KubernetesResource<Schemas['GitCommitStatus']>;
export type GitRepository = KubernetesResource<Schemas['GitRepository']>;
export type PromotionStrategy = KubernetesResource<Schemas['PromotionStrategy']>;
export type PullRequestResource = KubernetesResource<Schemas['PullRequest']>;
export type RevertCommit = KubernetesResource<Schemas['RevertCommit']>;
export type ScmProvider = KubernetesResource<Schemas['ScmProvider']>;
export type TimedCommitStatus = KubernetesResource<Schemas['TimedCommitStatus']>;
export type WebRequestCommitStatus = KubernetesResource<Schemas['WebRequestCommitStatus']>;
