import React from 'react';
import { FaBan, FaArrowRight } from 'react-icons/fa';
import { GoGitPullRequest } from 'react-icons/go';
import { timeAgo, formatDate } from '@shared/utils/util';
import type { CellState, CommitRow } from './types';
import { commitKey } from './helpers';
import { CELL_KIND_LABELS, cellPillTooltip } from './presentation';
import Tooltip from './Tooltip';

const FlowCell: React.FC<{
  cell: CellState;
  branch: string;
  isSelected: boolean;
  onSelect?: () => void;
  rowsById: Map<string, CommitRow>;
  onJumpToRow?: (rowId: string) => void;
}> = ({ cell, branch, isSelected, onSelect, rowsById, onJumpToRow }) => {
  if (cell.kind === 'not-reached') {
    return (
      <div className="cell cell--not-reached" aria-label={`Not reached in ${branch}`}>
        Not reached
      </div>
    );
  }

  const failingChecks = cell.commitStatuses
    .filter((s) => s.phase === 'failure')
    .map((s) => (s.description ? `${s.key}: ${s.description}` : s.key));

  const time = cell.at ? timeAgo(cell.at) : '';
  const exact = cell.at ? formatDate(cell.at) : '';

  // Each cell shows only what is specific to *this env*: the promotion PR.
  // The commit identity (subject, SHA, author) is identical for every reached
  // cell in the row — it's the same commit promoted across envs — so it lives
  // once in the row label rather than being repeated here.
  const rowForCell = cell.commit ? rowsById.get(commitKey(cell.commit) ?? '') : undefined;
  const prId = cell.pullRequest?.id ?? rowForCell?.prId;
  const prUrl = cell.pullRequest?.url ?? rowForCell?.prUrl;

  return (
    <button
      type="button"
      className={['cell', `cell--${cell.kind}`, isSelected ? 'cell--selected' : '']
        .filter(Boolean)
        .join(' ')}
      onClick={onSelect}
      aria-label={`${
        cell.kind === 'in-flight'
          ? cell.isProposed
            ? 'Proposed'
            : 'PR open'
          : CELL_KIND_LABELS[cell.kind]
      } in ${branch}`}
      title={exact}
    >
      <div className="cell__top">
        <Tooltip label={cellPillTooltip(cell, branch)}>
          <span className={`cell__pill cell__pill--${cell.kind}`}>
            {cell.kind === 'live' && 'LIVE'}
            {cell.kind === 'in-flight' && (cell.isProposed ? 'PROPOSED' : 'PR OPEN')}
            {cell.kind === 'was-here' && 'WAS HERE'}
            {cell.kind === 'failed' && 'FAILED'}
            {cell.kind === 'no-op' && (
              <>
                <FaBan aria-hidden="true" /> NO-OP
              </>
            )}
          </span>
        </Tooltip>
        {time && <span className="cell__time">{time}</span>}
      </div>

      {prId && (
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
        {cell.kind === 'was-here' &&
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
    </button>
  );
};

export default FlowCell;
