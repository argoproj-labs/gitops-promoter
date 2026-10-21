import { describe, it, expect } from 'vitest';
import {
  ancestors,
  buildPromotionGraph,
  childrenOf,
  descendants,
  edgePath,
  layoutPromotionGraph,
  parentsOf,
  NODE_HEIGHT,
  NODE_WIDTH,
} from './promotionGraph';
import type { PromotionGraphSource, PositionedNode } from './promotionGraph';

function strategy(envs: { branch: string; dependsOn?: string[] }[]): PromotionGraphSource {
  return { spec: { environments: envs } };
}

const dag = strategy([
  { branch: 'dev' },
  { branch: 'e2e-1', dependsOn: ['dev'] },
  { branch: 'e2e-2', dependsOn: ['dev'] },
  { branch: 'e2e-3', dependsOn: ['dev'] },
  { branch: 'prod-us', dependsOn: ['e2e-1'] },
  { branch: 'prod-eu', dependsOn: ['e2e-1', 'e2e-2', 'e2e-3'] },
  { branch: 'prod-apac', dependsOn: ['e2e-3'] },
]);

function overlaps(a: PositionedNode, b: PositionedNode): boolean {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

describe('buildPromotionGraph', () => {
  it('returns an empty graph for a strategy with no environments', () => {
    const g = buildPromotionGraph({});
    expect(g.nodes).toEqual([]);
    expect(g.edges).toEqual([]);
  });

  it('infers a linear chain from spec order when no environment declares dependsOn', () => {
    const g = buildPromotionGraph(strategy([{ branch: 'a' }, { branch: 'b' }, { branch: 'c' }]));
    expect(g.inferredLinear).toBe(true);
    expect(g.nodes.map((n) => n.id)).toEqual(['a', 'b', 'c']);
    expect(g.edges.map((e) => [e.from, e.to])).toEqual([
      ['a', 'b'],
      ['b', 'c'],
    ]);
  });

  it('has no edges for a single environment', () => {
    const g = buildPromotionGraph(strategy([{ branch: 'a' }]));
    expect(g.edges).toEqual([]);
  });

  it('treats empty dependsOn arrays as undeclared for the linear fallback', () => {
    const g = buildPromotionGraph(strategy([{ branch: 'a', dependsOn: [] }, { branch: 'b' }]));
    expect(g.inferredLinear).toBe(true);
    expect(g.edges).toHaveLength(1);
  });

  it('uses explicit dependsOn and makes undeclared environments roots once any is declared', () => {
    const g = buildPromotionGraph(
      strategy([{ branch: 'a' }, { branch: 'b' }, { branch: 'c', dependsOn: ['a', 'b'] }]),
    );
    expect(g.inferredLinear).toBe(false);
    expect(parentsOf(g, 'b')).toEqual([]);
    expect(parentsOf(g, 'c')).toEqual(['a', 'b']);
  });

  it('builds fan-out and fan-in edges', () => {
    const g = buildPromotionGraph(dag);
    expect(childrenOf(g, 'dev')).toEqual(['e2e-1', 'e2e-2', 'e2e-3']);
    expect(parentsOf(g, 'prod-eu')).toEqual(['e2e-1', 'e2e-2', 'e2e-3']);
    expect(g.edges).toHaveLength(3 + 1 + 3 + 1);
  });

  it('ignores unknown parents, self references, and duplicate entries', () => {
    const g = buildPromotionGraph(
      strategy([
        { branch: 'a' },
        { branch: 'b', dependsOn: ['a', 'a', 'b', 'ghost'] },
        { branch: 'a' },
      ]),
    );
    expect(g.nodes).toHaveLength(2);
    expect(g.edges.map((e) => e.id)).toEqual(['a->b']);
  });

  it('returns empty arrays for unknown ids', () => {
    const g = buildPromotionGraph(dag);
    expect(parentsOf(g, 'nope')).toEqual([]);
    expect(childrenOf(g, 'nope')).toEqual([]);
  });
});

describe('ancestors and descendants', () => {
  const g = buildPromotionGraph(dag);

  it('collects transitive ancestors without the node itself', () => {
    expect([...ancestors(g, 'prod-eu')].sort()).toEqual(['dev', 'e2e-1', 'e2e-2', 'e2e-3']);
    expect([...ancestors(g, 'prod-us')].sort()).toEqual(['dev', 'e2e-1']);
    expect(ancestors(g, 'dev').size).toBe(0);
  });

  it('collects transitive descendants', () => {
    expect([...descendants(g, 'dev')].sort()).toEqual([
      'e2e-1',
      'e2e-2',
      'e2e-3',
      'prod-apac',
      'prod-eu',
      'prod-us',
    ]);
    expect([...descendants(g, 'e2e-2')]).toEqual(['prod-eu']);
  });

  it('terminates on cyclic input', () => {
    const cyc = buildPromotionGraph(
      strategy([
        { branch: 'a', dependsOn: ['b'] },
        { branch: 'b', dependsOn: ['a'] },
      ]),
    );
    expect([...ancestors(cyc, 'a')]).toEqual(['b']);
    const layout = layoutPromotionGraph(cyc);
    expect(layout.nodes).toHaveLength(2);
  });
});

describe('layoutPromotionGraph', () => {
  it('returns a padded empty canvas for an empty graph', () => {
    const layout = layoutPromotionGraph(buildPromotionGraph({}));
    expect(layout.nodes).toEqual([]);
    expect(layout.width).toBeGreaterThan(0);
  });

  it('places a linear chain on one row in successive columns', () => {
    const g = buildPromotionGraph(strategy([{ branch: 'a' }, { branch: 'b' }, { branch: 'c' }]));
    const layout = layoutPromotionGraph(g);
    expect(layout.nodes.map((n) => n.layer)).toEqual([0, 1, 2]);
    expect(new Set(layout.nodes.map((n) => n.y)).size).toBe(1);
    expect(layout.nodes[1].x).toBeGreaterThan(layout.nodes[0].x + NODE_WIDTH);
    expect(layout.nodes[0].width).toBe(NODE_WIDTH);
    expect(layout.nodes[0].height).toBe(NODE_HEIGHT);
  });

  it('layers by longest path', () => {
    const layout = layoutPromotionGraph(buildPromotionGraph(dag));
    const layerOf = Object.fromEntries(layout.nodes.map((n) => [n.id, n.layer]));
    expect(layerOf).toEqual({
      dev: 0,
      'e2e-1': 1,
      'e2e-2': 1,
      'e2e-3': 1,
      'prod-us': 2,
      'prod-eu': 2,
      'prod-apac': 2,
    });
  });

  it('pushes a node below its deepest parent (longest path, not shortest)', () => {
    const g = buildPromotionGraph(
      strategy([
        { branch: 'a' },
        { branch: 'b', dependsOn: ['a'] },
        { branch: 'c', dependsOn: ['a', 'b'] },
      ]),
    );
    const layerOf = Object.fromEntries(layoutPromotionGraph(g).nodes.map((n) => [n.id, n.layer]));
    expect(layerOf.c).toBe(2);
  });

  it('never overlaps nodes and keeps them inside the canvas', () => {
    const layout = layoutPromotionGraph(buildPromotionGraph(dag));
    for (let i = 0; i < layout.nodes.length; i++) {
      const a = layout.nodes[i];
      expect(a.x).toBeGreaterThanOrEqual(0);
      expect(a.y).toBeGreaterThanOrEqual(0);
      expect(a.x + a.width).toBeLessThanOrEqual(layout.width);
      expect(a.y + a.height).toBeLessThanOrEqual(layout.height);
      for (let j = i + 1; j < layout.nodes.length; j++) {
        expect(overlaps(a, layout.nodes[j])).toBe(false);
      }
    }
  });

  it('orders layers to avoid a crossing that spec order would create', () => {
    const g = buildPromotionGraph(
      strategy([
        { branch: 'r1' },
        { branch: 'r2' },
        { branch: 'x', dependsOn: ['r2'] },
        { branch: 'y', dependsOn: ['r1'] },
      ]),
    );
    const layout = layoutPromotionGraph(g);
    const y = Object.fromEntries(layout.nodes.map((n) => [n.id, n.y]));
    expect(y.r1 < y.r2).toBe(y.y < y.x);
  });

  it('centers narrower layers vertically', () => {
    const layout = layoutPromotionGraph(buildPromotionGraph(dag));
    const dev = layout.nodes.find((n) => n.id === 'dev')!;
    const mid = layout.nodes.find((n) => n.id === 'e2e-2')!;
    expect(dev.y).toBeCloseTo(mid.y);
  });

  it('honors custom options', () => {
    const layout = layoutPromotionGraph(
      buildPromotionGraph(strategy([{ branch: 'a' }, { branch: 'b' }])),
      { nodeWidth: 100, nodeHeight: 40, columnGap: 10, padding: 0 },
    );
    expect(layout.width).toBe(210);
    expect(layout.height).toBe(40);
    expect(layout.nodes[1].x).toBe(110);
  });

  it('keeps nodes in spec order in the returned array', () => {
    const layout = layoutPromotionGraph(buildPromotionGraph(dag));
    expect(layout.nodes.map((n) => n.id)).toEqual(dag.spec!.environments!.map((e) => e.branch));
  });
});

describe('edgePath', () => {
  it('draws a cubic bezier from parent right-middle to child left-middle', () => {
    expect(edgePath({ x: 0, y: 0 }, { x: 226, y: 100 })).toBe(
      `M${NODE_WIDTH} ${NODE_HEIGHT / 2} C190 30 190 130 226 130`,
    );
  });

  it('supports custom node sizes', () => {
    expect(edgePath({ x: 10, y: 10 }, { x: 110, y: 10 }, 40, 20)).toBe(
      'M50 20 C80 20 80 20 110 20',
    );
  });
});
