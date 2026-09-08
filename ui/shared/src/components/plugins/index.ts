import type { CommitStatusManagerKind } from '../../types/promotion';
import type { RowPlugin } from './types';
import TimedCommitStatus from './TimedCommitStatus/TimedCommitStatus';

export const commitStatusPlugins: Partial<Record<CommitStatusManagerKind, RowPlugin>> = {
  TimedCommitStatus,
};

export type { CommitStatusContext, RowPlugin } from './types';
export { narrowCheck } from './narrowCheck';
