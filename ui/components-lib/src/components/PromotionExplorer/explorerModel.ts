import type { PromotionStrategy } from '@shared/types/promotion';
import {
  buildPromotionGraph,
  edgePath,
  layoutPromotionGraph,
  type GraphLayout,
  type PositionedNode,
  type PromotionGraph,
  type PromotionGraphSource,
} from '@shared/utils/promotionGraph';
import { buildMatrix } from './buildMatrix';
import { isEmptyCellKind } from './helpers';
import type { CellKind, CommitRow, EnvColumn } from './types';

export type NodeStatus = 'live' | 'proposed' | 'failed' | 'previously-live' | 'not-reached';

export const NODE_STATUS_LABELS: Record<NodeStatus, string> = {
  live: 'Live',
  proposed: 'Proposed',
  failed: 'Failed',
  'previously-live': 'Previously live',
  'not-reached': 'Not reached',
};

export interface ExplorerNode extends PositionedNode {
  kicker: string | null;
  title: string;
  status: NodeStatus;
  hash?: string;
  rowId?: string;
  hasColumn: boolean;
}

export interface ExplorerEdge {
  id: string;
  from: string;
  to: string;
  d: string;
}

export interface ExplorerModel {
  envs: EnvColumn[];
  rows: CommitRow[];
  graph: PromotionGraph;
  layout: GraphLayout;
  nodes: ExplorerNode[];
  nodeById: Map<string, ExplorerNode>;
  edges: ExplorerEdge[];
}

export function splitBranch(branch: string): { kicker: string | null; title: string } {
  const at = branch.lastIndexOf('/');
  return at === -1
    ? { kicker: null, title: branch }
    : { kicker: branch.slice(0, at), title: branch.slice(at + 1) };
}

export function statusFromCellKind(kind: CellKind): NodeStatus | null {
  switch (kind) {
    case 'live':
      return 'live';
    case 'in-flight':
      return 'proposed';
    case 'failed':
      return 'failed';
    case 'was-here':
    case 'restored':
    case 'no-op':
      return 'previously-live';
    default:
      return null;
  }
}

export function deriveNodeStatus(
  branch: string,
  rows: readonly CommitRow[],
): { status: NodeStatus; hash?: string; rowId?: string } {
  const cells = rows
    .map((row) => ({ row, cell: row.cells[branch] }))
    .filter(({ cell }) => cell && !isEmptyCellKind(cell.kind));
  const pick = (found: (typeof cells)[number] | undefined, status: NodeStatus) =>
    found ? { status, hash: found.row.dryShaShort, rowId: found.row.id } : undefined;

  const proposed = cells.find(({ cell }) => cell.isProposed || cell.kind === 'in-flight');
  if (proposed) return pick(proposed, proposed.cell.kind === 'failed' ? 'failed' : 'proposed')!;

  const live = cells.find(({ cell }) => cell.isLive);
  if (live) return pick(live, live.cell.kind === 'failed' ? 'failed' : 'live')!;

  const failed = pick(
    cells.find(({ cell }) => cell.kind === 'failed'),
    'failed',
  );
  if (failed) return failed;

  const was = pick(
    cells.find(({ cell }) => statusFromCellKind(cell.kind) === 'previously-live'),
    'previously-live',
  );
  return was ?? { status: 'not-reached' };
}

export function orderEnvsByLayout(
  envs: readonly EnvColumn[],
  layout: Pick<GraphLayout, 'layers'>,
): EnvColumn[] {
  const rank = new Map<string, number>();
  let next = 0;
  for (const layer of layout.layers) for (const id of layer) rank.set(id, next++);
  return envs
    .map((env, i) => ({ env, i, rank: rank.get(env.branch) ?? Number.MAX_SAFE_INTEGER }))
    .sort((a, b) => a.rank - b.rank || a.i - b.i)
    .map(({ env }) => env);
}

function graphSourceFor(
  strategy: PromotionStrategy,
  envs: readonly EnvColumn[],
): PromotionGraphSource {
  if ((strategy.spec?.environments?.length ?? 0) > 0) return strategy;
  return { spec: { environments: envs.map((env) => ({ branch: env.branch })) } };
}

export function buildExplorerModel(strategy: PromotionStrategy): ExplorerModel {
  const matrix = buildMatrix(strategy);
  const graph = buildPromotionGraph(graphSourceFor(strategy, matrix.envs));
  const layout = layoutPromotionGraph(graph);
  const columns = new Set(matrix.envs.map((env) => env.branch));

  const nodes: ExplorerNode[] = layout.nodes.map((positioned) => {
    const { kicker, title } = splitBranch(positioned.branch);
    return {
      ...positioned,
      kicker,
      title,
      ...deriveNodeStatus(positioned.branch, matrix.rows),
      hasColumn: columns.has(positioned.branch),
    };
  });
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const edges: ExplorerEdge[] = graph.edges.map((edge) => ({
    id: edge.id,
    from: edge.from,
    to: edge.to,
    d: edgePath(nodeById.get(edge.from)!, nodeById.get(edge.to)!),
  }));

  return {
    envs: orderEnvsByLayout(matrix.envs, layout),
    rows: matrix.rows,
    graph,
    layout,
    nodes,
    nodeById,
    edges,
  };
}
