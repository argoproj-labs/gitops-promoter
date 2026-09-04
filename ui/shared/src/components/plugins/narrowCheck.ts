import type { Check, CommitStatusManagerKind } from '../../types/promotion';
import type { components } from '../../types/generated/view.gen';

export type ManagerFor<K extends CommitStatusManagerKind> = K extends 'TimedCommitStatus'
  ? components['schemas']['TimedCommitStatus']
  : K extends 'GitCommitStatus'
    ? components['schemas']['GitCommitStatus']
    : K extends 'ScheduledCommitStatus'
      ? components['schemas']['ScheduledCommitStatus']
      : K extends 'ArgoCDCommitStatus'
        ? components['schemas']['ArgoCDCommitStatus']
        : K extends 'WebRequestCommitStatus'
          ? components['schemas']['WebRequestCommitStatus']
          : never;

export function narrowCheck<K extends CommitStatusManagerKind>(
  check: Check,
  kind: K,
): (Check & { kind: K; manager: ManagerFor<K> }) | undefined {
  if (check.kind !== kind) {
    return undefined;
  }
  return check as Check & { kind: K; manager: ManagerFor<K> };
}
