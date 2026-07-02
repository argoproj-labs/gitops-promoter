import React from 'react';
import { FaCheckCircle, FaTimesCircle, FaSpinner, FaQuestionCircle } from 'react-icons/fa';
import type { CommitStatus } from '@shared/types/promotion';
import type { CellKind, CellState, HealthKey } from './types';

/* ═════════════════════════════════════════════════════════════════
   Presentation constants + label helpers
   ═════════════════════════════════════════════════════════════════ */

export const HEALTH_LABELS: Record<HealthKey, string> = {
  success: 'Healthy',
  failure: 'Failed',
  pending: 'Pending',
  unknown: 'Unknown',
};

// Human-readable status words for screen readers, mirroring the visible pills.
export const CELL_KIND_LABELS: Record<CellKind, string> = {
  live: 'Live',
  'in-flight': 'In flight',
  'was-here': 'Was here',
  failed: 'Failed',
  'no-op': 'No-op',
  'not-reached': 'Not reached',
};

/* Detail drawer resize bounds (px) and the localStorage key for persistence. */
export const DRAWER_MIN_WIDTH = 320;
export const DRAWER_MAX_WIDTH = 760;
export const DRAWER_DEFAULT_WIDTH = 420;
export const DRAWER_WIDTH_KEY = 'hp-drawer-width';

export const healthIcon: Record<HealthKey, React.ReactNode> = {
  success: <FaCheckCircle aria-hidden="true" />,
  failure: <FaTimesCircle aria-hidden="true" />,
  pending: <FaSpinner className="fa-spin" aria-hidden="true" />,
  unknown: <FaQuestionCircle aria-hidden="true" />,
};

/** Plain-language explanation of what a cell's status means in a given env,
 *  shown as the hover tooltip on the status pill. */
export function cellPillTooltip(cell: CellState, branch: string): string {
  switch (cell.kind) {
    case 'live':
      return `Currently live in ${branch}`;
    case 'in-flight':
      return cell.isProposed
        ? `Proposed for ${branch} — promotion pending`
        : `Open promotion PR into ${branch}`;
    case 'was-here':
      return `Was live in ${branch}, since replaced by a newer commit`;
    case 'failed':
      return `Checks failed in ${branch}`;
    case 'no-op':
      return cell.noopNote || `No change in ${branch}`;
    case 'not-reached':
      return `Never reached ${branch}`;
    default:
      return '';
  }
}

/** One-line health status for the live strip: the health word plus the most
 *  relevant check count (failures > pending > passing), e.g. "Healthy · 2 passing"
 *  or "Failed · 1 failing". Falls back to just the health word when no checks. */
export function healthSummary(health: HealthKey, statuses: CommitStatus[]): string {
  const label = HEALTH_LABELS[health];
  if (statuses.length === 0) return label;
  let pass = 0,
    fail = 0,
    pend = 0;
  for (const s of statuses) {
    if (s.phase === 'success') pass++;
    else if (s.phase === 'failure') fail++;
    else if (s.phase === 'pending') pend++;
  }
  if (fail > 0) return `${label} · ${fail} failing`;
  if (pend > 0) return `${label} · ${pend} running`;
  if (pass > 0) return `${label} · ${pass} passing`;
  return label;
}
