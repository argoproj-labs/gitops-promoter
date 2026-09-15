import { FILTER_IDS, SORT_IDS, type FilterId, type SortId } from '../types/historyView';

/** URL-addressable view state of the history matrix. */
export interface HistoryViewState {
  filter: FilterId;
  sort: SortId;
  envFilter: string[];
}

/** The state a URL with no history params means. Never serialized. */
export const DEFAULT_HISTORY_VIEW_STATE: HistoryViewState = {
  filter: 'all',
  sort: 'newest',
  envFilter: [],
};

/** Query param names for {@link HistoryViewState}; they differ per host. */
export interface HistoryParamNames {
  filter: string;
  sort: string;
  envs: string;
}

export const DASHBOARD_HISTORY_PARAMS: HistoryParamNames = {
  filter: 'filter',
  sort: 'sort',
  envs: 'envs',
};

/** Extension params are `ps`-prefixed to avoid colliding with ArgoCD's own. */
export const EXTENSION_HISTORY_PARAMS: HistoryParamNames = {
  filter: 'psFilter',
  sort: 'psSort',
  envs: 'psEnvs',
};

/** A selected history cell. Structurally matches `CellSelection`. */
export interface SelectionState {
  rowId: string;
  branch: string;
}

/** Query param names for {@link SelectionState}; they differ per host. */
export interface SelectionParamNames {
  commit: string;
  env: string;
}

export const DASHBOARD_SELECTION_PARAMS: SelectionParamNames = {
  commit: 'commit',
  env: 'env',
};

export const EXTENSION_SELECTION_PARAMS: SelectionParamNames = {
  commit: 'psCommit',
  env: 'psEnv',
};

/**
 * Read the history view state out of a query string. Unknown or malformed
 * values fall back to their default silently. Branch names in `envs` cannot be
 * validated here (the codec does not know the strategy's branches); that
 * happens in `HistoryView`.
 */
export function readHistoryViewState(
  params: URLSearchParams,
  names: HistoryParamNames,
): HistoryViewState {
  const filter = params.get(names.filter);
  const sort = params.get(names.sort);
  const envs = params.get(names.envs);

  return {
    filter: FILTER_IDS.includes(filter as FilterId)
      ? (filter as FilterId)
      : DEFAULT_HISTORY_VIEW_STATE.filter,
    sort: SORT_IDS.includes(sort as SortId) ? (sort as SortId) : DEFAULT_HISTORY_VIEW_STATE.sort,
    envFilter: envs ? envs.split(',').filter(Boolean) : [],
  };
}

/**
 * Serialize the history view state onto a copy of `params`, leaving unrelated
 * params untouched. Defaults are deleted rather than written, so a URL carrying
 * no history params means all defaults. Does not mutate its input.
 */
export function writeHistoryViewState(
  params: URLSearchParams,
  names: HistoryParamNames,
  state: HistoryViewState,
): URLSearchParams {
  const next = new URLSearchParams(params);

  if (state.filter !== DEFAULT_HISTORY_VIEW_STATE.filter) {
    next.set(names.filter, state.filter);
  } else {
    next.delete(names.filter);
  }

  if (state.sort !== DEFAULT_HISTORY_VIEW_STATE.sort) {
    next.set(names.sort, state.sort);
  } else {
    next.delete(names.sort);
  }

  const envs = state.envFilter.filter(Boolean);
  if (envs.length) {
    next.set(names.envs, envs.join(','));
  } else {
    next.delete(names.envs);
  }

  return next;
}

/** Read a cell selection. A partial pair (one name present) reads as `null`. */
export function readSelection(
  params: URLSearchParams,
  names: SelectionParamNames,
): SelectionState | null {
  const rowId = params.get(names.commit);
  const branch = params.get(names.env);
  return rowId && branch ? { rowId, branch } : null;
}

/**
 * Serialize a cell selection onto a copy of `params`, leaving unrelated params
 * untouched. A `null` selection deletes both names. Does not mutate its input.
 */
export function writeSelection(
  params: URLSearchParams,
  names: SelectionParamNames,
  selection: SelectionState | null,
): URLSearchParams {
  const next = new URLSearchParams(params);

  if (selection && selection.rowId && selection.branch) {
    next.set(names.commit, selection.rowId);
    next.set(names.env, selection.branch);
  } else {
    next.delete(names.commit);
    next.delete(names.env);
  }

  return next;
}
