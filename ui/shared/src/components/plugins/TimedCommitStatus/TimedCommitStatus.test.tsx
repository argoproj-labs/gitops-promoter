/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';
import TimedCommitStatus from './TimedCommitStatus';
import type { Check, CommitStatusManager } from '../../../types/promotion';

const makeManager = (
  env: { commitTime?: string; requiredDuration?: string } = {},
): CommitStatusManager => ({
  spec: {
    promotionStrategyRef: { name: 'my-strategy' },
    environments: [{ branch: 'production', duration: '5m' }],
  },
  status: {
    environments: [
      {
        branch: 'production',
        sha: 'a'.repeat(40),
        commitTime: env.commitTime ?? new Date(Date.now() - 60_000).toISOString(),
        requiredDuration: env.requiredDuration ?? '5m',
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
    root.render(React.createElement(TimedCommitStatus.rowHeader, { check, manager }));
    await vi.advanceTimersByTimeAsync(0);
  };

  it('displays the initial remaining time and a filling progress bar', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    expect(container.textContent).toContain('timer');
    expect(container.textContent).toContain('4m');

    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('20%');
  });

  it('decreases the remaining time and increases the progress bar width as time advances', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    await vi.advanceTimersByTimeAsync(60_000);

    expect(container.textContent).toContain('3m');
    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('40%');
  });

  it('clears the interval and stops updating on unmount', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval');
    root.unmount();
    expect(clearIntervalSpy).toHaveBeenCalled();

    const textBeforeAdvance = container.textContent;
    await vi.advanceTimersByTimeAsync(120_000);
    expect(container.textContent).toBe(textBeforeAdvance);

    clearIntervalSpy.mockRestore();
  });

  it('renders the plain fallback as a link once the check succeeds', async () => {
    const manager = makeManager();
    await render(makeCheck({ status: 'success', url: 'https://example.com/status' }), manager);

    expect(container.querySelector('.timed-commit-status-fill')).toBeNull();
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

  it('seeds the fill width from commitTime so the first paint matches the first tick', async () => {
    const manager = makeManager();
    const widths: string[] = [];
    const observed = new MutationObserver(() => {
      const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement | null;
      if (fill) {
        widths.push(fill.style.width);
      }
    });
    observed.observe(container, { childList: true, subtree: true, attributes: true });

    await render(makeCheck(), manager);
    observed.disconnect();

    expect(widths.length).toBeGreaterThan(0);
    expect(new Set(widths)).toEqual(new Set(['20%']));
  });

  it('leaves only the data-driven width inline', async () => {
    const manager = makeManager();
    await render(makeCheck(), manager);

    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.getAttribute('style')).toBe('width: 20%;');

    const track = container.querySelector('.timed-commit-status-track') as HTMLDivElement;
    expect(track.getAttribute('style')).toBeNull();
  });

  it('seeds zero when commitTime is unparsable', async () => {
    const manager = makeManager({ commitTime: 'not-a-date' });
    await render(makeCheck(), manager);

    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('100%');
  });

  it.each(['failure', 'unknown', 'success'])(
    'renders the plain fallback for the %s phase',
    async (status) => {
      const manager = makeManager();
      await render(makeCheck({ status: status as Check['status'] }), manager);

      expect(container.querySelector('.timed-commit-status-fill')).toBeNull();
      expect(container.textContent).toBe('timer');
    },
  );

  it('treats a sub-second requiredDuration as milliseconds, not minutes', async () => {
    const manager = makeManager({
      requiredDuration: '500ms',
      commitTime: new Date(Date.now() - 250).toISOString(),
    });
    await render(makeCheck(), manager);

    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('50%');
  });

  it('clamps the bar once the required duration has elapsed', async () => {
    const manager = makeManager({ commitTime: new Date(Date.now() - 10 * 60_000).toISOString() });
    await render(makeCheck(), manager);

    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('100%');
  });

  it('parses a negative requiredDuration as negative, leaving the bar empty', async () => {
    const manager = makeManager({ requiredDuration: '-30s', commitTime: new Date().toISOString() });
    await render(makeCheck(), manager);

    const fill = container.querySelector('.timed-commit-status-fill') as HTMLDivElement;
    expect(fill.style.width).toBe('0%');
  });
});
