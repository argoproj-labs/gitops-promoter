import React from 'react';
import { FaTimesCircle, FaTimes, FaBan, FaArrowRight } from 'react-icons/fa';
import { GoGitPullRequest, GoGitCommit } from 'react-icons/go';
import {
  timeAgo,
  formatDate,
  getCommitUrl,
  formatDuration,
  extractNameOnly,
  extractBodyPreTrailer,
} from '@shared/utils/util';
import type { CellState, CommitRow, EnvColumn, HealthKey } from '../types';
import { DRAWER_MIN_WIDTH, DRAWER_MAX_WIDTH, HEALTH_LABELS, healthIcon } from '../presentation';
import Tooltip from '../Tooltip/Tooltip';

const DetailDrawer: React.FC<{
  row: CommitRow | null;
  cell: CellState | null;
  branch: string | null;
  envs: EnvColumn[];
  rowsById: Map<string, CommitRow>;
  width: number;
  isResizing: boolean;
  onResizeStart: (e: React.PointerEvent) => void;
  onResizeReset: () => void;
  onResizeTo: (width: number) => void;
  onClose: () => void;
  onJumpToRow: (id: string) => void;
  onSelectCell: (branch: string) => void;
}> = ({
  row,
  cell,
  branch,
  envs,
  rowsById,
  width,
  isResizing,
  onResizeStart,
  onResizeReset,
  onResizeTo,
  onClose,
  onJumpToRow,
  onSelectCell,
}) => {
  if (!row || !cell || !branch) {
    return (
      <aside className="hp-drawer">
        <div className="hp-drawer__prompt">
          <p>Select a cell to see what happened to a commit in that environment.</p>
        </div>
      </aside>
    );
  }

  const exact = cell.at ? formatDate(cell.at) : '';
  const ago = cell.at ? timeAgo(cell.at) : '';
  const failingChecks = cell.commitStatuses.filter((s) => s.phase === 'failure');
  const passingChecks = cell.commitStatuses.filter((s) => s.phase === 'success');
  const pendingChecks = cell.commitStatuses.filter((s) => s.phase === 'pending');

  const checksLabel = cell.isProposed ? 'Proposed' : 'Active';

  const hydrated = cell.hydrated;
  const hydratedRepoURL = hydrated?.repoURL || cell.commit?.repoURL || row.repoUrl || '';
  const hydratedRepoName = hydratedRepoURL
    ? hydratedRepoURL
        .replace(/\.git$/, '')
        .split('/')
        .pop()
    : undefined;
  const refs = cell.references ?? [];

  return (
    <aside
      className={`hp-drawer hp-drawer--open ${isResizing ? 'hp-drawer--resizing' : ''}`}
      style={{ width, minWidth: width }}
    >
      <div
        className="hp-drawer__resizer"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize details panel"
        aria-valuenow={Math.round(width)}
        aria-valuemin={DRAWER_MIN_WIDTH}
        aria-valuemax={DRAWER_MAX_WIDTH}
        tabIndex={0}
        onPointerDown={onResizeStart}
        onDoubleClick={onResizeReset}
        onKeyDown={(e) => {
          if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
            e.preventDefault();
            const step = e.shiftKey ? 48 : 16;
            const dir = e.key === 'ArrowLeft' ? 1 : -1;
            onResizeTo(Math.min(DRAWER_MAX_WIDTH, Math.max(DRAWER_MIN_WIDTH, width + dir * step)));
          } else if (e.key === 'Home') {
            e.preventDefault();
            onResizeReset();
          }
        }}
      >
        <span className="hp-drawer__resizer-grip" aria-hidden="true" />
      </div>
      <button
        type="button"
        className="hp-drawer__close"
        onClick={onClose}
        aria-label="Close details"
      >
        <FaTimes aria-hidden="true" />
      </button>

      <div className="hp-drawer__scroll">
        <div className="hp-drawer__header">
          <div className="hp-drawer__badges">
            <span className={`hp-drawer__kind hp-drawer__kind--${cell.kind}`}>
              {cell.kind === 'live' && 'LIVE'}
              {cell.kind === 'in-flight' && (cell.isProposed ? 'PROPOSED' : 'PR OPEN')}
              {cell.kind === 'was-here' && 'REPLACED'}
              {cell.kind === 'failed' && 'FAILED'}
              {cell.kind === 'no-op' && 'NO-OP'}
              {cell.kind === 'no-changes' && 'NO CHANGES'}
            </span>
            <span className="hp-drawer__branch">{branch}</span>
          </div>
          <h2 className="hp-drawer__subject">{row.subject}</h2>
          <div className="hp-drawer__meta">
            <span className="hp-drawer__author">{row.author}</span>
            <span className="hp-drawer__sep">·</span>
            {row.repoUrl && row.dryShaFull ? (
              <Tooltip label="Dry commit">
                <a
                  href={getCommitUrl(row.repoUrl, row.dryShaFull)}
                  target="_blank"
                  rel="noreferrer"
                  className="hp-drawer__sha"
                  aria-label={`Dry commit ${row.dryShaShort}, opens in new tab`}
                >
                  {row.dryShaShort}
                </a>
              </Tooltip>
            ) : (
              <Tooltip label="Dry commit">
                <span className="hp-drawer__sha">{row.dryShaShort}</span>
              </Tooltip>
            )}
            {row.prId && (
              <>
                <span className="hp-drawer__sep" aria-hidden="true">
                  ·
                </span>
                <a
                  href={row.prUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="hp-drawer__pr"
                  aria-label={`Pull request #${row.prId}, opens in new tab`}
                >
                  <GoGitPullRequest aria-hidden="true" /> #{row.prId}
                </a>
              </>
            )}
            {ago && (
              <>
                <span className="hp-drawer__sep">·</span>
                <Tooltip label={exact}>
                  <span>{ago}</span>
                </Tooltip>
              </>
            )}
            {cell.kind === 'was-here' && cell.liveDurationMs != null && (
              <>
                <span className="hp-drawer__sep">·</span>
                <span>live for {formatDuration(cell.liveDurationMs)}</span>
              </>
            )}
          </div>
          {hydrated?.sha && (
            <div className="hp-drawer__deployed">
              Deployed as{' '}
              {hydratedRepoURL ? (
                <Tooltip label="Hydrated commit">
                  <a
                    href={getCommitUrl(hydratedRepoURL, hydrated.sha)}
                    target="_blank"
                    rel="noreferrer"
                    className="hp-drawer__deployed-sha"
                    aria-label={`Hydrated commit ${hydrated.sha.slice(0, 7)}, opens in new tab`}
                  >
                    {hydrated.sha.slice(0, 7)}
                  </a>
                </Tooltip>
              ) : (
                <Tooltip label="Hydrated commit">
                  <span className="hp-drawer__deployed-sha">{hydrated.sha.slice(0, 7)}</span>
                </Tooltip>
              )}
              {hydratedRepoName && (
                <span className="hp-drawer__deployed-repo"> ({hydratedRepoName})</span>
              )}
            </div>
          )}
        </div>

        {refs.length > 0 && (
          <div className="hp-drawer__section">
            <h3>app commits ({refs.length})</h3>
            <ul className="hp-drawer__refs">
              {refs.map((ref, i) => {
                const body = ref.body ? extractBodyPreTrailer(ref.body) : '';
                return (
                  <li key={ref.sha ?? i} className="hp-drawer__ref">
                    <div className="hp-drawer__ref-subject">
                      {(ref.subject ?? '').trim() || '(no subject)'}
                    </div>
                    <div className="hp-drawer__ref-meta">
                      <span className="hp-drawer__ref-author">
                        {ref.author ? extractNameOnly(ref.author) : '—'}
                      </span>
                      {ref.sha && (
                        <>
                          <span className="hp-drawer__sep" aria-hidden="true">
                            ·
                          </span>
                          {ref.url ? (
                            <a
                              href={ref.url}
                              target="_blank"
                              rel="noreferrer"
                              className="hp-drawer__ref-sha"
                              aria-label={`Source commit ${ref.sha.slice(0, 7)}, opens in new tab`}
                            >
                              <GoGitCommit aria-hidden="true" /> {ref.sha.slice(0, 7)}
                            </a>
                          ) : (
                            <span className="hp-drawer__ref-sha">
                              <GoGitCommit aria-hidden="true" /> {ref.sha.slice(0, 7)}
                            </span>
                          )}
                        </>
                      )}
                    </div>
                    {body && <pre className="hp-drawer__ref-body">{body}</pre>}
                  </li>
                );
              })}
            </ul>
          </div>
        )}

        {cell.kind === 'no-op' && cell.noopNote && (
          <div className="hp-drawer__section hp-drawer__section--muted">
            <h3>Why it's a no-op</h3>
            <p>{cell.noopNote}</p>
          </div>
        )}

        {cell.commitStatuses.length > 0 && (
          <div className="hp-drawer__section">
            <h3>Checks</h3>
            <p className="hp-drawer__checks-group-label">{checksLabel}</p>
            <ul className="hp-drawer__checks">
              {[...passingChecks, ...pendingChecks, ...failingChecks].map((c) => (
                <li key={c.key} className={`hp-drawer__check hp-drawer__check--${c.phase}`}>
                  <span className="hp-drawer__check-icon" aria-hidden="true">
                    {healthIcon[c.phase as HealthKey] ?? healthIcon.unknown}
                  </span>
                  <span className="hp-sr-only">
                    {HEALTH_LABELS[c.phase as HealthKey] ?? HEALTH_LABELS.unknown}:{' '}
                  </span>
                  <span className="hp-drawer__check-key">{c.key}</span>
                  {c.description && <span className="hp-drawer__check-desc">{c.description}</span>}
                </li>
              ))}
            </ul>
          </div>
        )}

        {row.body && (
          <div className="hp-drawer__section">
            <h3>Commit message</h3>
            <pre className="hp-drawer__body">{row.body}</pre>
          </div>
        )}

        <div className="hp-drawer__section">
          <h3>This commit across environments</h3>
          <ul className="hp-drawer__presence">
            {envs.map((e) => {
              const c = row.cells[e.branch];
              const isHere = e.branch === branch;
              const selectable = !isHere && c.kind !== 'no-changes';
              const inner = (
                <>
                  <span className="hp-drawer__presence-branch">{e.branch}</span>
                  <span className={`cell__pill cell__pill--${c.kind}`}>
                    {c.kind === 'live' && 'LIVE'}
                    {c.kind === 'in-flight' && (c.isProposed ? 'PROPOSED' : 'PR OPEN')}
                    {c.kind === 'was-here' && 'REPLACED'}
                    {c.kind === 'failed' && (
                      <>
                        <FaTimesCircle aria-hidden="true" /> FAILED
                      </>
                    )}
                    {c.kind === 'no-op' && (
                      <>
                        <FaBan aria-hidden="true" /> NO-OP
                      </>
                    )}
                    {c.kind === 'no-changes' && '—'}
                  </span>
                  {c.at && (
                    <Tooltip label={formatDate(c.at)}>
                      <span className="hp-drawer__presence-time">{timeAgo(c.at)}</span>
                    </Tooltip>
                  )}
                </>
              );
              const className = [
                'hp-drawer__presence-item',
                `hp-drawer__presence-item--${c.kind}`,
                isHere ? 'hp-drawer__presence-item--current' : '',
                selectable ? 'hp-drawer__presence-item--selectable' : '',
              ].join(' ');
              return (
                <li key={e.branch}>
                  {selectable ? (
                    <button
                      type="button"
                      className={className}
                      onClick={() => onSelectCell(e.branch)}
                      aria-label={`View ${e.branch} details for this commit`}
                    >
                      {inner}
                    </button>
                  ) : (
                    <div className={className} aria-current={isHere ? 'true' : undefined}>
                      {inner}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </div>

        {cell.kind === 'was-here' && cell.supersededById && rowsById.get(cell.supersededById) && (
          <div className="hp-drawer__section">
            <h3>Replaced by</h3>
            <button
              type="button"
              className="hp-drawer__replaced"
              onClick={() => onJumpToRow(cell.supersededById!)}
            >
              <FaArrowRight aria-hidden="true" />
              <span>{rowsById.get(cell.supersededById)!.subject}</span>
            </button>
          </div>
        )}
      </div>
    </aside>
  );
};

export default DetailDrawer;
