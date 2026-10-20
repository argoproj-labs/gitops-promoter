import React, { useRef } from 'react';
import type { EnrichedBranchCommitStatus } from '@shared/types/promotion';
import { safeHttpUrl } from '../helpers';
import { hiddenRowsBelow, useScrollOverflow } from './useScrollOverflow';

type CheckTone = 'pass' | 'fail' | 'run';

const TONE_LABEL: Record<CheckTone, string> = { pass: 'passed', fail: 'failed', run: 'running' };
const TONE_ORDER: Record<CheckTone, number> = { fail: 0, run: 1, pass: 2 };

export function checkTone(phase: string): CheckTone {
  if (phase === 'success') return 'pass';
  if (phase === 'failure') return 'fail';
  return 'run';
}

const CheckIcon: React.FC<{ tone: CheckTone }> = ({ tone }) => (
  <svg
    className={`cell-check__icon cell-check__icon--${tone}`}
    width="17"
    height="17"
    viewBox="0 0 17 17"
    aria-hidden="true"
    focusable="false"
    data-testid={tone === 'run' ? 'cell-check-spinner' : undefined}
  >
    {tone === 'run' ? (
      <>
        <circle className="cell-check__track" cx="8.5" cy="8.5" r="7.5" />
        <path className="cell-check__arc" d="M13.803 13.803A7.5 7.5 0 1 1 13.803 3.197" />
      </>
    ) : (
      <>
        <circle className="cell-check__ring" cx="8.5" cy="8.5" r="7.5" />
        {tone === 'pass' ? (
          <polyline className="cell-check__mark" points="5.2,8.5 7.5,10.8 11.8,6.2" />
        ) : (
          <path className="cell-check__mark" d="M5.8 5.8L11.2 11.2M11.2 5.8L5.8 11.2" />
        )}
      </>
    )}
  </svg>
);

const stopActivation = (e: React.KeyboardEvent) => {
  if (e.key === 'Enter' || e.key === ' ') e.stopPropagation();
};

const CheckRow: React.FC<{ status: EnrichedBranchCommitStatus; tone: CheckTone }> = ({
  status,
  tone,
}) => {
  const href = safeHttpUrl(status.url);
  return (
    <li
      className={`cell-check cell-check--${tone}`}
      title={`${status.key}: ${TONE_LABEL[tone]}${status.description ? ` - ${status.description}` : ''}`}
    >
      <CheckIcon tone={tone} />
      {href ? (
        <a
          className="cell-check__name cell-check__link"
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`${status.key} (opens details)`}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={stopActivation}
        >
          {status.key}
        </a>
      ) : (
        <span className="cell-check__name">{status.key}</span>
      )}
      <span className="hp-sr-only">{TONE_LABEL[tone]}</span>
    </li>
  );
};

const CellChecks: React.FC<{
  statuses: EnrichedBranchCommitStatus[];
  branch: string;
}> = ({ statuses, branch }) => {
  const listRef = useRef<HTMLUListElement>(null);
  const overflow = useScrollOverflow(listRef, statuses.length);

  if (statuses.length === 0) return null;

  const checks = statuses
    .map((s, index) => ({ s, index, tone: checkTone(s.phase) }))
    .sort((a, b) => TONE_ORDER[a.tone] - TONE_ORDER[b.tone] || a.index - b.index);

  const { hasOverflow, atTop, atBottom } = overflow;
  const hiddenBelow = hasOverflow ? hiddenRowsBelow(overflow, checks.length) : 0;

  const scrollPage = (e: React.MouseEvent) => {
    e.stopPropagation();
    const el = listRef.current;
    if (!el) return;
    const rowPitch = overflow.scrollHeight / checks.length;
    el.scrollTo({ top: el.scrollTop + Math.max(rowPitch, el.clientHeight - rowPitch) });
  };

  const listClass = [
    'cell__checks',
    hasOverflow ? 'cell__checks--overflow' : '',
    hasOverflow && !atTop ? 'cell__checks--fade-top' : '',
    hasOverflow && !atBottom ? 'cell__checks--fade-bottom' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className="cell__checks-wrap">
      <ul
        ref={listRef}
        className={listClass}
        aria-label={`Checks in ${branch}, ${checks.length} total`}
        tabIndex={hasOverflow ? 0 : undefined}
        onKeyDown={stopActivation}
      >
        {checks.map(({ s, tone }) => (
          <CheckRow key={s.key} status={s} tone={tone} />
        ))}
      </ul>
      {hiddenBelow > 0 && (
        <button
          type="button"
          className="cell__checks-more"
          aria-hidden="true"
          tabIndex={-1}
          onClick={scrollPage}
          onKeyDown={stopActivation}
        >
          <span className="cell__checks-more__caret" />
          {hiddenBelow} more
        </button>
      )}
    </div>
  );
};

export default CellChecks;
