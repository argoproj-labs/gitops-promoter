import { FILTER_IDS, SORT_IDS, type FilterId, type SortId } from '../types/historyView';

export interface HistoryViewState {
  filter: FilterId;
  sort: SortId;
  envFilter: string[];
}

export const DEFAULT_HISTORY_VIEW_STATE: HistoryViewState = {
  filter: 'all',
  sort: 'newest',
  envFilter: [],
};

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

export const EXTENSION_HISTORY_PARAMS: HistoryParamNames = {
  filter: 'psFilter',
  sort: 'psSort',
  envs: 'psEnvs',
};

export interface SelectionState {
  rowId: string;
  branch: string;
}

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

export function readHistoryViewState(
  params: URLSearchParams,
  names: HistoryParamNames,
): HistoryViewState {
  const filter = params.get(names.filter);
  const sort = params.get(names.sort);

  return {
    filter: FILTER_IDS.includes(filter as FilterId)
      ? (filter as FilterId)
      : DEFAULT_HISTORY_VIEW_STATE.filter,
    sort: SORT_IDS.includes(sort as SortId) ? (sort as SortId) : DEFAULT_HISTORY_VIEW_STATE.sort,
    envFilter: params.getAll(names.envs).filter(Boolean),
  };
}

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

  next.delete(names.envs);
  for (const branch of state.envFilter.filter(Boolean)) {
    next.append(names.envs, branch);
  }

  return next;
}

export function readSelection(
  params: URLSearchParams,
  names: SelectionParamNames,
): SelectionState | null {
  const rowId = params.get(names.commit);
  const branch = params.get(names.env);
  return rowId && branch ? { rowId, branch } : null;
}

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
