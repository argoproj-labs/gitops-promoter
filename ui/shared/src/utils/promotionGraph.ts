export const NODE_WIDTH = 154;
export const NODE_HEIGHT = 60;
export const COLUMN_GAP = 72;
export const ROW_GAP = 24;
export const CANVAS_PADDING = 16;

export interface PromotionGraphSource {
  spec?: {
    environments?: ReadonlyArray<{ branch: string; dependsOn?: readonly string[] }>;
  };
}

export interface GraphNode {
  id: string;
  branch: string;
  index: number;
}

export interface GraphEdge {
  id: string;
  from: string;
  to: string;
}

export interface PromotionGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  parents: Record<string, string[]>;
  children: Record<string, string[]>;
  inferredLinear: boolean;
}

export interface PositionedNode extends GraphNode {
  x: number;
  y: number;
  width: number;
  height: number;
  layer: number;
}

export interface GraphLayout {
  nodes: PositionedNode[];
  layers: string[][];
  width: number;
  height: number;
}

export interface LayoutOptions {
  nodeWidth?: number;
  nodeHeight?: number;
  columnGap?: number;
  rowGap?: number;
  padding?: number;
  sweeps?: number;
}

export interface Point {
  x: number;
  y: number;
}

export function buildPromotionGraph(source: PromotionGraphSource): PromotionGraph {
  const specEnvs = source.spec?.environments ?? [];
  const seen = new Set<string>();
  const envs: { branch: string; dependsOn?: readonly string[] }[] = [];
  for (const env of specEnvs) {
    if (seen.has(env.branch)) continue;
    seen.add(env.branch);
    envs.push(env);
  }

  const nodes: GraphNode[] = envs.map((env, index) => ({
    id: env.branch,
    branch: env.branch,
    index,
  }));
  const parents: Record<string, string[]> = {};
  const children: Record<string, string[]> = {};
  for (const node of nodes) {
    parents[node.id] = [];
    children[node.id] = [];
  }

  const anyDeclared = envs.some((env) => (env.dependsOn?.length ?? 0) > 0);
  const edges: GraphEdge[] = [];
  const addEdge = (from: string, to: string) => {
    if (from === to || !seen.has(from) || !seen.has(to) || parents[to].includes(from)) return;
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

  return { nodes, edges, parents, children, inferredLinear: !anyDeclared };
}

export function parentsOf(graph: PromotionGraph, id: string): string[] {
  return graph.parents[id] ?? [];
}

export function childrenOf(graph: PromotionGraph, id: string): string[] {
  return graph.children[id] ?? [];
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

export function ancestors(graph: PromotionGraph, id: string): Set<string> {
  return walk(id, graph.parents);
}

export function descendants(graph: PromotionGraph, id: string): Set<string> {
  return walk(id, graph.children);
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
  for (const node of graph.nodes) visit(node.id);
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
  for (const node of graph.nodes) layers[depth.get(node.id)!].push(node.id);

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
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
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
