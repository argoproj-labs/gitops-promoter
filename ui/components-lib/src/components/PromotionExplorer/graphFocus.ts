import { ancestors, type PromotionGraph } from '@shared/utils/promotionGraph';
import { isEmptyCellKind } from './helpers';
import { statusFromCellKind, type NodeStatus } from './explorerModel';
import type { CommitRow } from './types';

export interface CommitPath {
  rowId: string;
  nodes: Set<string>;
  tints: Record<string, NodeStatus>;
}

export type NodeMode = 'path' | 'dimmed';
export type EdgeMode = 'lit' | 'dimmed';

export interface NodeVisual {
  mode: NodeMode | null;
  tint: NodeStatus | null;
}

export function buildCommitPath(
  graph: PromotionGraph,
  row: CommitRow | undefined,
): CommitPath | null {
  if (!row) return null;
  const nodes = new Set<string>();
  const tints: Record<string, NodeStatus> = {};
  for (const [branch, cell] of Object.entries(row.cells)) {
    if (isEmptyCellKind(cell.kind) || !(branch in graph.parents)) continue;
    nodes.add(branch);
    const status = statusFromCellKind(cell.kind);
    if (status) tints[branch] = status;
    for (const parent of ancestors(graph, branch)) nodes.add(parent);
  }
  return nodes.size === 0 ? null : { rowId: row.id, nodes, tints };
}

export function nodeVisual(id: string, path: CommitPath | null): NodeVisual {
  if (!path) return { mode: null, tint: null };
  if (path.nodes.has(id)) return { mode: 'path', tint: path.tints[id] ?? null };
  return { mode: 'dimmed', tint: null };
}

export function edgeVisual(from: string, to: string, path: CommitPath | null): EdgeMode | null {
  if (!path) return null;
  return path.nodes.has(from) && path.nodes.has(to) ? 'lit' : 'dimmed';
}

export function nodeModifiers(visual: NodeVisual): string[] {
  const out: string[] = [];
  if (visual.mode) out.push(`hp-node--${visual.mode}`);
  if (visual.tint) out.push(`hp-node--tint-${visual.tint}`);
  return out;
}
