import type { CommitStatus } from '@shared/types/promotion';
import { formatDate } from '@shared/utils/util';
import type { CellKind, CellState, HealthKey } from './types';

export const HEALTH_LABELS: Record<HealthKey, string> = {
  success: 'Healthy',
  failure: 'Failed',
  pending: 'Pending',
  unknown: 'Unknown',
};

/** The kinds a cell is drawn as; see displayKind. */
export type DisplayCellKind = Exclude<CellKind, 'restored'>;

/**
 * The kind a cell is drawn as. A superseded restore is a second row for an earlier dry commit
 * and uses the replaced layout; the amber cell fill is what marks it as a restore.
 */
export function displayKind(kind: CellKind): DisplayCellKind {
  return kind === 'restored' ? 'was-here' : kind;
}

/**
 * Pill and badge text for a cell. `compact` shortens the empty kinds for the drawer's
 * per-environment list. A proposed commit that a RevertActiveCommit moved off the active branch is
 * labeled REVERTED rather than PROPOSED, since it will not be promoted.
 */
export function cellKindLabel(
  cell: Pick<CellState, 'kind' | 'isProposed' | 'revertedByRevertActiveCommit'>,
  compact = false,
): string {
  switch (displayKind(cell.kind)) {
    case 'live':
      return 'LIVE';
    case 'in-flight':
      if (cell.isProposed) return cell.revertedByRevertActiveCommit ? 'REVERTED' : 'PROPOSED';
      return 'PR OPEN';
    case 'was-here':
      return 'REPLACED';
    case 'failed':
      return 'FAILED';
    case 'no-op':
      return 'NO-OP';
    case 'no-changes':
      return compact ? '—' : 'NO CHANGES';
    case 'unknown-history':
      return compact ? '?' : 'HISTORY UNAVAILABLE';
  }
}

export const CELL_KIND_LABELS: Record<DisplayCellKind, string> = {
  live: 'Live',
  'in-flight': 'In flight',
  'was-here': 'Was here',
  failed: 'Failed',
  'no-op': 'No-op',
  'no-changes': 'No changes',
  'unknown-history': 'History unavailable',
};

export const DRAWER_MIN_WIDTH = 320;
export const DRAWER_MAX_WIDTH = 760;
export const DRAWER_DEFAULT_WIDTH = 420;
export const DRAWER_WIDTH_KEY = 'hp-drawer-width';

export function cellPillTooltip(cell: CellState, branch: string): string {
  switch (cell.kind) {
    case 'live': {
      if (!cell.restoredFrom) return `Currently live in ${branch}`;
      const dry = cell.commit?.sha?.slice(0, 7);
      return dry
        ? `Currently live in ${branch} on reverted dry SHA ${dry}`
        : `Currently live in ${branch} on a reverted dry SHA`;
    }
    case 'in-flight':
      return cell.isProposed
        ? `Proposed for ${branch} — promotion pending`
        : `Open promotion PR into ${branch}`;
    case 'was-here':
      return cell.replacedAt
        ? `Replaced on ${formatDate(cell.replacedAt)}`
        : `Was live in ${branch}, since replaced by a newer commit`;
    case 'restored':
      return `${branch} was manually restored to this version${
        cell.restoredFrom ? ` (${cell.restoredFrom.slice(0, 7)})` : ''
      }, not promoted`;
    case 'failed':
      return `Checks failed in ${branch}`;
    case 'no-op':
      return cell.noopNote || `No change in ${branch}`;
    case 'no-changes':
      return `Never reached ${branch}`;
    case 'unknown-history':
      return `Predates ${branch}'s available history — no longer recorded`;
    default:
      return '';
  }
}

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
