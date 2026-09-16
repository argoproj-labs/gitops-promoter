import React, { useEffect, useState } from 'react';
import type { components } from '../../../types/generated/view.gen';
import { formatDuration, parseGoDuration } from '../../../utils/util';
import type { CommitStatusContext } from '../types';
import type { RowPlugin } from '../types';
import './TimedCommitStatus.scss';

function remainingFromCommitTime(
  environment: NonNullable<ReturnType<typeof findEnvironment>>,
): number | null {
  const requiredDurationMs = parseGoDuration(environment.requiredDuration);
  if (requiredDurationMs === null) {
    return null;
  }
  const commitTimeMs = new Date(environment.commitTime).getTime();
  if (Number.isNaN(commitTimeMs)) {
    return 0;
  }
  return requiredDurationMs - (Date.now() - commitTimeMs);
}

const renderFallback = (name: string, url?: string) =>
  url ? (
    <a href={url} target="_blank" rel="noopener noreferrer" className="health-check-name-link">
      {name}
    </a>
  ) : (
    <span className="check-name-text">{name}</span>
  );

function findEnvironment(
  check: CommitStatusContext['check'],
  manager: CommitStatusContext['manager'],
) {
  const timedManager = manager as components['schemas']['TimedCommitStatus'];
  return timedManager.status?.environments?.find((env) => env.branch === check.branch);
}

function useTimedCommitStatusProgress(
  check: CommitStatusContext['check'],
  environment: ReturnType<typeof findEnvironment>,
) {
  const initialRemaining = environment ? (remainingFromCommitTime(environment) ?? 0) : 0;
  const [remaining, setRemaining] = useState<number>(initialRemaining);

  useEffect(() => {
    if (!environment || check.status !== 'pending') {
      return;
    }

    const tick = () => {
      setRemaining(remainingFromCommitTime(environment) ?? 0);
    };

    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, [environment, check.status]);

  const requiredDurationMs = environment ? parseGoDuration(environment.requiredDuration) : 0;
  const clampedRemaining = Math.max(remaining, 0);
  const elapsedMs = (requiredDurationMs ?? 0) - clampedRemaining;
  const ratio =
    requiredDurationMs !== null && requiredDurationMs > 0
      ? Math.min(Math.max(elapsedMs / requiredDurationMs, 0), 1)
      : 0;

  return { clampedRemaining, ratio, durationParsed: requiredDurationMs !== null };
}

const TimedCommitStatusHeader: React.FC<CommitStatusContext> = ({ check, manager }) => {
  const environment = findEnvironment(check, manager);
  const { clampedRemaining, ratio, durationParsed } = useTimedCommitStatusProgress(
    check,
    environment,
  );

  if (!environment || check.status !== 'pending' || !durationParsed) {
    return renderFallback(check.name, check.url);
  }

  return (
    <div className="timed-commit-status">
      <span className="timed-commit-status-label">
        {renderFallback(check.name, check.url)} ({formatDuration(clampedRemaining)} remaining)
      </span>
      <div className="timed-commit-status-track">
        <div className="timed-commit-status-fill" style={{ width: `${ratio * 100}%` }} />
      </div>
    </div>
  );
};

const TimedCommitStatus: RowPlugin = {
  rowHeader: TimedCommitStatusHeader,
};

export default TimedCommitStatus;
