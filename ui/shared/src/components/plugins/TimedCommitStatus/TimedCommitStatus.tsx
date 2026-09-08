import React, { useEffect, useRef, useState } from 'react';
import type { components } from '../../../types/generated/view.gen';
import { formatDuration } from '../../../utils/util';
import type { CommitStatusContext } from '../types';
import type { RowPlugin } from '../types';

function parseGoDuration(duration: string): number {
  let totalMs = 0;
  const re = /(\d+(?:\.\d+)?)(h|m|s)/g;
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
    }
  }
  return totalMs;
}

const renderFallback = (name: string, url?: string) =>
  url ? (
    <a href={url} target="_blank" rel="noopener noreferrer" className="health-check-name-link">
      {name}
    </a>
  ) : (
    <span className="check-name-text">{name}</span>
  );

function findEnvironment(check: CommitStatusContext['check'], manager: CommitStatusContext['manager']) {
  const timedManager = manager as components['schemas']['TimedCommitStatus'];
  return timedManager.status?.environments?.find((env) => env.branch === check.branch);
}

function useTimedCommitStatusProgress(check: CommitStatusContext['check'], environment: ReturnType<typeof findEnvironment>) {
  const initialRemaining = environment ? parseGoDuration(environment.atMostDurationRemaining) : 0;
  const [remaining, setRemaining] = useState<number>(initialRemaining);

  useEffect(() => {
    if (!environment || check.status === 'success') {
      return;
    }

    const commitTimeMs = new Date(environment.commitTime).getTime();
    const requiredDurationMs = parseGoDuration(environment.requiredDuration);

    const tick = () => {
      setRemaining(requiredDurationMs - (Date.now() - commitTimeMs));
    };

    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, [environment, check.status]);

  const requiredDurationMs = environment ? parseGoDuration(environment.requiredDuration) : 0;
  const clampedRemaining = Math.max(remaining, 0);
  const elapsedMs = requiredDurationMs - clampedRemaining;
  const ratio = requiredDurationMs > 0 ? Math.min(Math.max(elapsedMs / requiredDurationMs, 0), 1) : 0;

  return { clampedRemaining, ratio };
}

const RADIAL_RADIUS = 7;
const RADIAL_CIRCUMFERENCE = 2 * Math.PI * RADIAL_RADIUS;

const TimedCommitStatusRadial: React.FC<CommitStatusContext> = ({ check, manager }) => {
  const environment = findEnvironment(check, manager);
  const { ratio } = useTimedCommitStatusProgress(check, environment);

  const hasRenderedRef = useRef(false);
  useEffect(() => {
    hasRenderedRef.current = true;
  }, []);

  const dashoffset = RADIAL_CIRCUMFERENCE * (1 - ratio);

  return (
    <svg
      className="timed-commit-status-radial"
      width={18}
      height={18}
      viewBox="0 0 18 18"
      style={{ flexShrink: 0 }}
    >
      <circle
        cx={9}
        cy={9}
        r={RADIAL_RADIUS}
        fill="none"
        stroke="rgba(13, 173, 234, 0.15)"
        strokeWidth={2}
      />
      <circle
        cx={9}
        cy={9}
        r={RADIAL_RADIUS}
        fill="none"
        stroke="#0dadea"
        strokeWidth={2}
        strokeDasharray={RADIAL_CIRCUMFERENCE}
        strokeDashoffset={dashoffset}
        strokeLinecap="round"
        transform="rotate(-90 9 9)"
        style={{ transition: hasRenderedRef.current ? 'stroke-dashoffset 1s linear' : 'none' }}
      />
    </svg>
  );
};

const TimedCommitStatusHeader: React.FC<CommitStatusContext> = ({ check, manager }) => {
  const environment = findEnvironment(check, manager);
  const { clampedRemaining } = useTimedCommitStatusProgress(check, environment);

  if (!environment || check.status === 'success') {
    return renderFallback(check.name, check.url);
  }

  return (
    <span style={{ whiteSpace: 'nowrap' }}>
      {renderFallback(check.name, check.url)} ({formatDuration(clampedRemaining)} remaining)
    </span>
  );
};

const TimedCommitStatus: RowPlugin = {
  rowHeader: TimedCommitStatusHeader,
  pendingSpinner: TimedCommitStatusRadial,
};

export default TimedCommitStatus;
