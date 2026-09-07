import type React from 'react';
import type { Check, CommitStatusManager, CommitStatusManagerKind } from '../../types/promotion';
import TimedCommitStatus from './TimedCommitStatus/TimedCommitStatus';

export const commitStatusPlugins: Partial<
  Record<CommitStatusManagerKind, React.FC<{ check: Check; manager: CommitStatusManager }>>
> = {
  TimedCommitStatus,
};

export { narrowCheck } from './narrowCheck';
