import type React from 'react';
import type { Check, CommitStatusManager } from '../../types/promotion';

export interface CommitStatusContext {
  check: Check;
  manager: CommitStatusManager;
}

export interface RowPlugin {
  rowHeader: React.FC<CommitStatusContext>;
  rowContent?: React.FC<CommitStatusContext>;
}
