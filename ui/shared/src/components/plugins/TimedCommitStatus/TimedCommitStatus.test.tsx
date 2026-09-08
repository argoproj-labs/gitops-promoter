/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';
import TimedCommitStatus from './TimedCommitStatus';
import type { Check, CommitStatusManager } from '../../../types/promotion';

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

describe('TimedCommitStatus.rowHeader', () => {
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
    root.render(React.createElement(TimedCommitStatus.rowHeader, { check, manager }));
    await vi.advanceTimersByTimeAsync(0);
  };

  it('displays the initial remaining time', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    expect(container.textContent).toContain('timer');
    expect(container.textContent).toContain('4m');
  });

  it('decreases the remaining time as time passes', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    await vi.advanceTimersByTimeAsync(60_000);

    expect(container.textContent).toContain('3m');
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

  it('renders the in-progress check name as a link when a url is set', async () => {
    const manager = makeManager();
    await render(makeCheck({ url: 'https://example.com/status' }), manager);

    const link = container.querySelector('a');
    expect(link).not.toBeNull();
    expect(link?.getAttribute('href')).toBe('https://example.com/status');
    expect(link?.textContent).toBe('timer');
  });

  it('does not render its own radial (that is pendingSpinner\'s job)', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    expect(container.querySelector('.timed-commit-status-radial')).toBeNull();
  });
});

describe('TimedCommitStatus.pendingSpinner', () => {
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
    root.render(React.createElement(TimedCommitStatus.pendingSpinner!, { check, manager }));
    await vi.advanceTimersByTimeAsync(0);
  };

  it('displays the initial progress as a radial', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    const radial = container.querySelector(
      '.timed-commit-status-radial circle:last-of-type',
    ) as SVGCircleElement;
    // 20% elapsed of the 5m duration.
    expect(radial.getAttribute('stroke-dashoffset')).toBe((2 * Math.PI * 7 * 0.8).toString());
  });

  it('advances the radial as time passes', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    await vi.advanceTimersByTimeAsync(60_000);

    const radial = container.querySelector(
      '.timed-commit-status-radial circle:last-of-type',
    ) as SVGCircleElement;
    // 40% elapsed of the 5m duration.
    expect(radial.getAttribute('stroke-dashoffset')).toBe((2 * Math.PI * 7 * 0.6).toString());
  });

  it('has no transition on first render, then applies it on subsequent updates', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    const radialBefore = container.querySelector(
      '.timed-commit-status-radial circle:last-of-type',
    ) as SVGCircleElement;
    expect(radialBefore.style.transition).toBe('none');

    await vi.advanceTimersByTimeAsync(1000);

    const radialAfter = container.querySelector(
      '.timed-commit-status-radial circle:last-of-type',
    ) as SVGCircleElement;
    expect(radialAfter.style.transition).toBe('stroke-dashoffset 1s linear');
  });
});
