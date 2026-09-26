import React from 'react';
import { FaBan, FaArrowRight } from 'react-icons/fa';
import { GoGitPullRequest } from 'react-icons/go';
import { timeAgo, formatDate, formatDuration } from '@shared/utils/util';
import type { CellState, CommitRow } from '../types';
import { commitKey } from '../helpers';
import { CELL_KIND_LABELS, cellKindLabel, cellPillTooltip, displayKind } from '../presentation';
import Tooltip from '../Tooltip/Tooltip';
import { revertHoldTooltip } from '@shared/utils/environments';

const FlowCell: React.FC<{
  cell: CellState;
  branch: string;
  isSelected: boolean;
  onSelect?: () => void;
  rowsById: Map<string, CommitRow>;
  onJumpToRow?: (rowId: string) => void;
}> = ({ cell, branch, isSelected, onSelect, rowsById, onJumpToRow }) => {
  if (cell.kind === 'no-changes') {
    return (
      <div className="cell cell--no-changes" aria-label={`No changes in ${branch}`}>
        No changes
      </div>
    );
  }

  if (cell.kind === 'unknown-history') {
    return (
      <Tooltip
        label={`This commit predates ${branch}'s available history, so whether it ran here is no longer recorded.`}
      >
        <div
          className="cell cell--unknown-history"
          aria-label={`History unavailable for this commit in ${branch}`}
        >
          <span className="cell__unknown-label">History unavailable</span>
        </div>
      </Tooltip>
    );
  }

  const failingChecks = cell.commitStatuses
    .filter((s) => s.phase === 'failure')
    .map((s) => (s.description ? `${s.key}: ${s.description}` : s.key));

  const time = cell.at ? timeAgo(cell.at) : '';
  const exact = cell.at ? formatDate(cell.at) : '';
  const visualKind = displayKind(cell.kind);

  const rowForCell = cell.commit ? rowsById.get(commitKey(cell.commit) ?? '') : undefined;
  const held = cell.revertCommit;
  // A held cell never borrows the row's PR: that is the earlier promotion of this dry SHA.
  const prId = held ? cell.pullRequest?.id : (cell.pullRequest?.id ?? rowForCell?.prId);
  const prUrl = held ? cell.pullRequest?.url : (cell.pullRequest?.url ?? rowForCell?.prUrl);
  const pillTooltip = held
    ? revertHoldTooltip(held, !!cell.revertedByRevertCommit)
    : cellPillTooltip(cell, branch);

  return (
    <div
      className={[
        'cell',
        `cell--${visualKind}`,
        held ? 'cell--held' : '',
        isSelected ? 'cell--selected' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      role="button"
      tabIndex={0}
      onClick={onSelect}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onSelect?.();
        }
      }}
      aria-label={`${
        visualKind === 'in-flight'
          ? cell.isProposed
            ? 'Proposed'
            : 'PR open'
          : CELL_KIND_LABELS[visualKind]
      } in ${branch}`}
    >
      <div className="cell__top">
        {visualKind === 'was-here' && cell.liveDurationMs != null ? (
          <Tooltip label={cellPillTooltip(cell, branch)}>
            <span className="cell__live-for">live for {formatDuration(cell.liveDurationMs)}</span>
          </Tooltip>
        ) : (
          <Tooltip label={pillTooltip}>
            <span
              className={`cell__pill cell__pill--${visualKind}${held ? ' cell__pill--held' : ''}`}
            >
              {visualKind === 'no-op' && (
                <>
                  <FaBan aria-hidden="true" />{' '}
                </>
              )}
              {cellKindLabel(cell)}
            </span>
          </Tooltip>
        )}
        {time && (
          <Tooltip label={exact}>
            <span className="cell__time">{time}</span>
          </Tooltip>
        )}
      </div>

      {prId && prUrl && (
        <div className="cell__commit">
          <div className="cell__commit-meta">
            <Tooltip
              label={
                <>
                  Promotion PR into {branch}: #{prId}
                  <br />
                  Open on remote
                </>
              }
            >
              <a
                className="cell__pr"
                href={prUrl}
                target="_blank"
                rel="noreferrer"
                onClick={(e) => e.stopPropagation()}
                aria-label={`Promotion pull request #${prId} into ${branch}, opens in new tab`}
              >
                <GoGitPullRequest aria-hidden="true" /> #{prId}
              </a>
            </Tooltip>
          </div>
        </div>
      )}

      <div className="cell__bottom">
        {cell.kind === 'failed' && failingChecks.length > 0 && (
          <span className="cell__reason">{failingChecks[0]}</span>
        )}
        {cell.kind === 'no-op' && (
          <span className="cell__reason cell__reason--muted">{cell.noopNote}</span>
        )}
        {visualKind === 'was-here' &&
          cell.supersededById &&
          onJumpToRow &&
          rowsById.get(cell.supersededById) && (
            <div className="cell__bottom-row">
              <button
                type="button"
                className="cell__superseded"
                onClick={(e) => {
                  e.stopPropagation();
                  onJumpToRow(cell.supersededById!);
                }}
                title={`Replaced by ${rowsById.get(cell.supersededById)!.subject}`}
              >
                <FaArrowRight aria-hidden="true" /> Replaced
              </button>
            </div>
          )}
      </div>
    </div>
  );
};

export default FlowCell;
