import React from 'react';
import { FaTimesCircle, FaTimes, FaBan, FaArrowRight } from 'react-icons/fa';
import { GoGitPullRequest, GoGitCommit } from 'react-icons/go';
import { FiChevronDown, FiChevronUp } from 'react-icons/fi';
import {
  timeAgo,
  formatDate,
  getCommitUrl,
  formatDuration,
  extractNameOnly,
  extractBodyPreTrailer,
} from '@shared/utils/util';
import { getChecks } from '@shared/utils/PSData';
import { commitStatusPlugins } from '@shared/components/plugins';
import type { Check } from '@shared/types/promotion';
import type { CellState, CommitRow, EnvColumn, HealthKey } from '../types';
import {
  DRAWER_MIN_WIDTH,
  DRAWER_MAX_WIDTH,
  HEALTH_LABELS,
  cellKindLabel,
  displayKind,
} from '../presentation';
import { isEmptyCellKind } from '../helpers';
import { buildRevertCommitApplyCommand, canShowRevertCommand } from '../revertCommand';
import { revertHoldTooltip } from '@shared/utils/environments';
import Tooltip from '../Tooltip/Tooltip';
import { StatusIcon, StatusType } from '../../StatusIcon';

const CopyCommandButton: React.FC<{ command: string }> = ({ command }) => {
  const [copied, setCopied] = React.useState(false);

  const onCopy = React.useCallback(async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable (insecure context / denied permission).
    }
  }, [command]);

  return (
    <button
      type="button"
      className="hp-drawer__copy"
      onClick={() => {
        void onCopy();
      }}
      aria-label={copied ? 'Copied' : 'Copy kubectl command'}
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  );
};

const DrawerChecks: React.FC<{ checks: Check[] }> = ({ checks }) => {
  const [expanded, setExpanded] = React.useState<Record<string, boolean>>({});

  return (
    <ul className="hp-drawer__checks">
      {checks.map((check) => {
        const Plugin = check.kind ? commitStatusPlugins[check.kind] : undefined;
        const manager = Plugin ? check.manager : undefined;
        const RowContent = manager ? Plugin?.rowContent : undefined;
        const isExpanded = !!expanded[check.name];
        const panelId = `hp-drawer-check-panel-${check.name}`;
        const phase: StatusType = HEALTH_LABELS[check.status as HealthKey]
          ? (check.status as StatusType)
          : 'unknown';

        return (
          <li key={check.name} className="hp-drawer__check-item">
            <div className={`hp-drawer__check hp-drawer__check--${check.status}`}>
              <span className="hp-drawer__check-icon" aria-hidden="true">
                <StatusIcon phase={phase} type="status" />
              </span>
              {RowContent && (
                <button
                  type="button"
                  className="hp-drawer__check-toggle"
                  aria-expanded={isExpanded}
                  aria-controls={panelId}
                  onClick={() =>
                    setExpanded((prev) => ({ ...prev, [check.name]: !prev[check.name] }))
                  }
                >
                  <span className="hp-sr-only">
                    {isExpanded ? 'Hide details for ' : 'Show details for '}
                    {check.name}
                  </span>
                  {isExpanded ? (
                    <FiChevronUp aria-hidden="true" />
                  ) : (
                    <FiChevronDown aria-hidden="true" />
                  )}
                </button>
              )}
              {Plugin && manager ? (
                <Plugin.rowHeader check={check} manager={manager} />
              ) : (
                <>
                  <span className="hp-sr-only">
                    {HEALTH_LABELS[check.status as HealthKey] ?? HEALTH_LABELS.unknown}:{' '}
                  </span>
                  <span className="hp-drawer__check-key">{check.name}</span>
                  {check.description && (
                    <span className="hp-drawer__check-desc">{check.description}</span>
                  )}
                  {check.url && (
                    <a
                      href={check.url}
                      target="_blank"
                      rel="noreferrer"
                      className="hp-drawer__check-link"
                      aria-label={`View details for ${check.name}, opens in new tab`}
                    >
                      View details
                    </a>
                  )}
                </>
              )}
            </div>
            {RowContent && manager && (
              <div id={panelId} className="hp-drawer__check-panel" hidden={!isExpanded}>
                <RowContent check={check} manager={manager} />
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
};

const DetailDrawer: React.FC<{
  row: CommitRow | null;
  cell: CellState | null;
  branch: string | null;
  namespace?: string;
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
  namespace,
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
  const checks = getChecks([...failingChecks, ...pendingChecks, ...passingChecks], branch);

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

  const [prId, prUrl] = cell.revertCommit
    ? [cell.pullRequest?.id, cell.pullRequest?.url]
    : cell.pullRequest?.id && cell.pullRequest?.url
      ? [cell.pullRequest.id, cell.pullRequest.url]
      : [row.prId, row.prUrl];

  const env = envs.find((e) => e.branch === branch);
  const changeTransferPolicyName = env?.changeTransferPolicyName;
  const showRevert = canShowRevertCommand(cell);
  const revertCommand =
    showRevert && hydrated?.sha && namespace && changeTransferPolicyName
      ? buildRevertCommitApplyCommand({
          namespace,
          changeTransferPolicyName,
          instanceId: env?.instanceId,
          branch,
          sha: hydrated.sha,
        })
      : null;

  const kindBadge = (
    <span
      className={`hp-drawer__kind hp-drawer__kind--${displayKind(cell.kind)}${
        cell.revertCommit ? ' hp-drawer__kind--held' : ''
      }`}
    >
      {cellKindLabel(cell)}
    </span>
  );

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
            {cell.revertCommit ? (
              <Tooltip label={revertHoldTooltip(cell.revertCommit, !!cell.revertedByRevertCommit)}>
                {kindBadge}
              </Tooltip>
            ) : (
              kindBadge
            )}
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
            {prId && prUrl && (
              <>
                <span className="hp-drawer__sep" aria-hidden="true">
                  ·
                </span>
                <a
                  href={prUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="hp-drawer__pr"
                  aria-label={`Pull request #${prId}, opens in new tab`}
                >
                  <GoGitPullRequest aria-hidden="true" /> #{prId}
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
            <h3>referenced commits ({refs.length})</h3>
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

        {cell.restoredFrom && (
          <div className="hp-drawer__section hp-drawer__section--muted">
            <h3>Restored, not promoted</h3>
            <p>
              Someone moved {branch} back to <code>{cell.restoredFrom.slice(0, 7)}</code> by pushing
              a revert commit
              {row.restoreShaShort && (
                <>
                  , <code>{row.restoreShaShort}</code>
                </>
              )}
              {row.restoreSubject && <> — “{row.restoreSubject}”</>}. This row repeats the dry
              commit of the version that was put back, so the identical row further down is that
              version's original promotion. The pull request and checks shown here were copied from
              it and describe that promotion, not this restore.
            </p>
          </div>
        )}

        {revertCommand && (
          <div className="hp-drawer__section">
            <div className="hp-drawer__restore-header">
              <h3>Restore this version on {branch}</h3>
              <CopyCommandButton command={revertCommand} />
            </div>
            <pre className="hp-drawer__command">{revertCommand}</pre>
            <p className="hp-drawer__restore-note">
              Applies a RevertCommit for this environment. The controller restores the active branch
              to this hydrated commit and does not open a promotion pull request that would put that
              active branch&apos;s dry commit back. A pull request already open for a different
              proposed commit stays open and is not auto-merged. Push a new commit on the proposed
              branch. Delete the RevertCommit to leave the reverted state. Paste into bash, zsh, or
              fish.
            </p>
          </div>
        )}

        {cell.commitStatuses.length > 0 && (
          <div className="hp-drawer__section">
            <h3>Checks</h3>
            <p className="hp-drawer__checks-group-label">{checksLabel}</p>
            <DrawerChecks checks={checks} />
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
              const selectable = !isHere && !isEmptyCellKind(c.kind);
              const pillKind = displayKind(c.kind);
              const pill = (
                <span
                  className={`cell__pill cell__pill--${pillKind}${c.revertCommit ? ' cell__pill--held' : ''}`}
                >
                  {pillKind === 'failed' && <FaTimesCircle aria-hidden="true" />}
                  {pillKind === 'no-op' && <FaBan aria-hidden="true" />}
                  {(pillKind === 'failed' || pillKind === 'no-op') && ' '}
                  {cellKindLabel(c, true)}
                </span>
              );
              const inner = (
                <>
                  <span className="hp-drawer__presence-branch">{e.branch}</span>
                  {c.revertCommit ? (
                    <Tooltip label={revertHoldTooltip(c.revertCommit, !!c.revertedByRevertCommit)}>
                      {pill}
                    </Tooltip>
                  ) : (
                    pill
                  )}
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
