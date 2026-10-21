import { describe, expect, it } from 'vitest';
import { buildExplorerModel } from '@lib/components/PromotionExplorer/explorerModel';
import {
  buildCommitPath,
  edgeVisual,
  nodeModifiers,
  nodeVisual,
} from '@lib/components/PromotionExplorer/graphFocus';
import { dagStrategy, rowId } from './fixtures';

const B = (name: string) => `environments/${name}`;
const model = buildExplorerModel(dagStrategy());
const { graph, rows } = model;
const row = (c: string) => rows.find((r) => r.id === rowId(c))!;

describe('buildCommitPath', () => {
  it('lights touched nodes plus every ancestor, tinted by state', () => {
    const path = buildCommitPath(graph, row('4'))!;
    expect([...path.nodes].sort()).toEqual([B('dev'), B('e2e-eu'), B('e2e-us')].sort());
    expect(path.tints).toEqual({
      [B('dev')]: 'live',
      [B('e2e-us')]: 'proposed',
      [B('e2e-eu')]: 'proposed',
    });
  });

  it('pulls in ancestors that the commit never touched', () => {
    const path = buildCommitPath(graph, row('1'))!;
    expect(path.nodes.has(B('prod-eu'))).toBe(true);
    expect(path.nodes.has(B('e2e-apac'))).toBe(true);
    expect(path.tints[B('e2e-apac')]).toBeUndefined();
    expect(path.tints[B('prod-eu')]).toBe('failed');
  });

  it('returns null without a row', () => {
    expect(buildCommitPath(graph, undefined)).toBeNull();
  });

  it('returns null when no cell maps to a node', () => {
    expect(buildCommitPath({ ...graph, parents: {} }, row('4'))).toBeNull();
  });
});

describe('nodeVisual', () => {
  it('has no mode without a path', () => {
    expect(nodeVisual(B('dev'), null)).toEqual({ mode: null, tint: null });
  });

  it('tints nodes on a commit path and dims the rest', () => {
    const path = buildCommitPath(graph, row('4'));
    expect(nodeVisual(B('dev'), path)).toEqual({ mode: 'path', tint: 'live' });
    expect(nodeVisual(B('e2e-us'), path)).toEqual({ mode: 'path', tint: 'proposed' });
    expect(nodeVisual(B('prod-us'), path)).toEqual({ mode: 'dimmed', tint: null });
  });

  it('builds BEM modifier classes', () => {
    expect(nodeModifiers({ mode: 'path', tint: 'live' })).toEqual([
      'hp-node--path',
      'hp-node--tint-live',
    ]);
    expect(nodeModifiers({ mode: null, tint: null })).toEqual([]);
  });
});

describe('edgeVisual', () => {
  it('has no mode without a path', () => {
    expect(edgeVisual(B('dev'), B('e2e-us'), null)).toBeNull();
  });

  it('lights only edges whose both ends are on a commit path', () => {
    const path = buildCommitPath(graph, row('4'));
    expect(edgeVisual(B('dev'), B('e2e-us'), path)).toBe('lit');
    expect(edgeVisual(B('e2e-us'), B('prod-us'), path)).toBe('dimmed');
  });
});
