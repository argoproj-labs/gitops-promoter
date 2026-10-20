import { describe, expect, it } from 'vitest';
import {
  buildExplorerModel,
  deriveNodeStatus,
  orderEnvsByLayout,
  splitBranch,
  statusFromCellKind,
} from '@lib/components/PromotionExplorer/explorerModel';
import { buildMatrix } from '@lib/components/PromotionExplorer/buildMatrix';
import type { CellKind } from '@lib/components/PromotionExplorer/types';
import { dagStrategy, linearStrategy, makeStrategy, rowId } from './fixtures';

const B = (name: string) => `environments/${name}`;

describe('splitBranch', () => {
  it('splits the last path segment into kicker and title', () => {
    expect(splitBranch('environments/prod-us')).toEqual({
      kicker: 'environments',
      title: 'prod-us',
    });
    expect(splitBranch('a/b/c')).toEqual({ kicker: 'a/b', title: 'c' });
  });

  it('has no kicker for a bare branch', () => {
    expect(splitBranch('main')).toEqual({ kicker: null, title: 'main' });
  });
});

describe('statusFromCellKind', () => {
  it.each<[CellKind, ReturnType<typeof statusFromCellKind>]>([
    ['live', 'live'],
    ['in-flight', 'proposed'],
    ['failed', 'failed'],
    ['was-here', 'previously-live'],
    ['restored', 'previously-live'],
    ['no-op', 'previously-live'],
    ['no-changes', null],
    ['unknown-history', null],
  ])('%s maps to %s', (kind, expected) => {
    expect(statusFromCellKind(kind)).toBe(expected);
  });
});

describe('deriveNodeStatus', () => {
  const rowsOf = (envs: Parameters<typeof makeStrategy>[0]) => buildMatrix(makeStrategy(envs)).rows;

  it('reports live with the live dry hash', () => {
    const rows = rowsOf([{ branch: 'a', active: '4', history: ['4', '3'] }]);
    expect(deriveNodeStatus('a', rows)).toEqual({
      status: 'live',
      hash: '4444444',
      rowId: rowId('4'),
    });
  });

  it('reports proposed ahead of live', () => {
    const rows = rowsOf([{ branch: 'a', active: '3', proposed: '4', history: ['3'] }]);
    expect(deriveNodeStatus('a', rows)).toMatchObject({ status: 'proposed', hash: '4444444' });
  });

  it('reports failed when the proposed commit checks fail', () => {
    const rows = rowsOf([
      { branch: 'a', active: '3', proposed: '4', proposedPhase: 'failure', history: ['3'] },
    ]);
    expect(deriveNodeStatus('a', rows)).toMatchObject({ status: 'failed', hash: '4444444' });
  });

  it('reports failed when the live commit checks fail', () => {
    const rows = rowsOf([{ branch: 'a', active: '3', activePhase: 'failure', history: ['3'] }]);
    expect(deriveNodeStatus('a', rows)).toMatchObject({ status: 'failed', hash: '3333333' });
  });

  it('reports previously live when only superseded history remains', () => {
    const rows = rowsOf([{ branch: 'a', history: ['2', '1'] }]);
    expect(deriveNodeStatus('a', rows)).toMatchObject({
      status: 'previously-live',
      hash: '2222222',
    });
  });

  it('prefers a historical failure over previously live when nothing is live', () => {
    const rows = rowsOf([{ branch: 'a', history: ['2', '1'], failedHistory: ['1'] }]);
    expect(deriveNodeStatus('a', rows)).toMatchObject({ status: 'failed', hash: '1111111' });
  });

  it('keeps live when an older history entry failed', () => {
    const rows = rowsOf([{ branch: 'a', active: '3', history: ['3', '2'], failedHistory: ['2'] }]);
    expect(deriveNodeStatus('a', rows).status).toBe('live');
  });

  it('is not reached for a branch with no cells', () => {
    const rows = rowsOf([{ branch: 'a', active: '3', history: ['3'] }]);
    expect(deriveNodeStatus('missing', rows)).toEqual({ status: 'not-reached' });
  });
});

describe('buildExplorerModel (linear strategy)', () => {
  const model = buildExplorerModel(linearStrategy());

  it('builds a simple chain from spec order', () => {
    expect(model.graph.inferredLinear).toBe(true);
    expect(model.edges.map((e) => e.id)).toEqual([
      `${B('dev')}->${B('staging')}`,
      `${B('staging')}->${B('prod')}`,
    ]);
    expect(model.layout.layers).toEqual([[B('dev')], [B('staging')], [B('prod')]]);
  });

  it('derives a status for every node', () => {
    expect(model.nodes.map((n) => [n.title, n.status, n.hash])).toEqual([
      ['dev', 'live', '4444444'],
      ['staging', 'proposed', '4444444'],
      ['prod', 'live', '2222222'],
    ]);
  });

  it('positions nodes left to right on one row', () => {
    const ys = new Set(model.nodes.map((n) => n.y));
    expect(ys.size).toBe(1);
    const xs = model.nodes.map((n) => n.x);
    expect([...xs].sort((a, b) => a - b)).toEqual(xs);
  });

  it('emits a bezier path per edge', () => {
    for (const edge of model.edges) expect(edge.d).toMatch(/^M[\d.]+ [\d.]+ C/);
  });

  it('marks every node as having a grid column', () => {
    expect(model.nodes.every((n) => n.hasColumn)).toBe(true);
    expect(model.envs.map((e) => e.branch)).toEqual([B('dev'), B('staging'), B('prod')]);
  });
});

describe('buildExplorerModel (DAG strategy)', () => {
  const model = buildExplorerModel(dagStrategy());
  const node = (name: string) => model.nodeById.get(B(name))!;

  it('uses dependsOn for edges, including fan-in', () => {
    expect(model.graph.inferredLinear).toBe(false);
    expect(model.edges).toHaveLength(8);
    expect(model.graph.parents[B('prod-eu')].sort()).toEqual(
      [B('e2e-apac'), B('e2e-eu'), B('e2e-us')].sort(),
    );
    expect(model.graph.children[B('dev')]).toHaveLength(3);
  });

  it('lays out three layers', () => {
    expect(model.layout.layers.map((l) => l.length)).toEqual([1, 3, 3]);
    expect(node('dev').x).toBeLessThan(node('e2e-us').x);
    expect(node('e2e-us').x).toBeLessThan(node('prod-us').x);
  });

  it('derives node statuses from the per-environment data', () => {
    expect(node('dev').status).toBe('live');
    expect(node('e2e-us').status).toBe('proposed');
    expect(node('e2e-eu').status).toBe('proposed');
    expect(node('e2e-apac').status).toBe('not-reached');
    expect(node('prod-us').status).toBe('live');
    expect(node('prod-eu').status).toBe('failed');
    expect(node('prod-apac').status).toBe('not-reached');
  });

  it('keeps nodes for environments without history and flags them as column-less', () => {
    expect(node('e2e-apac').hasColumn).toBe(false);
    expect(node('e2e-apac').hash).toBeUndefined();
    expect(node('dev').hasColumn).toBe(true);
  });

  it('orders grid columns topologically', () => {
    const order = model.envs.map((e) => e.branch);
    expect(order).toEqual(expect.arrayContaining([B('dev'), B('e2e-us'), B('prod-eu')]));
    expect(order.indexOf(B('dev'))).toBe(0);
    expect(order.indexOf(B('e2e-us'))).toBeLessThan(order.indexOf(B('prod-us')));
    expect(order.indexOf(B('e2e-eu'))).toBeLessThan(order.indexOf(B('prod-eu')));
  });

  it('exposes the node shortcut hash and row id for linking', () => {
    expect(node('prod-us').rowId).toBe(rowId('2'));
  });
});

describe('buildExplorerModel (edge cases)', () => {
  it('synthesizes a linear graph from status when spec has no environments', () => {
    const strategy = linearStrategy();
    (strategy.spec as { environments?: unknown }).environments = [];
    const model = buildExplorerModel(strategy);
    expect(model.nodes).toHaveLength(3);
    expect(model.edges).toHaveLength(2);
  });

  it('appends status-only environments after graph nodes', () => {
    const strategy = linearStrategy();
    (strategy.spec as { environments: unknown[] }).environments = [{ branch: B('dev') }];
    const model = buildExplorerModel(strategy);
    expect(model.nodes).toHaveLength(1);
    expect(model.envs.map((e) => e.branch)[0]).toBe(B('dev'));
    expect(model.envs).toHaveLength(3);
  });

  it('handles a single environment', () => {
    const model = buildExplorerModel(
      makeStrategy([{ branch: 'solo', active: '1', history: ['1'] }]),
    );
    expect(model.nodes).toHaveLength(1);
    expect(model.edges).toHaveLength(0);
  });
});

describe('orderEnvsByLayout', () => {
  it('sorts columns by graph layer even when status order differs', () => {
    const strategy = linearStrategy();
    strategy.status!.environments = [...strategy.status!.environments!].reverse();
    const model = buildExplorerModel(strategy);
    expect(model.envs.map((e) => e.branch)).toEqual([B('dev'), B('staging'), B('prod')]);
  });

  it('keeps unknown environments last and stable', () => {
    const envs = [{ branch: 'x' }, { branch: 'a' }, { branch: 'y' }] as never[];
    const ordered = orderEnvsByLayout(envs, { layers: [['a']] });
    expect(ordered.map((e: { branch: string }) => e.branch)).toEqual(['a', 'x', 'y']);
  });
});
