import React, { useMemo, useState, useCallback, useEffect, useRef } from 'react';
import { FaChevronLeft, FaFilter, FaSort, FaLayerGroup } from 'react-icons/fa';
import { GoGitCommit } from 'react-icons/go';
import { timeAgo, formatDate, getCommitUrl } from '@shared/utils/util';
import type { PromotionStrategy } from '@shared/types/promotion';
import type { CommitRow, FilterId, SortId } from './types';
import { buildMatrix } from './buildMatrix';
import { isEmptyCellKind } from './helpers';
import { Dropdown, DropdownItem } from './Dropdown/Dropdown';
import Tooltip from './Tooltip/Tooltip';
import FlowCell from './FlowCell/FlowCell';
import DetailDrawer from './DetailDrawer/DetailDrawer';
import { useDrawerWidth } from './useDrawerWidth';
import './index.scss';

export interface CellSelection {
  rowId: string;
  branch: string;
}

export interface HistoryViewProps {
  strategy?: PromotionStrategy;
  name?: string;
  namespace?: string;
  onBack?: () => void;
  initialSelection?: CellSelection | null;
  onSelectionChange?: (_selection: CellSelection | null) => void;
  // By default the view fills its host container (`height: 100%`), which is
  // correct when mounted inside a sized ancestor like ArgoCD's
  // `application-details__tree`. Hosts with no sized ancestor (the dashboard)
  // set this to clamp the root to the viewport instead.
  fillViewport?: boolean;
}

const HistoryView: React.FC<HistoryViewProps> = ({
  strategy,
  name: nameProp,
  namespace: namespaceProp,
  onBack,
  fillViewport = false,
  initialSelection = null,
  onSelectionChange,
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

  const [filter, setFilter] = useState<FilterId>('all');
  const [sort, setSort] = useState<SortId>('newest');
  const [envFilter, setEnvFilter] = useState<string[]>([]);
  const [selected, setSelectedState] = useState<CellSelection | null>(initialSelection);

  const onSelectionChangeRef = useRef(onSelectionChange);
  onSelectionChangeRef.current = onSelectionChange;

  const selectCell = useCallback((next: CellSelection | null) => {
    setSelectedState(next);
    onSelectionChangeRef.current?.(next);
  }, []);

  const drawer = useDrawerWidth();

  const validBranches = useMemo(() => new Set(envs.map((e) => e.branch)), [envs]);
  useEffect(() => {
    if (!selected || rows.length === 0) return;
    const selectedCell = rowsById.get(selected.rowId)?.cells[selected.branch];
    if (
      !validBranches.has(selected.branch) ||
      !selectedCell ||
      isEmptyCellKind(selectedCell.kind)
    ) {
      setSelectedState(null);
      onSelectionChangeRef.current?.(null);
    }
  }, [selected, rows.length, rowsById, validBranches]);

  const handleToggleEnvFilter = useCallback((branch: string) => {
    setEnvFilter((prev) =>
      prev.includes(branch) ? prev.filter((b) => b !== branch) : [...prev, branch],
    );
  }, []);

  const envScopedRows = useMemo(
    () =>
      envFilter.length
        ? rows.filter((r) => envFilter.some((b) => !isEmptyCellKind(r.cells[b]?.kind)))
        : rows,
    [rows, envFilter],
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
      requestAnimationFrame(() => {
        document
          .getElementById(`row-${rowId}`)
          ?.scrollIntoView({ block: 'center', behavior: 'smooth' });
      });
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

  const selectedRow = selected ? (rowsById.get(selected.rowId) ?? null) : null;
  const selectedCell = selectedRow && selected ? selectedRow.cells[selected.branch] : null;
  const hasMultipleEnvs = envs.length > 1;

  const FILTERS: { id: FilterId; label: string }[] = [
    { id: 'all', label: 'All commits' },
    { id: 'live', label: 'Live' },
    { id: 'in-flight', label: 'In flight' },
    { id: 'failed', label: 'Failed' },
    { id: 'no-op', label: 'No-op' },
  ];

  const SORTS: { id: SortId; label: string }[] = [
    { id: 'newest', label: 'Newest first' },
    { id: 'oldest', label: 'Oldest first' },
  ];

  const visibleEnvs = envFilter.length ? envs.filter((e) => envFilter.includes(e.branch)) : envs;

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
              active={envFilter.length > 0}
              value={
                envFilter.length === 0 ? (
                  'All environments'
                ) : envFilter.length === 1 ? (
                  <>
                    <span
                      className="hp-chip__dot"
                      style={{ background: envs.find((e) => e.branch === envFilter[0])?.color }}
                      aria-hidden="true"
                    />
                    {envFilter[0]}
                  </>
                ) : (
                  `${envFilter.length} environments`
                )
              }
            >
              {() => (
                <>
                  <DropdownItem
                    multi
                    selected={envFilter.length === 0}
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
                        selected={envFilter.includes(env.branch)}
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
              {envFilter.length > 0 && visibleEnvs.length < envs.length && (
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
                <span className="hp-matrix__head-label">Dry Commit</span>
                {visibleEnvs.map((env) => (
                  <span key={env.branch} className="hp-matrix__head-env">
                    <span
                      className="hp-matrix__head-env-swatch"
                      style={{ background: env.color }}
                    />
                    <span className="hp-matrix__head-env-branch" style={{ color: env.color }}>
                      {env.branch}
                    </span>
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
                  className={`hp-row ${selected?.rowId === row.id ? 'hp-row--selected' : ''}`}
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
                    const isFocusedEnv = envFilter.includes(env.branch);
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
                          isSelected={selected?.rowId === row.id && selected?.branch === env.branch}
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
          branch={selected?.branch ?? null}
          envs={envs}
          rowsById={rowsById}
          width={drawer.width}
          isResizing={drawer.isResizing}
          onResizeStart={drawer.onResizeStart}
          onResizeReset={drawer.onResizeReset}
          onResizeTo={drawer.onResizeTo}
          onClose={() => selectCell(null)}
          onJumpToRow={handleJumpToRow}
          onSelectCell={(branch) => selected && selectCell({ rowId: selected.rowId, branch })}
        />
      </div>
    </div>
  );
};

export default HistoryView;
