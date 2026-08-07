import type {
  DAGCommitStatus,
  DAGEnvironment,
  PreviousEnvironmentCommitStatus,
  PromotionStrategyDetails,
} from '../types/view';

export interface TopologyNode {
  branch: string;
  dependsOn: string[];
}

export interface PromotionTopology {
  key: string;
  source: {
    kind: 'DAGCommitStatus' | 'PreviousEnvironmentCommitStatus';
    name: string;
  };
  nodes: TopologyNode[];
  isChain: boolean;
  materialized: boolean;
}

export function computeTopologyDepths(nodes: TopologyNode[]): Map<string, number> {
  const byBranch = new Map(nodes.map((node) => [node.branch, node]));
  const depths = new Map<string, number>();

  const resolve = (branch: string, ancestors: Set<string>): number => {
    const cached = depths.get(branch);
    if (cached !== undefined) return cached;
    if (ancestors.has(branch)) return 0;

    const node = byBranch.get(branch);
    const dependencies = node?.dependsOn ?? [];
    const nextAncestors = new Set(ancestors).add(branch);
    const depth =
      dependencies.length === 0
        ? 0
        : 1 + Math.max(...dependencies.map((dependency) => resolve(dependency, nextAncestors)));

    depths.set(branch, depth);
    return depth;
  };

  for (const node of nodes) {
    resolve(node.branch, new Set());
  }

  return depths;
}

function normalizeNodes(environments: DAGEnvironment[]): TopologyNode[] {
  return environments.map((environment) => ({
    branch: environment.branch,
    dependsOn: environment.dependsOn ?? [],
  }));
}

function chainFromBranches(branches: string[]): TopologyNode[] {
  return branches.map((branch, index) => ({
    branch,
    dependsOn: index === 0 ? [] : [branches[index - 1]],
  }));
}

function isChainMatchingOrder(nodes: TopologyNode[], branches: string[]): boolean {
  if (nodes.length !== branches.length) return false;

  return nodes.every((node, index) => {
    if (node.branch !== branches[index]) return false;
    const expectedDependencies = index === 0 ? [] : [branches[index - 1]];
    return (
      node.dependsOn.length === expectedDependencies.length &&
      node.dependsOn.every((dependency, dependencyIndex) => {
        return dependency === expectedDependencies[dependencyIndex];
      })
    );
  });
}

function previousEnvironmentOwner(
  dag: DAGCommitStatus,
  previousEnvironmentStatuses: PreviousEnvironmentCommitStatus[],
): PreviousEnvironmentCommitStatus | undefined {
  const owner = dag.metadata?.ownerReferences?.find(
    (reference) =>
      reference.kind === 'PreviousEnvironmentCommitStatus' && reference.controller !== false,
  );
  if (!owner) return undefined;

  return previousEnvironmentStatuses.find((status) => status.metadata?.name === owner.name);
}

export function resolvePromotionTopologies(bundle: PromotionStrategyDetails): PromotionTopology[] {
  const branches = bundle.promotionStrategy.spec.environments.map((environment) => {
    return environment.branch;
  });
  const previousEnvironmentStatuses = bundle.previousEnvironmentCommitStatuses ?? [];
  const materializedPreviousEnvironmentStatuses = new Set<string>();

  const topologies = (bundle.dagCommitStatuses ?? []).map((dag) => {
    const previousEnvironmentStatus = previousEnvironmentOwner(dag, previousEnvironmentStatuses);
    const nodes = normalizeNodes(dag.spec.environments);

    if (previousEnvironmentStatus?.metadata?.name) {
      materializedPreviousEnvironmentStatuses.add(previousEnvironmentStatus.metadata.name);
    }

    return {
      key: dag.spec.key,
      source: previousEnvironmentStatus
        ? {
            kind: 'PreviousEnvironmentCommitStatus' as const,
            name: previousEnvironmentStatus.metadata?.name ?? '',
          }
        : {
            kind: 'DAGCommitStatus' as const,
            name: dag.metadata?.name ?? '',
          },
      nodes,
      isChain: isChainMatchingOrder(nodes, branches),
      materialized: true,
    };
  });

  for (const previousEnvironmentStatus of previousEnvironmentStatuses) {
    const name = previousEnvironmentStatus.metadata?.name ?? '';
    if (materializedPreviousEnvironmentStatuses.has(name)) continue;

    topologies.push({
      key: previousEnvironmentStatus.spec.key,
      source: {
        kind: 'PreviousEnvironmentCommitStatus',
        name,
      },
      nodes: chainFromBranches(branches),
      isChain: true,
      materialized: false,
    });
  }

  return topologies;
}
