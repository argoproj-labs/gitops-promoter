import type { V1ObjectMeta } from '@kubernetes/client-node';

/**
 * ObjectMeta for namespaced promoter resources returned by the API (list/get/SSE).
 *
 * Upstream `V1ObjectMeta` keeps `name` and `namespace` optional because the same
 * type is used for create requests and partial objects. Persisted namespaced
 * resources always include both.
 */
export type KubernetesObjectMeta = Omit<V1ObjectMeta, 'name' | 'namespace'> & {
  name: string;
  namespace: string;
};

/** ObjectMeta for cluster-scoped promoter resources (e.g. ClusterScmProvider). */
export type ClusterKubernetesObjectMeta = Omit<V1ObjectMeta, 'name'> & {
  name: string;
};
