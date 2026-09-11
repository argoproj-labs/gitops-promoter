import React, { useEffect, useState } from 'react';
import type { components } from '../../../types/generated/view.gen';
import { formatDuration } from '../../../utils/util';
import type { CommitStatusContext } from '../types';
import type { RowPlugin } from '../types';
import './TimedCommitStatus.scss';

function parseGoDuration(duration: string): number {
  let totalMs = 0;
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|h|m|s)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(duration)) !== null) {
    const value = parseFloat(m[1]);
    switch (m[2]) {
      case 'h':
        totalMs += value * 3_600_000;
        break;
      case 'm':
        totalMs += value * 60_000;
        break;
      case 's':
        totalMs += value * 1_000;
        break;
      case 'ms':
        totalMs += value;
        break;
      case 'us':
      case 'µs':
        totalMs += value / 1_000;
        break;
      case 'ns':
        totalMs += value / 1_000_000;
        break;
    }
  }
  return duration.trimStart().startsWith('-') ? -totalMs : totalMs;
}

function remainingFromCommitTime(
  environment: NonNullable<ReturnType<typeof findEnvironment>>,
): number {
  const commitTimeMs = new Date(environment.commitTime).getTime();
  if (Number.isNaN(commitTimeMs)) {
    return 0;
  }
  return parseGoDuration(environment.requiredDuration) - (Date.now() - commitTimeMs);
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
  const initialRemaining = environment ? remainingFromCommitTime(environment) : 0;
  const [remaining, setRemaining] = useState<number>(initialRemaining);

  useEffect(() => {
    if (!environment || check.status !== 'pending') {
      return;
    }

    const tick = () => {
      setRemaining(remainingFromCommitTime(environment));
    };

    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, [environment, check.status]);

  const requiredDurationMs = environment ? parseGoDuration(environment.requiredDuration) : 0;
  const clampedRemaining = Math.max(remaining, 0);
  const elapsedMs = requiredDurationMs - clampedRemaining;
  const ratio =
    requiredDurationMs > 0 ? Math.min(Math.max(elapsedMs / requiredDurationMs, 0), 1) : 0;

  return { clampedRemaining, ratio };
}

const TimedCommitStatusHeader: React.FC<CommitStatusContext> = ({ check, manager }) => {
  const environment = findEnvironment(check, manager);
  const { clampedRemaining, ratio } = useTimedCommitStatusProgress(check, environment);

  if (!environment || check.status !== 'pending') {
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
