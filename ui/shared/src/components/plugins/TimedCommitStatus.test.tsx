/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';
import TimedCommitStatus from './TimedCommitStatus';
import type { Check, CommitStatusManager } from '../../types/promotion';

const makeManager = (): CommitStatusManager => ({
  spec: {
    promotionStrategyRef: { name: 'my-strategy' },
    environments: [{ branch: 'production', duration: '5m' }],
  },
  status: {
    environments: [
      {
        branch: 'production',
        sha: 'a'.repeat(40),
        commitTime: new Date(Date.now() - 60_000).toISOString(),
        requiredDuration: '5m',
        phase: 'pending',
        atMostDurationRemaining: '4m',
      },
    ],
  },
});

const makeCheck = (overrides: Partial<Check> = {}): Check => ({
  name: 'timer',
  status: 'pending',
  branch: 'production',
  kind: 'TimedCommitStatus',
  ...overrides,
});

describe('TimedCommitStatus', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    vi.useFakeTimers();
  });

  afterEach(() => {
    root?.unmount();
    container.remove();
    vi.useRealTimers();
  });

  const render = async (check: Check, manager: CommitStatusManager) => {
    root = createRoot(container);
    root.render(React.createElement(TimedCommitStatus, { check, manager }));
    await vi.advanceTimersByTimeAsync(0);
  };

  it('displays the initial remaining time and a draining progress bar', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    expect(container.textContent).toContain('timer');
    expect(container.textContent).toContain('4m');

    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('80%');
  });

  it('decreases the remaining time and progress bar width as time advances', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    await vi.advanceTimersByTimeAsync(60_000);

    expect(container.textContent).toContain('3m');
    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('60%');
  });

  it('clears the interval and stops updating on unmount', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    const clearIntervalSpy = vi.spyOn(global, 'clearInterval');
    root.unmount();
    expect(clearIntervalSpy).toHaveBeenCalled();

    const textBeforeAdvance = container.textContent;
    await vi.advanceTimersByTimeAsync(120_000);
    expect(container.textContent).toBe(textBeforeAdvance);

    clearIntervalSpy.mockRestore();
  });

  it('renders the plain fallback once the check succeeds', async () => {
    const manager = makeManager();
    await render(makeCheck({ status: 'success', url: 'https://example.com/status' }), manager);

    expect(container.querySelector('a')).not.toBeNull();
    expect(container.querySelector('a')?.getAttribute('href')).toBe('https://example.com/status');
  });
});
