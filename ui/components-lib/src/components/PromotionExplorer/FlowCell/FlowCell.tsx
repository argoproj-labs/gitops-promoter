import React from 'react';
import { FaBan, FaArrowRight } from 'react-icons/fa';
import { GoBlocked, GoGitMerge } from 'react-icons/go';
import { timeAgo, formatDate, formatDuration, getCommitUrl } from '@shared/utils/util';
import type { CellState, CommitRow } from '../types';
import { commitKey, safeHttpUrl, shortSha } from '../helpers';
import {
  CELL_KIND_LABELS,
  cellHeaderTime,
  cellKindLabel,
  cellPillTooltip,
  displayKind,
} from '../presentation';
import Tooltip from '../Tooltip/Tooltip';
import CellChecks from './CellChecks';
import { restoreBlockTooltip } from '@shared/utils/environments';

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

  const headerTime = cellHeaderTime(cell);
  const visualKind = displayKind(cell.kind);

  const rowForCell = cell.commit ? rowsById.get(commitKey(cell.commit) ?? '') : undefined;
  const blockedEnv = cell.restoreActiveCommit;
  const blocked = !!cell.blockedByRestoreActiveCommit;
  // A restore cell's dry sha is the version it restored, so a lookup by that sha finds the
  // earlier promotion and its pull request. The restore commit has no pull request of its own.
  const isRestore = !!cell.restoredFrom;
  const prId =
    blockedEnv || blocked || isRestore
      ? cell.pullRequest?.id
      : (cell.pullRequest?.id ?? rowForCell?.prId);
  const prUrl =
    blockedEnv || blocked || isRestore
      ? cell.pullRequest?.url
      : (cell.pullRequest?.url ?? rowForCell?.prUrl);
  const hydratedSha = cell.hydrated?.sha;
  const hydratedRepoUrl =
    cell.hydrated?.repoURL || cell.commit?.repoURL || rowForCell?.repoUrl || '';
  const hydratedUrl = hydratedSha
    ? safeHttpUrl(getCommitUrl(hydratedRepoUrl, hydratedSha))
    : undefined;
  const safePrUrl = safeHttpUrl(prUrl);
  const pillTooltip = blockedEnv
    ? restoreBlockTooltip(blockedEnv, blocked)
    : cellPillTooltip(cell, branch);

  const isWasHere = visualKind === 'was-here';
  const replacement = cell.supersededById ? rowsById.get(cell.supersededById) : undefined;
  const showChecks =
    !blockedEnv &&
    !blocked &&
    (visualKind === 'live' || visualKind === 'in-flight' || visualKind === 'failed') &&
    cell.commitStatuses.length > 0;
  const wasHereTooltip = `${cellPillTooltip(cell, branch)}${
    cell.liveDurationMs != null ? ` · live for ${formatDuration(cell.liveDurationMs)}` : ''
  }`;

  const hydratedGroup = (hydratedSha || (prId && prUrl) || headerTime) && (
    <div className="cell__hydrated">
      {hydratedSha &&
        (hydratedUrl ? (
          <a
            className="cell__sha"
            href={hydratedUrl}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            title={`Hydrated commit ${hydratedSha} on ${branch}, opens in new tab`}
            aria-label={`Hydrated commit ${shortSha(hydratedSha)} in ${branch}, opens in new tab`}
          >
            {shortSha(hydratedSha)}
          </a>
        ) : (
          <span className="cell__sha" title={`Hydrated commit ${hydratedSha}`}>
            {shortSha(hydratedSha)}
          </span>
        ))}
      {prId &&
        (safePrUrl ? (
          <a
            className="cell__pr"
            href={safePrUrl}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            title={`Promotion PR into ${branch}: #${prId}`}
            aria-label={`Promotion pull request #${prId} into ${branch}, opens in new tab`}
          >
            <GoGitMerge aria-hidden="true" />#{prId}
          </a>
        ) : (
          <span className="cell__pr" title={`Pull request #${prId}`}>
            <GoGitMerge aria-hidden="true" />
            <span className="hp-sr-only">Pull request </span>#{prId}
          </span>
        ))}
      {headerTime && (
        <time
          className="cell__time"
          dateTime={headerTime.at}
          title={`${headerTime.label} ${formatDate(headerTime.at)}`}
        >
          <span className="hp-sr-only">{headerTime.label} </span>
          {timeAgo(headerTime.at)}
        </time>
      )}
    </div>
  );

  return (
    <div
      className={[
        'cell',
        `cell--${visualKind}`,
        blocked ? 'cell--blocked' : blockedEnv ? 'cell--blocked-env' : '',
        isRestore ? 'cell--restore' : '',
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
            ? blocked
              ? 'Blocked'
              : 'Proposed'
            : 'PR open'
          : CELL_KIND_LABELS[visualKind]
      } in ${branch}`}
    >
      {isWasHere ? (
        <div className="cell__head cell__head--was-here">
          <Tooltip label={wasHereTooltip}>
            <span className="cell__was-label">Previously live</span>
          </Tooltip>
          {hydratedGroup}
        </div>
      ) : (
        <div className="cell__head">
          <Tooltip label={pillTooltip}>
            <span
              className={`cell__pill cell__pill--${visualKind}${
                blocked ? ' cell__pill--blocked' : blockedEnv ? ' cell__pill--blocked-env' : ''
              }${isRestore ? ' cell__pill--restore' : ''}`}
            >
              {visualKind === 'no-op' && (
                <>
                  <FaBan aria-hidden="true" />{' '}
                </>
              )}
              {blockedEnv && !blocked && (
                <GoBlocked className="cell__pill__stop" aria-hidden="true" />
              )}
              {cellKindLabel(cell)}
            </span>
          </Tooltip>
          {hydratedGroup}
        </div>
      )}

      {showChecks && (
        <div className="cell__body">
          <CellChecks statuses={cell.commitStatuses} branch={branch} />
        </div>
      )}

      {isWasHere && replacement && onJumpToRow && cell.supersededById && (
        <button
          type="button"
          className="cell__superseded"
          onClick={(e) => {
            e.stopPropagation();
            onJumpToRow(cell.supersededById!);
          }}
          title={`Replaced by ${replacement.subject}`}
        >
          <FaArrowRight aria-hidden="true" /> Replaced
        </button>
      )}
    </div>
  );
};

export default FlowCell;
