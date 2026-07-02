import React from 'react';
import { FaTimesCircle, FaTimes, FaBan, FaArrowRight } from 'react-icons/fa';
import { GoGitPullRequest } from 'react-icons/go';
import { timeAgo, formatDate, getCommitUrl } from '@shared/utils/util';
import type { CellState, CommitRow, EnvColumn, HealthKey } from './types';
import { DRAWER_MIN_WIDTH, DRAWER_MAX_WIDTH, HEALTH_LABELS, healthIcon } from './presentation';

/* ═════════════════════════════════════════════════════════════════
   Detail drawer
   ═════════════════════════════════════════════════════════════════ */

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

  // Cross-env story: which other envs did this same commit reach?
  const presence = envs
    .map((e) => ({ env: e, cell: row.cells[e.branch] }))
    .filter((x) => x.cell.kind !== 'not-reached');

  // Find where it came from (closest upstream env where this commit was live/was-here)
  const envIdx = envs.findIndex((e) => e.branch === branch);
  const upstream = envs
    .slice(0, envIdx)
    .reverse()
    .find((e) => {
      const k = row.cells[e.branch].kind;
      return k === 'live' || k === 'was-here';
    });
  const downstreamNotReached = envs
    .slice(envIdx + 1)
    .filter((e) => row.cells[e.branch].kind === 'not-reached');

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
            // Left arrow widens (panel is right-anchored), right arrow narrows.
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
              {cell.kind === 'was-here' && 'WAS HERE'}
              {cell.kind === 'failed' && 'FAILED'}
              {cell.kind === 'no-op' && 'NO-OP'}
              {cell.kind === 'not-reached' && 'NOT REACHED'}
            </span>
            <span className="hp-drawer__branch">{branch}</span>
          </div>
          <h2 className="hp-drawer__subject">{row.subject}</h2>
          <div className="hp-drawer__meta">
            <span className="hp-drawer__author">{row.author}</span>
            <span className="hp-drawer__sep">·</span>
            {row.repoUrl && row.dryShaFull ? (
              <a
                href={getCommitUrl(row.repoUrl, row.dryShaFull)}
                target="_blank"
                rel="noreferrer"
                className="hp-drawer__sha"
                aria-label={`Commit ${row.dryShaShort}, opens in new tab`}
              >
                {row.dryShaShort}
              </a>
            ) : (
              <span className="hp-drawer__sha">{row.dryShaShort}</span>
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
                <span title={exact}>{ago}</span>
              </>
            )}
          </div>
        </div>

        {cell.kind === 'failed' && failingChecks.length > 0 && (
          <div className="hp-drawer__section hp-drawer__section--danger">
            <h3>Why it failed</h3>
            <ul>
              {failingChecks.map((c) => (
                <li key={c.key}>
                  <strong>{c.key}</strong>
                  {c.description ? <> — {c.description}</> : null}
                  {c.url && (
                    <>
                      {' '}
                      <a
                        href={c.url}
                        target="_blank"
                        rel="noreferrer"
                        aria-label={`View details for ${c.key}, opens in new tab`}
                      >
                        View details
                      </a>
                    </>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}

        {cell.kind === 'no-op' && cell.noopNote && (
          <div className="hp-drawer__section hp-drawer__section--muted">
            <h3>Why it's a no-op</h3>
            <p>{cell.noopNote}</p>
          </div>
        )}

        {cell.commitStatuses.length > 0 && cell.kind !== 'failed' && (
          <div className="hp-drawer__section">
            <h3>Checks</h3>
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
              return (
                <li
                  key={e.branch}
                  className={[
                    'hp-drawer__presence-item',
                    `hp-drawer__presence-item--${c.kind}`,
                    isHere ? 'hp-drawer__presence-item--current' : '',
                  ].join(' ')}
                >
                  <span className="hp-drawer__presence-branch">{e.branch}</span>
                  <span className={`cell__pill cell__pill--${c.kind}`}>
                    {c.kind === 'live' && 'LIVE'}
                    {c.kind === 'in-flight' && (c.isProposed ? 'PROPOSED' : 'PR OPEN')}
                    {c.kind === 'was-here' && 'WAS HERE'}
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
                    {c.kind === 'not-reached' && '—'}
                  </span>
                  {c.at && <span className="hp-drawer__presence-time">{timeAgo(c.at)}</span>}
                </li>
              );
            })}
          </ul>
          {presence.length === 1 && downstreamNotReached.length > 0 && (
            <p className="hp-drawer__hint">
              This commit only reached <strong>{branch}</strong>. It hasn't moved on to{' '}
              {downstreamNotReached.map((e) => e.branch).join(', ')}.
            </p>
          )}
          {upstream && (
            <p className="hp-drawer__hint">
              Promoted here from <strong>{upstream.branch}</strong>.
            </p>
          )}
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
