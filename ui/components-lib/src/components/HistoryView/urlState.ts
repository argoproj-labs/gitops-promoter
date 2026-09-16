import type { HistoryViewState } from '@shared/utils/deepLink';
import type { FilterId, SortId } from './types';

export interface CellSelection {
  rowId: string;
  branch: string;
}

export interface HistoryUrlState {
  selection: CellSelection | null;
  viewState: HistoryViewState;
}

export const initialUrlState = (
  selection: CellSelection | null,
  viewState: Partial<HistoryViewState> | undefined,
): HistoryUrlState => ({
  selection,
  viewState: {
    filter: viewState?.filter ?? 'all',
    sort: viewState?.sort ?? 'newest',
    envFilter: viewState?.envFilter ?? [],
  },
});

export const sameUrlState = (a: HistoryUrlState, b: HistoryUrlState): boolean =>
  a.selection?.rowId === b.selection?.rowId &&
  a.selection?.branch === b.selection?.branch &&
  a.viewState.filter === b.viewState.filter &&
  a.viewState.sort === b.viewState.sort &&
  a.viewState.envFilter.length === b.viewState.envFilter.length &&
  a.viewState.envFilter.every((branch, index) => branch === b.viewState.envFilter[index]);

export type UrlStateAction =
  | { type: 'setFilter'; filter: FilterId }
  | { type: 'setSort'; sort: SortId }
  | { type: 'setEnvFilter'; envFilter: string[] }
  | { type: 'setSelection'; selection: CellSelection | null }
  | { type: 'reset'; state: HistoryUrlState };

export const urlStateReducer = (
  state: HistoryUrlState,
  action: UrlStateAction,
): HistoryUrlState => {
  switch (action.type) {
    case 'setFilter':
      return { ...state, viewState: { ...state.viewState, filter: action.filter } };
    case 'setSort':
      return { ...state, viewState: { ...state.viewState, sort: action.sort } };
    case 'setEnvFilter':
      return { ...state, viewState: { ...state.viewState, envFilter: action.envFilter } };
    case 'setSelection':
      return { ...state, selection: action.selection };
    case 'reset':
      return action.state;
  }
};
