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

const TimedCommitStatusHeader: React.FC<CommitStatusContext> = ({ check, manager }) => {
  const environment = findEnvironment(check, manager);
  const { clampedRemaining, ratio } = useTimedCommitStatusProgress(check, environment);

  const hasRenderedRef = useRef(false);
  useEffect(() => {
    hasRenderedRef.current = true;
  }, []);

  if (!environment || check.status === 'success') {
    return renderFallback(check.name, check.url);
  }

  return (
    <div style={{ display: 'inline-flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ whiteSpace: 'nowrap' }}>
        {renderFallback(check.name, check.url)} ({formatDuration(clampedRemaining)} remaining)
      </span>
      <div
        className="timed-commit-status-track"
        style={{
          position: 'relative',
          width: '100%',
          height: 4,
          borderRadius: 2,
          backgroundColor: 'rgba(66, 133, 244, 0.15)',
          overflow: 'hidden',
        }}
      >
        <div
          className="timed-commit-status-fill"
          style={{
            position: 'absolute',
            top: 0,
            left: 0,
            bottom: 0,
            width: `${ratio * 100}%`,
            borderRadius: 2,
            backgroundColor: 'rgb(66, 133, 244)',
            transition: hasRenderedRef.current ? 'width 1s linear' : 'none',
          }}
        />
      </div>
    </div>
  );
};

const TimedCommitStatus: RowPlugin = {
  rowHeader: TimedCommitStatusHeader,
};

export default TimedCommitStatus;
