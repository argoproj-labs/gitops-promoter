/**
 * Builds and lays out the promotion graph of a PromotionStrategy's environments.
 *
 * Invariants relied on, all enforced by k8s validation on the PromotionStrategy resource:
 * - environment branch names are unique, so a branch is a node's identity
 * - an environment never depends on itself
 * - a single dependsOn list has no duplicate entries
 *
 * Not validated, so handled here: dependsOn entries naming a branch that is not declared in the
 * environments list. Those edges are dropped.
 */
const NODE_WIDTH = 154;
const NODE_HEIGHT = 60;
const COLUMN_GAP = 72;
const ROW_GAP = 24;
const CANVAS_PADDING = 16;

/** The subset of a PromotionStrategy needed to build the graph. */
export interface PromotionGraphSource {
  spec?: {
    environments?: ReadonlyArray<{ branch: string; dependsOn?: readonly string[] }>;
  };
}

/** A single environment. `branch` is the node's unique identity. */
export interface GraphNode {
  branch: string;
  index: number;
}

/** A promotion dependency: `from` must be promoted before `to`. */
export interface GraphEdge {
  id: string;
  from: string;
  to: string;
}

/** Nodes in spec order, plus edges and branch-keyed parent/child adjacency lists. */
export interface PromotionGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  parents: Record<string, string[]>;
  children: Record<string, string[]>;
}

/** A node with its computed canvas position and layer (column) index. */
export interface PositionedNode extends GraphNode {
  x: number;
  y: number;
  width: number;
  height: number;
  layer: number;
}

/** Positioned nodes in spec order, the branches in each layer, and the canvas size. */
export interface GraphLayout {
  nodes: PositionedNode[];
  layers: string[][];
  width: number;
  height: number;
}

/** Overrides for the default layout dimensions; `sweeps` is the number of ordering passes. */
export interface LayoutOptions {
  nodeWidth?: number;
  nodeHeight?: number;
  columnGap?: number;
  rowGap?: number;
  padding?: number;
  sweeps?: number;
}

/** A canvas coordinate. */
export interface Point {
  x: number;
  y: number;
}

/**
 * Builds the graph from the declared environments. If any environment declares dependsOn, those
 * declarations are the edges (environments without one are roots); otherwise the environments
 * are chained in spec order. Edges to undeclared branches are dropped.
 */
export function buildPromotionGraph(source: PromotionGraphSource): PromotionGraph {
  const envs = source.spec?.environments ?? [];
  const seen = new Set(envs.map((env) => env.branch));

  const nodes: GraphNode[] = envs.map((env, index) => ({
    branch: env.branch,
    index,
  }));
  const parents: Record<string, string[]> = {};
  const children: Record<string, string[]> = {};
  for (const node of nodes) {
    parents[node.branch] = [];
    children[node.branch] = [];
  }

  const anyDeclared = envs.some((env) => (env.dependsOn?.length ?? 0) > 0);
  const edges: GraphEdge[] = [];
  const addEdge = (from: string, to: string) => {
    if (!seen.has(from)) return;
    parents[to].push(from);
    children[from].push(to);
    edges.push({ id: `${from}->${to}`, from, to });
  };

  if (anyDeclared) {
    for (const env of envs) {
      for (const parent of env.dependsOn ?? []) addEdge(parent, env.branch);
    }
  } else {
    for (let i = 1; i < envs.length; i++) addEdge(envs[i - 1].branch, envs[i].branch);
  }

  return { nodes, edges, parents, children };
}

function walk(start: string, next: Record<string, string[]>): Set<string> {
  const visited = new Set<string>();
  const stack = [...(next[start] ?? [])];
  while (stack.length) {
    const at = stack.pop()!;
    if (visited.has(at)) continue;
    visited.add(at);
    for (const step of next[at] ?? []) stack.push(step);
  }
  visited.delete(start);
  return visited;
}

/** All transitive parents of a branch, excluding the branch itself. Terminates on cycles. */
export function ancestors(graph: PromotionGraph, id: string): Set<string> {
  return walk(id, graph.parents);
}

function computeLayers(graph: PromotionGraph): Map<string, number> {
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const visit = (id: string): number => {
    const known = depth.get(id);
    if (known !== undefined) return known;
    if (visiting.has(id)) return 0;
    visiting.add(id);
    let d = 0;
    for (const parent of graph.parents[id] ?? []) {
      if (visiting.has(parent)) continue;
      d = Math.max(d, visit(parent) + 1);
    }
    visiting.delete(id);
    depth.set(id, d);
    return d;
  };
  for (const node of graph.nodes) visit(node.branch);
  return depth;
}

function barycenter(ids: string[], position: Map<string, number>): number | undefined {
  const known = ids.filter((id) => position.has(id));
  if (known.length === 0) return undefined;
  return known.reduce((sum, id) => sum + position.get(id)!, 0) / known.length;
}

function reorder(
  layer: string[],
  neighborsOfId: Record<string, string[]>,
  position: Map<string, number>,
): string[] {
  const keyed = layer.map((id, order) => ({
    id,
    order,
    bary: barycenter(neighborsOfId[id] ?? [], position),
  }));
  const fixed = keyed.filter((k) => k.bary === undefined);
  const movable = keyed
    .filter((k) => k.bary !== undefined)
    .sort((a, b) => a.bary! - b.bary! || a.order - b.order);
  const result: (string | undefined)[] = new Array(layer.length).fill(undefined);
  for (const k of fixed) result[k.order] = k.id;
  let m = 0;
  for (let i = 0; i < result.length; i++) {
    if (result[i] === undefined) result[i] = movable[m++].id;
  }
  return result as string[];
}

/**
 * Layered left-to-right layout: each node sits one column right of its deepest parent, and
 * nodes within a column are reordered by barycenter to reduce edge crossings.
 */
export function layoutPromotionGraph(
  graph: PromotionGraph,
  options: LayoutOptions = {},
): GraphLayout {
  const nodeWidth = options.nodeWidth ?? NODE_WIDTH;
  const nodeHeight = options.nodeHeight ?? NODE_HEIGHT;
  const columnGap = options.columnGap ?? COLUMN_GAP;
  const rowGap = options.rowGap ?? ROW_GAP;
  const padding = options.padding ?? CANVAS_PADDING;
  const sweeps = options.sweeps ?? 4;

  if (graph.nodes.length === 0) {
    return { nodes: [], layers: [], width: padding * 2, height: padding * 2 };
  }

  const depth = computeLayers(graph);
  const layerCount = Math.max(...depth.values()) + 1;
  const layers: string[][] = Array.from({ length: layerCount }, () => []);
  for (const node of graph.nodes) layers[depth.get(node.branch)!].push(node.branch);

  const positionOf = (layer: string[]) => new Map(layer.map((id, i) => [id, i]));

  for (let s = 0; s < sweeps; s++) {
    for (let l = 1; l < layerCount; l++) {
      layers[l] = reorder(layers[l], graph.parents, positionOf(layers[l - 1]));
    }
    for (let l = layerCount - 2; l >= 0; l--) {
      layers[l] = reorder(layers[l], graph.children, positionOf(layers[l + 1]));
    }
  }

  const tallest = Math.max(...layers.map((layer) => layer.length));
  const innerHeight = tallest * nodeHeight + (tallest - 1) * rowGap;
  const byId = new Map(graph.nodes.map((n) => [n.branch, n]));
  const positioned: PositionedNode[] = [];
  layers.forEach((layer, l) => {
    const layerHeight = layer.length * nodeHeight + (layer.length - 1) * rowGap;
    const top = padding + (innerHeight - layerHeight) / 2;
    layer.forEach((id, i) => {
      positioned.push({
        ...byId.get(id)!,
        x: padding + l * (nodeWidth + columnGap),
        y: top + i * (nodeHeight + rowGap),
        width: nodeWidth,
        height: nodeHeight,
        layer: l,
      });
    });
  });
  positioned.sort((a, b) => a.index - b.index);

  return {
    nodes: positioned,
    layers,
    width: padding * 2 + layerCount * nodeWidth + (layerCount - 1) * columnGap,
    height: padding * 2 + innerHeight,
  };
}

/** SVG cubic bezier path from a parent node's right-middle to a child node's left-middle. */
export function edgePath(
  from: Point,
  to: Point,
  nodeWidth: number = NODE_WIDTH,
  nodeHeight: number = NODE_HEIGHT,
): string {
  const x1 = from.x + nodeWidth;
  const y1 = from.y + nodeHeight / 2;
  const x2 = to.x;
  const y2 = to.y + nodeHeight / 2;
  const mid = (x1 + x2) / 2;
  return `M${x1} ${y1} C${mid} ${y1} ${mid} ${y2} ${x2} ${y2}`;
}
