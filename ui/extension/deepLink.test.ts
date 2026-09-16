import { describe, it, expect } from 'vitest';
import {
  DASHBOARD_HISTORY_PARAMS,
  DASHBOARD_SELECTION_PARAMS,
  DEFAULT_HISTORY_VIEW_STATE,
  EXTENSION_HISTORY_PARAMS,
  EXTENSION_SELECTION_PARAMS,
  readHistoryViewState,
  readSelection,
  writeHistoryViewState,
  writeSelection,
} from '@shared/utils/deepLink';
import type { HistoryViewState } from '@shared/utils/deepLink';

const nameSets = [
  ['dashboard', DASHBOARD_HISTORY_PARAMS],
  ['extension', EXTENSION_HISTORY_PARAMS],
] as const;

describe('readHistoryViewState', () => {
  it.each(nameSets)('returns all defaults for an empty query (%s)', (_host, names) => {
    expect(readHistoryViewState(new URLSearchParams(''), names)).toEqual(
      DEFAULT_HISTORY_VIEW_STATE,
    );
  });

  it.each(nameSets)('reads non-default filter, sort and envs (%s)', (_host, names) => {
    const params = new URLSearchParams();
    params.set(names.filter, 'failed');
    params.set(names.sort, 'oldest');
    params.append(names.envs, 'staging');
    params.append(names.envs, 'prod');
    expect(readHistoryViewState(params, names)).toEqual({
      filter: 'failed',
      sort: 'oldest',
      envFilter: ['staging', 'prod'],
    });
  });

  it('ignores the other host param names', () => {
    const params = new URLSearchParams({ psFilter: 'failed', psSort: 'oldest' });
    expect(readHistoryViewState(params, DASHBOARD_HISTORY_PARAMS)).toEqual(
      DEFAULT_HISTORY_VIEW_STATE,
    );
    expect(readHistoryViewState(params, EXTENSION_HISTORY_PARAMS)).toEqual({
      filter: 'failed',
      sort: 'oldest',
      envFilter: [],
    });
  });

  it.each(['bogus', '', 'ALL', 'in flight'])('falls back to filter=all for %o', (value) => {
    const params = new URLSearchParams({ filter: value });
    expect(readHistoryViewState(params, DASHBOARD_HISTORY_PARAMS).filter).toBe('all');
  });

  it.each(['bogus', '', 'NEWEST', 'desc'])('falls back to sort=newest for %o', (value) => {
    const params = new URLSearchParams({ sort: value });
    expect(readHistoryViewState(params, DASHBOARD_HISTORY_PARAMS).sort).toBe('newest');
  });

  it.each(['live', 'in-flight', 'failed', 'no-op', 'all'])('accepts filter=%s', (value) => {
    expect(
      readHistoryViewState(new URLSearchParams({ filter: value }), DASHBOARD_HISTORY_PARAMS).filter,
    ).toBe(value);
  });

  it.each(['newest', 'oldest'])('accepts sort=%s', (value) => {
    expect(
      readHistoryViewState(new URLSearchParams({ sort: value }), DASHBOARD_HISTORY_PARAMS).sort,
    ).toBe(value);
  });

  it('reads a single env', () => {
    const params = new URLSearchParams({ envs: 'prod' });
    expect(readHistoryViewState(params, DASHBOARD_HISTORY_PARAMS).envFilter).toEqual(['prod']);
  });

  it('drops empty env segments', () => {
    const params = new URLSearchParams('envs=&envs=staging&envs=&envs=prod');
    expect(readHistoryViewState(params, DASHBOARD_HISTORY_PARAMS).envFilter).toEqual([
      'staging',
      'prod',
    ]);
  });

  it('reads an absent envs param as no filter', () => {
    expect(
      readHistoryViewState(new URLSearchParams(''), DASHBOARD_HISTORY_PARAMS).envFilter,
    ).toEqual([]);
  });

  it('does not validate branch names', () => {
    const params = new URLSearchParams({ envs: 'no-such-branch' });
    expect(readHistoryViewState(params, DASHBOARD_HISTORY_PARAMS).envFilter).toEqual([
      'no-such-branch',
    ]);
  });

  it('round-trips a branch name containing a comma', () => {
    const params = new URLSearchParams('envs=env%2Fus%2Ceu');
    expect(readHistoryViewState(params, DASHBOARD_HISTORY_PARAMS).envFilter).toEqual(['env/us,eu']);
  });
});

describe('writeHistoryViewState', () => {
  it.each(nameSets)('omits every default (%s)', (_host, names) => {
    const next = writeHistoryViewState(new URLSearchParams(), names, DEFAULT_HISTORY_VIEW_STATE);
    expect(next.toString()).toBe('');
  });

  it.each(nameSets)('deletes params that fall back to their default (%s)', (_host, names) => {
    const params = new URLSearchParams({
      [names.filter]: 'failed',
      [names.sort]: 'oldest',
      [names.envs]: 'prod',
    });
    const next = writeHistoryViewState(params, names, DEFAULT_HISTORY_VIEW_STATE);
    expect(next.has(names.filter)).toBe(false);
    expect(next.has(names.sort)).toBe(false);
    expect(next.has(names.envs)).toBe(false);
  });

  it.each(nameSets)('writes non-defaults under the right names (%s)', (_host, names) => {
    const next = writeHistoryViewState(new URLSearchParams(), names, {
      filter: 'in-flight',
      sort: 'oldest',
      envFilter: ['staging', 'prod'],
    });
    expect(next.get(names.filter)).toBe('in-flight');
    expect(next.get(names.sort)).toBe('oldest');
    expect(next.getAll(names.envs)).toEqual(['staging', 'prod']);
  });

  it('writes the env filter as repeated params', () => {
    const next = writeHistoryViewState(new URLSearchParams(), DASHBOARD_HISTORY_PARAMS, {
      ...DEFAULT_HISTORY_VIEW_STATE,
      envFilter: ['a', 'b', 'c'],
    });
    expect(next.getAll('envs')).toEqual(['a', 'b', 'c']);
  });

  it('round-trips a branch name containing a comma', () => {
    const next = writeHistoryViewState(new URLSearchParams(), DASHBOARD_HISTORY_PARAMS, {
      ...DEFAULT_HISTORY_VIEW_STATE,
      envFilter: ['env/us,eu'],
    });
    expect(readHistoryViewState(next, DASHBOARD_HISTORY_PARAMS).envFilter).toEqual(['env/us,eu']);
  });

  it('drops empty env segments rather than writing them', () => {
    const next = writeHistoryViewState(new URLSearchParams(), DASHBOARD_HISTORY_PARAMS, {
      ...DEFAULT_HISTORY_VIEW_STATE,
      envFilter: ['', 'prod', ''],
    });
    expect(next.getAll('envs')).toEqual(['prod']);
  });

  it('omits envs entirely when every segment is empty', () => {
    const next = writeHistoryViewState(new URLSearchParams(), DASHBOARD_HISTORY_PARAMS, {
      ...DEFAULT_HISTORY_VIEW_STATE,
      envFilter: ['', ''],
    });
    expect(next.has('envs')).toBe(false);
  });

  it('preserves unrelated params, including the other host set', () => {
    const params = new URLSearchParams({
      mock: 'true',
      appName: 'guestbook',
      psFilter: 'failed',
      commit: 'abc',
    });
    const next = writeHistoryViewState(params, DASHBOARD_HISTORY_PARAMS, {
      filter: 'live',
      sort: 'newest',
      envFilter: [],
    });
    expect(next.get('mock')).toBe('true');
    expect(next.get('appName')).toBe('guestbook');
    expect(next.get('psFilter')).toBe('failed');
    expect(next.get('commit')).toBe('abc');
    expect(next.get('filter')).toBe('live');
  });

  it('does not mutate its input', () => {
    const params = new URLSearchParams({ filter: 'failed', mock: 'true' });
    const before = params.toString();
    writeHistoryViewState(params, DASHBOARD_HISTORY_PARAMS, {
      filter: 'no-op',
      sort: 'oldest',
      envFilter: ['prod'],
    });
    expect(params.toString()).toBe(before);
  });
});

describe('history view state round trip', () => {
  const states: HistoryViewState[] = [
    DEFAULT_HISTORY_VIEW_STATE,
    { filter: 'live', sort: 'newest', envFilter: [] },
    { filter: 'in-flight', sort: 'oldest', envFilter: ['prod'] },
    { filter: 'failed', sort: 'oldest', envFilter: ['staging', 'prod'] },
    { filter: 'no-op', sort: 'newest', envFilter: ['release/1.0'] },
    { filter: 'all', sort: 'oldest', envFilter: ['a', 'b', 'c'] },
  ];

  it.each(nameSets)('round-trips every state (%s)', (_host, names) => {
    for (const state of states) {
      const written = writeHistoryViewState(new URLSearchParams(), names, state);
      expect(readHistoryViewState(written, names)).toEqual(state);
      const reparsed = new URLSearchParams(written.toString());
      expect(readHistoryViewState(reparsed, names)).toEqual(state);
    }
  });
});

describe('readSelection', () => {
  it.each([
    ['dashboard', DASHBOARD_SELECTION_PARAMS],
    ['extension', EXTENSION_SELECTION_PARAMS],
  ] as const)('reads a complete pair (%s)', (_host, names) => {
    const params = new URLSearchParams({ [names.commit]: 'abc123', [names.env]: 'prod' });
    expect(readSelection(params, names)).toEqual({ rowId: 'abc123', branch: 'prod' });
  });

  it('returns null for an empty query', () => {
    expect(readSelection(new URLSearchParams(), DASHBOARD_SELECTION_PARAMS)).toBeNull();
  });

  it('returns null for a partial pair', () => {
    expect(
      readSelection(new URLSearchParams({ commit: 'abc123' }), DASHBOARD_SELECTION_PARAMS),
    ).toBeNull();
    expect(
      readSelection(new URLSearchParams({ env: 'prod' }), DASHBOARD_SELECTION_PARAMS),
    ).toBeNull();
  });

  it('returns null when either value is empty', () => {
    expect(
      readSelection(new URLSearchParams({ commit: '', env: 'prod' }), DASHBOARD_SELECTION_PARAMS),
    ).toBeNull();
    expect(
      readSelection(new URLSearchParams({ commit: 'abc123', env: '' }), DASHBOARD_SELECTION_PARAMS),
    ).toBeNull();
  });

  it('ignores the other host param names', () => {
    const params = new URLSearchParams({ psCommit: 'abc123', psEnv: 'prod' });
    expect(readSelection(params, DASHBOARD_SELECTION_PARAMS)).toBeNull();
    expect(readSelection(params, EXTENSION_SELECTION_PARAMS)).toEqual({
      rowId: 'abc123',
      branch: 'prod',
    });
  });
});

describe('writeSelection', () => {
  it.each([
    ['dashboard', DASHBOARD_SELECTION_PARAMS],
    ['extension', EXTENSION_SELECTION_PARAMS],
  ] as const)('writes both names and round-trips (%s)', (_host, names) => {
    const selection = { rowId: 'abc123', branch: 'release/1.0' };
    const next = writeSelection(new URLSearchParams(), names, selection);
    expect(next.get(names.commit)).toBe('abc123');
    expect(next.get(names.env)).toBe('release/1.0');
    expect(readSelection(new URLSearchParams(next.toString()), names)).toEqual(selection);
  });

  it('deletes both names for a null selection', () => {
    const params = new URLSearchParams({ commit: 'abc123', env: 'prod', mock: 'true' });
    const next = writeSelection(params, DASHBOARD_SELECTION_PARAMS, null);
    expect(next.has('commit')).toBe(false);
    expect(next.has('env')).toBe(false);
    expect(next.get('mock')).toBe('true');
  });

  it('deletes both names when the selection is partially empty', () => {
    const params = new URLSearchParams({ commit: 'abc123', env: 'prod' });
    expect(
      writeSelection(params, DASHBOARD_SELECTION_PARAMS, {
        rowId: 'abc123',
        branch: '',
      }).toString(),
    ).toBe('');
    expect(
      writeSelection(params, DASHBOARD_SELECTION_PARAMS, { rowId: '', branch: 'prod' }).toString(),
    ).toBe('');
  });

  it('preserves unrelated params', () => {
    const params = new URLSearchParams({ mock: 'true', filter: 'failed', psCommit: 'zzz' });
    const next = writeSelection(params, DASHBOARD_SELECTION_PARAMS, {
      rowId: 'abc123',
      branch: 'prod',
    });
    expect(next.get('mock')).toBe('true');
    expect(next.get('filter')).toBe('failed');
    expect(next.get('psCommit')).toBe('zzz');
  });

  it('does not mutate its input', () => {
    const params = new URLSearchParams({ commit: 'abc123', env: 'prod' });
    const before = params.toString();
    writeSelection(params, DASHBOARD_SELECTION_PARAMS, null);
    expect(params.toString()).toBe(before);
  });
});

describe('combined history params', () => {
  it('layers selection and view state without clobbering each other', () => {
    let params = new URLSearchParams({ mock: 'true' });
    params = writeSelection(params, EXTENSION_SELECTION_PARAMS, {
      rowId: 'abc123',
      branch: 'prod',
    });
    params = writeHistoryViewState(params, EXTENSION_HISTORY_PARAMS, {
      filter: 'failed',
      sort: 'oldest',
      envFilter: ['staging', 'prod'],
    });

    expect(readSelection(params, EXTENSION_SELECTION_PARAMS)).toEqual({
      rowId: 'abc123',
      branch: 'prod',
    });
    expect(readHistoryViewState(params, EXTENSION_HISTORY_PARAMS)).toEqual({
      filter: 'failed',
      sort: 'oldest',
      envFilter: ['staging', 'prod'],
    });
    expect(params.get('mock')).toBe('true');
  });

  it('clearing everything leaves only unrelated params', () => {
    let params = new URLSearchParams({
      mock: 'true',
      psCommit: 'abc123',
      psEnv: 'prod',
      psFilter: 'failed',
      psSort: 'oldest',
      psEnvs: 'staging',
    });
    params = writeSelection(params, EXTENSION_SELECTION_PARAMS, null);
    params = writeHistoryViewState(params, EXTENSION_HISTORY_PARAMS, DEFAULT_HISTORY_VIEW_STATE);
    expect(params.toString()).toBe('mock=true');
  });
});
