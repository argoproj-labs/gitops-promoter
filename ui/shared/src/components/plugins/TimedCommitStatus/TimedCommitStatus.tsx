import React, { useEffect, useState } from 'react';
import type { Check, CommitStatusManager } from '../../../types/promotion';
import type { components } from '../../../types/generated/view.gen';
import { formatDuration } from '../../../utils/util';

export interface TimedCommitStatusProps {
  check: Check;
  manager: CommitStatusManager;
}

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

const renderFallback = (check: Check) =>
  check.url ? (
    <a
      href={check.url}
      target="_blank"
      rel="noopener noreferrer"
      className="health-check-name-link"
    >
      {check.name}
    </a>
  ) : (
    <span className="check-name-text">{check.name}</span>
  );

const TimedCommitStatus: React.FC<TimedCommitStatusProps> = ({ check, manager }) => {
  const timedManager = manager as components['schemas']['TimedCommitStatus'];
  const environment = timedManager.status?.environments?.find(
    (env) => env.branch === check.branch,
  );

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

  if (!environment || check.status === 'success') {
    return renderFallback(check);
  }

  const requiredDurationMs = parseGoDuration(environment.requiredDuration);
  const clampedRemaining = Math.max(remaining, 0);
  const ratio =
    requiredDurationMs > 0 ? Math.min(Math.max(clampedRemaining / requiredDurationMs, 0), 1) : 0;

  return (
    <div>
      <span className="check-name-text">
        {check.name} ({formatDuration(clampedRemaining)} remaining)
      </span>
      <div
        className="timed-commit-status-track"
        style={{
          position: 'relative',
          height: 6,
          marginTop: 4,
          borderRadius: 3,
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
            borderRadius: 3,
            backgroundColor: 'rgb(66, 133, 244)',
            transition: 'width 1s linear',
          }}
        />
      </div>
    </div>
  );
};

export default TimedCommitStatus;
