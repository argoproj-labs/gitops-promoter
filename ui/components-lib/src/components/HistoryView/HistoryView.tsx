import React, { useMemo, useState, useCallback, useEffect, useReducer, useRef } from 'react';
import { FaChevronLeft, FaFilter, FaSort, FaLayerGroup, FaArrowRight } from 'react-icons/fa';
import { GoGitCommit } from 'react-icons/go';
import { timeAgo, formatDate, getCommitUrl } from '@shared/utils/util';
import type { PromotionStrategy } from '@shared/types/promotion';
import type { HistoryViewState } from '@shared/utils/deepLink';
import { FILTER_IDS, SORT_IDS, type CommitRow, type FilterId, type SortId } from './types';
import { buildMatrix } from './buildMatrix';
import { isEmptyCellKind, pruneEnvFilter } from './helpers';
import { Dropdown, DropdownItem } from './Dropdown/Dropdown';
import Tooltip from './Tooltip/Tooltip';
import FlowCell from './FlowCell/FlowCell';
import DetailDrawer from './DetailDrawer/DetailDrawer';
import { useDrawerWidth } from './useDrawerWidth';
import { initialUrlState, sameUrlState, urlStateReducer } from './urlState';
import type { CellSelection, HistoryUrlState, UrlStateAction } from './urlState';
import './index.scss';

const scrollRowIntoView = (rowId: string) => {
  requestAnimationFrame(() => {
    document
      .getElementById(`row-${rowId}`)
      ?.scrollIntoView({ block: 'center', behavior: 'smooth' });
  });
};

const FILTER_LABELS: Record<FilterId, string> = {
  all: 'All commits',
  live: 'Live',
  'in-flight': 'In flight',
  failed: 'Failed',
  'no-op': 'No-op',
};
const FILTERS: { id: FilterId; label: string }[] = FILTER_IDS.map((id) => ({
  id,
  label: FILTER_LABELS[id],
}));

const SORT_LABELS: Record<SortId, string> = {
  newest: 'Newest first',
  oldest: 'Oldest first',
};
const SORTS: { id: SortId; label: string }[] = SORT_IDS.map((id) => ({
  id,
  label: SORT_LABELS[id],
}));

export type { CellSelection, HistoryUrlState };

export interface HistoryViewProps {
  strategy?: PromotionStrategy;
  name?: string;
  namespace?: string;
  onBack?: () => void;
  initialSelection?: CellSelection | null;
  initialViewState?: Partial<HistoryViewState>;
  onUrlStateChange?: (_state: HistoryUrlState) => void;
  fillViewport?: boolean;
}

const HistoryView: React.FC<HistoryViewProps> = ({
  strategy,
  name: nameProp,
  namespace: namespaceProp,
  onBack,
  fillViewport = false,
  initialSelection = null,
  initialViewState,
  onUrlStateChange,
}) => {
  const rootClass = fillViewport ? 'hp--viewport' : '';
  const name = nameProp ?? strategy?.metadata?.name;
  const namespace = namespaceProp ?? strategy?.metadata?.namespace;

  const { envs, rows } = useMemo(
    () => (strategy ? buildMatrix(strategy) : { envs: [], rows: [] }),
    [strategy],
  );
  const rowsById = useMemo(() => {
    const m = new Map<string, CommitRow>();
    for (const r of rows) m.set(r.id, r);
    return m;
  }, [rows]);

  const [urlState, dispatch] = useReducer(
    urlStateReducer,
    initialUrlState(initialSelection, initialViewState),
  );
  const { selection: selected, viewState } = urlState;
  const { filter, sort, envFilter } = viewState;
  const selectionFromLinkRef = useRef(initialSelection !== null);
  const pendingScrollRowIdRef = useRef<string | null>(null);

  const onUrlStateChangeRef = useRef(onUrlStateChange);
  onUrlStateChangeRef.current = onUrlStateChange;

  const urlStateRef = useRef(urlState);
  urlStateRef.current = urlState;

  const initialUrlStateRef = useRef(urlState);
  useEffect(() => {
    const next = initialUrlState(initialSelection, initialViewState);
    if (!sameUrlState(next, initialUrlStateRef.current)) {
      initialUrlStateRef.current = next;
      selectionFromLinkRef.current = next.selection !== null;
      dispatch({ type: 'reset', state: next });
      if (next.selection) {
        if (rows.length > 0) scrollRowIntoView(next.selection.rowId);
        else pendingScrollRowIdRef.current = next.selection.rowId;
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialSelection, initialViewState]);

  useEffect(() => {
    if (rows.length === 0 || !pendingScrollRowIdRef.current) return;
    scrollRowIntoView(pendingScrollRowIdRef.current);
    pendingScrollRowIdRef.current = null;
  }, [rows.length]);

  const dispatchAndNotify = useCallback((action: UrlStateAction) => {
    const next = urlStateReducer(urlStateRef.current, action);
    dispatch(action);
    urlStateRef.current = next;
    if (action.type !== 'reset') onUrlStateChangeRef.current?.(next);
  }, []);

  const setFilter = useCallback(
    (next: FilterId) => {
      dispatchAndNotify({ type: 'setFilter', filter: next });
    },
    [dispatchAndNotify],
  );

  const setSort = useCallback(
    (next: SortId) => {
      dispatchAndNotify({ type: 'setSort', sort: next });
    },
    [dispatchAndNotify],
  );

  const setEnvFilter = useCallback(
    (next: string[] | ((_prev: string[]) => string[])) => {
      const value =
        typeof next === 'function' ? next(urlStateRef.current.viewState.envFilter) : next;
      dispatchAndNotify({ type: 'setEnvFilter', envFilter: value });
    },
    [dispatchAndNotify],
  );

  const [staleLink, setStaleLink] = useState(false);

  const selectCell = useCallback(
    (next: CellSelection | null) => {
      selectionFromLinkRef.current = false;
      setStaleLink(false);
      dispatchAndNotify({ type: 'setSelection', selection: next });
    },
    [dispatchAndNotify],
  );

  const drawer = useDrawerWidth();

  const validBranches = useMemo(() => new Set(envs.map((e) => e.branch)), [envs]);

  const effectiveEnvFilter = useMemo(
    () => pruneEnvFilter(envFilter, validBranches) ?? envFilter,
    [envFilter, validBranches],
  );

  const handleToggleEnvFilter = useCallback((branch: string) => {
    setEnvFilter((prev) =>
      prev.includes(branch) ? prev.filter((b) => b !== branch) : [...prev, branch],
    );
  }, []);

  const envScopedRows = useMemo(
    () =>
      effectiveEnvFilter.length
        ? rows.filter((r) => effectiveEnvFilter.some((b) => !isEmptyCellKind(r.cells[b]?.kind)))
        : rows,
    [rows, effectiveEnvFilter],
  );

  const filteredRows = useMemo(() => {
    const apply = (r: CommitRow): boolean => {
      switch (filter) {
        case 'all':
          return true;
        case 'live':
          return r.hasLive;
        case 'in-flight':
          return r.hasInFlight;
        case 'failed':
          return r.hasFailed;
        case 'no-op':
          return r.hasNoop;
      }
    };
    const list = envScopedRows.filter(apply);
    if (sort === 'oldest') return [...list].sort((a, b) => a.freshestAt - b.freshestAt);
    return [...list].sort((a, b) => b.freshestAt - a.freshestAt);
  }, [envScopedRows, filter, sort]);

  const filteredRowIds = useMemo(() => new Set(filteredRows.map((r) => r.id)), [filteredRows]);

  const selectionIsStale = useMemo(() => {
    if (!selected || !strategy) return false;
    const selectedCell = rowsById.get(selected.rowId)?.cells[selected.branch];
    return (
      !validBranches.has(selected.branch) ||
      !selectedCell ||
      isEmptyCellKind(selectedCell.kind) ||
      (effectiveEnvFilter.length > 0 && !effectiveEnvFilter.includes(selected.branch)) ||
      !filteredRowIds.has(selected.rowId)
    );
  }, [selected, strategy, rowsById, validBranches, effectiveEnvFilter, filteredRowIds]);

  const effectiveSelected = selectionIsStale ? null : selected;

  useEffect(() => {
    if (!selectionIsStale) return;
    if (selectionFromLinkRef.current) setStaleLink(true);
    selectionFromLinkRef.current = false;
    dispatch({ type: 'setSelection', selection: null });
  }, [selectionIsStale]);

  const counts = useMemo(() => {
    return {
      all: envScopedRows.length,
      live: envScopedRows.filter((r) => r.hasLive).length,
      'in-flight': envScopedRows.filter((r) => r.hasInFlight).length,
      failed: envScopedRows.filter((r) => r.hasFailed).length,
      'no-op': envScopedRows.filter((r) => r.hasNoop).length,
    } satisfies Record<FilterId, number>;
  }, [envScopedRows]);

  const handleBack = onBack;

  const handleJumpToRow = useCallback(
    (rowId: string) => {
      const row = rowsById.get(rowId);
      if (!row) return;
      const branches = envs.map((e) => e.branch);
      const liveBranch = branches.find((b) => row.cells[b].kind === 'live');
      const branch = liveBranch ?? branches.find((b) => !isEmptyCellKind(row.cells[b].kind));
      if (branch) selectCell({ rowId, branch });
      scrollRowIntoView(rowId);
    },
    [envs, rowsById, selectCell],
  );

  if (!strategy) return <div className={`hp-loading ${rootClass}`}>Loading promotion history…</div>;

  if (envs.length === 0) {
    return (
      <div className={`hp-empty ${rootClass}`}>
        <h2 className="hp-empty__title">No promotion history yet</h2>
        <p className="hp-empty__body">
          This promotion strategy doesn't have any environments with history to show.
        </p>
        {handleBack && (
          <button onClick={handleBack} className="hp-empty__back">
            Back to {name}
          </button>
        )}
      </div>
    );
  }

  const selectedRow = effectiveSelected ? (rowsById.get(effectiveSelected.rowId) ?? null) : null;
  const selectedCell =
    selectedRow && effectiveSelected ? selectedRow.cells[effectiveSelected.branch] : null;
  const hasMultipleEnvs = envs.length > 1;

  const visibleEnvs = effectiveEnvFilter.length
    ? envs.filter((e) => effectiveEnvFilter.includes(e.branch))
    : envs;

  // Trailing 1fr spacer track (no cell placed in it) soaks up leftover width as
  // empty gap; when columns overflow it collapses to 0 and the matrix scrolls.
  const gridTemplate = `minmax(280px, 420px) repeat(${visibleEnvs.length}, minmax(180px, 260px)) 1fr`;

  return (
    <div className={`hp ${rootClass}`}>
      <header className="hp-header">
        {handleBack && (
          <button className="hp-header__back" onClick={handleBack} type="button">
            <FaChevronLeft aria-hidden="true" />
            <span>Back to {name}</span>
          </button>
        )}
        <div className="hp-header__title">
          <h1>{name}</h1>
          <span className="hp-header__subtitle">Promotion flow · {namespace}</span>
        </div>
        <div className="hp-header__spacer" />
        <div className="hp-controls">
          <Dropdown
            icon={<FaFilter />}
            label="Filter"
            active={filter !== 'all'}
            value={FILTERS.find((f) => f.id === filter)?.label ?? 'All commits'}
          >
            {(close) =>
              FILTERS.map((f) => (
                <DropdownItem
                  key={f.id}
                  selected={filter === f.id}
                  onSelect={() => {
                    setFilter(f.id);
                    close();
                  }}
                >
                  <span className="hp-dd__item-label">{f.label}</span>
                  <span className="hp-chip__count">{counts[f.id]}</span>
                </DropdownItem>
              ))
            }
          </Dropdown>

          {hasMultipleEnvs && (
            <Dropdown
              icon={<FaLayerGroup />}
              label="Environment"
              active={effectiveEnvFilter.length > 0}
              value={
                effectiveEnvFilter.length === 0 ? (
                  'All environments'
                ) : effectiveEnvFilter.length === 1 ? (
                  <>
                    <span
                      className="hp-chip__dot"
                      style={{
                        background: envs.find((e) => e.branch === effectiveEnvFilter[0])?.color,
                      }}
                      aria-hidden="true"
                    />
                    {effectiveEnvFilter[0]}
                  </>
                ) : (
                  `${effectiveEnvFilter.length} environments`
                )
              }
            >
              {() => (
                <>
                  <DropdownItem
                    multi
                    selected={effectiveEnvFilter.length === 0}
                    onSelect={() => setEnvFilter([])}
                  >
                    <span className="hp-dd__item-label">All environments</span>
                  </DropdownItem>
                  {envs.map((env) => {
                    const envCount = rows.filter(
                      (r) => !isEmptyCellKind(r.cells[env.branch]?.kind),
                    ).length;
                    return (
                      <DropdownItem
                        key={env.branch}
                        multi
                        selected={effectiveEnvFilter.includes(env.branch)}
                        onSelect={() => handleToggleEnvFilter(env.branch)}
                      >
                        <span
                          className="hp-chip__dot"
                          style={{ background: env.color }}
                          aria-hidden="true"
                        />
                        <span className="hp-dd__item-label">{env.branch}</span>
                        <span className="hp-chip__count">{envCount}</span>
                      </DropdownItem>
                    );
                  })}
                </>
              )}
            </Dropdown>
          )}

          <Dropdown
            icon={<FaSort />}
            label="Sort"
            active={sort !== 'newest'}
            value={SORTS.find((s) => s.id === sort)?.label ?? 'Newest first'}
          >
            {(close) =>
              SORTS.map((s) => (
                <DropdownItem
                  key={s.id}
                  selected={sort === s.id}
                  onSelect={() => {
                    setSort(s.id);
                    close();
                  }}
                >
                  <span className="hp-dd__item-label">{s.label}</span>
                </DropdownItem>
              ))
            }
          </Dropdown>
        </div>
      </header>

      <div className="hp-body">
        <div className="hp-main">
          <div className="hp-sr-only" role="status" aria-live="polite">
            {`${filteredRows.length} ${filteredRows.length === 1 ? 'commit' : 'commits'} shown`}
          </div>

          <div className="hp-matrix">
            <div className="hp-matrix__sticky">
              {staleLink && (
                <div className="hp-env-banner hp-stale-link" role="status">
                  <span className="hp-env-banner__text">
                    That commit is no longer in this promotion history.
                  </span>
                  <button
                    type="button"
                    className="hp-env-banner__clear"
                    onClick={() => setStaleLink(false)}
                  >
                    Dismiss
                  </button>
                </div>
              )}
              {effectiveEnvFilter.length > 0 && visibleEnvs.length < envs.length && (
                <div className="hp-env-banner">
                  <span className="hp-env-banner__text">
                    Showing {visibleEnvs.length} of {envs.length} environments
                  </span>
                  <button
                    type="button"
                    className="hp-env-banner__clear"
                    onClick={() => setEnvFilter([])}
                  >
                    Clear filter
                  </button>
                </div>
              )}
              <div className="hp-matrix__head" style={{ gridTemplateColumns: gridTemplate }}>
                <span className="hp-matrix__head-commit">
                  <span className="hp-matrix__head-label">Commit</span>
                  <span className="hp-matrix__head-promotes">
                    Promotes
                    <FaArrowRight className="hp-matrix__head-promotes-arrow" aria-hidden="true" />
                  </span>
                </span>
                {visibleEnvs.map((env, i) => (
                  <span key={env.branch} className="hp-matrix__head-env">
                    {i > 0 && (
                      <span className="hp-matrix__head-connector" aria-hidden="true">
                        <FaArrowRight />
                      </span>
                    )}
                    <span className="hp-matrix__head-pill">{env.branch}</span>
                  </span>
                ))}
              </div>
            </div>

            {filteredRows.length === 0 ? (
              <div className="hp-matrix__empty">
                No commits match these filters. Try adjusting them.
              </div>
            ) : (
              filteredRows.map((row) => (
                <div
                  key={row.id}
                  id={`row-${row.id}`}
                  className={`hp-row ${effectiveSelected?.rowId === row.id ? 'hp-row--selected' : ''}`}
                  style={{ gridTemplateColumns: gridTemplate }}
                >
                  <div className="hp-row__commit">
                    <div className="hp-row__subject" title={row.subject}>
                      {row.subject}
                    </div>
                    <div className="hp-row__meta">
                      {row.repoUrl && row.dryShaFull ? (
                        <a
                          className="hp-row__sha"
                          href={getCommitUrl(row.repoUrl, row.dryShaFull)}
                          target="_blank"
                          rel="noreferrer"
                          onClick={(e) => e.stopPropagation()}
                          aria-label={`Commit ${row.dryShaShort}, opens in new tab`}
                        >
                          {row.dryShaShort}
                        </a>
                      ) : (
                        <span className="hp-row__sha">{row.dryShaShort}</span>
                      )}
                      {row.refShaShort && row.refUrl && (
                        <Tooltip
                          label={
                            <>
                              Source commit <code>{row.refShaShort}</code>
                              <br />
                              Open on remote
                            </>
                          }
                        >
                          <a
                            className="hp-row__pr"
                            href={row.refUrl}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(e) => e.stopPropagation()}
                            aria-label={`Source commit ${row.refShaShort}, opens in new tab`}
                          >
                            <GoGitCommit aria-hidden="true" /> {row.refShaShort}
                          </a>
                        </Tooltip>
                      )}
                      <span className="hp-row__sep" aria-hidden="true">
                        ·
                      </span>
                      <Tooltip label={`Authored by ${row.author}`}>
                        <span className="hp-row__author-inline">{row.author}</span>
                      </Tooltip>
                      {row.freshestAt > 0 && (
                        <>
                          <span className="hp-row__sep">·</span>
                          <Tooltip
                            label={`Introduced ${formatDate(new Date(row.earliestAt || row.freshestAt).toISOString())}`}
                          >
                            <span className="hp-row__time-inline">
                              {timeAgo(new Date(row.earliestAt || row.freshestAt).toISOString())}
                            </span>
                          </Tooltip>
                        </>
                      )}
                    </div>
                  </div>
                  {visibleEnvs.map((env) => {
                    const isFocusedEnv = effectiveEnvFilter.includes(env.branch);
                    return (
                      <div
                        key={env.branch}
                        className={[
                          'hp-row__env-slot',
                          isFocusedEnv ? 'hp-row__env-slot--focus' : '',
                        ]
                          .filter(Boolean)
                          .join(' ')}
                      >
                        <FlowCell
                          cell={row.cells[env.branch]}
                          branch={env.branch}
                          isSelected={
                            effectiveSelected?.rowId === row.id &&
                            effectiveSelected?.branch === env.branch
                          }
                          onSelect={() => selectCell({ rowId: row.id, branch: env.branch })}
                          rowsById={rowsById}
                          onJumpToRow={handleJumpToRow}
                        />
                      </div>
                    );
                  })}
                </div>
              ))
            )}
          </div>
        </div>

        <DetailDrawer
          row={selectedRow}
          cell={selectedCell}
          branch={effectiveSelected?.branch ?? null}
          envs={envs}
          rowsById={rowsById}
          width={drawer.width}
          isResizing={drawer.isResizing}
          onResizeStart={drawer.onResizeStart}
          onResizeReset={drawer.onResizeReset}
          onResizeTo={drawer.onResizeTo}
          onClose={() => selectCell(null)}
          onJumpToRow={handleJumpToRow}
          onSelectCell={(branch) =>
            effectiveSelected && selectCell({ rowId: effectiveSelected.rowId, branch })
          }
        />
      </div>
    </div>
  );
};

export default HistoryView;
