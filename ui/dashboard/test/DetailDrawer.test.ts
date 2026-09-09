import { describe, it, beforeEach, afterEach, expect } from 'vitest';
import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import DetailDrawer from '@lib/components/HistoryView/DetailDrawer/DetailDrawer';
import type { CellState, CommitRow, EnvColumn } from '@lib/components/HistoryView/types';
import type { CommitStatusManager, EnrichedBranchCommitStatus } from '@shared/types/promotion';
import { commitStatusPlugins } from '@shared/components/plugins';
import type { CommitStatusContext } from '@shared/components/plugins';

const timedManager: CommitStatusManager = {
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
};

const makeCell = (commitStatuses: EnrichedBranchCommitStatus[]): CellState => ({
  kind: 'live',
  commitStatuses,
  health: 'pending',
});

const row: CommitRow = {
  id: 'row-1',
  dryShaFull: 'b'.repeat(40),
  dryShaShort: 'bbbbbbb',
  subject: 'a commit',
  author: 'Someone',
  repoUrl: 'https://github.com/org/repo',
  freshestAt: 0,
  earliestAt: 0,
  cells: {},
  hasLive: true,
  hasInFlight: false,
  hasFailed: false,
  hasNoop: false,
};

const envs: EnvColumn[] = [];

const noop = () => {};

describe('DetailDrawer commit-status plugins', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
  });

  afterEach(() => {
    act(() => root?.unmount());
    container.remove();
  });

  const render = (cell: CellState) => {
    row.cells = { production: cell };
    root = createRoot(container);
    act(() => {
      root.render(
        React.createElement(DetailDrawer, {
          row,
          cell,
          branch: 'production',
          envs,
          rowsById: new Map([[row.id, row]]),
          width: 420,
          isResizing: false,
          onResizeStart: noop,
          onResizeReset: noop,
          onResizeTo: noop,
          onClose: noop,
          onJumpToRow: noop,
          onSelectCell: noop,
        }),
      );
    });
  };

  it('dispatches to the plugin row header when kind and manager are present', () => {
    render(
      makeCell([
        {
          key: 'timer',
          phase: 'pending',
          kind: 'TimedCommitStatus',
          manager: timedManager,
        },
      ]),
    );

    expect(container.querySelector('.timed-commit-status')).not.toBeNull();
    expect(container.textContent).toContain('remaining');
  });

  it('uses the default rendering for a check with no kind or manager', () => {
    render(
      makeCell([
        {
          key: 'plain-check',
          phase: 'failure',
          description: 'it broke',
          url: 'https://example.com/plain',
        },
      ]),
    );

    expect(container.querySelector('.timed-commit-status')).toBeNull();
    expect(container.querySelector('.hp-drawer__check-key')?.textContent).toBe('plain-check');
    expect(container.querySelector('.hp-drawer__check-desc')?.textContent).toBe('it broke');
    expect(container.querySelector('.hp-drawer__check-link')?.getAttribute('href')).toBe(
      'https://example.com/plain',
    );
  });

  it('renders no toggle for a check that resolves to no plugin rowContent', () => {
    render(makeCell([{ key: 'plain-check', phase: 'success' }]));

    expect(container.querySelector('.hp-drawer__check-toggle')).toBeNull();
    expect(container.querySelector('.hp-drawer__check-panel')).toBeNull();
  });

  it('renders a toggle that expands a plugin rowContent panel', () => {
    const stub = {
      rowHeader: ({ check }: CommitStatusContext) =>
        React.createElement('span', null, check.name),
      rowContent: ({ check }: CommitStatusContext) =>
        React.createElement('span', null, `details for ${check.name}`),
    };
    const previous = commitStatusPlugins.GitCommitStatus;
    commitStatusPlugins.GitCommitStatus = stub;

    try {
      render(
        makeCell([
          {
            key: 'gate',
            phase: 'pending',
            kind: 'GitCommitStatus',
            manager: timedManager,
          },
        ]),
      );

      const toggle = container.querySelector('.hp-drawer__check-toggle') as HTMLButtonElement;
      const panel = container.querySelector('.hp-drawer__check-panel') as HTMLDivElement;

      expect(toggle).not.toBeNull();
      expect(toggle.getAttribute('aria-expanded')).toBe('false');
      expect(toggle.getAttribute('aria-controls')).toBe(panel.id);
      expect(panel.hidden).toBe(true);

      act(() => toggle.click());

      expect(toggle.getAttribute('aria-expanded')).toBe('true');
      const expandedPanel = container.querySelector('.hp-drawer__check-panel') as HTMLDivElement;
      expect(expandedPanel.hidden).toBe(false);
      expect(expandedPanel.textContent).toContain('details for gate');
    } finally {
      commitStatusPlugins.GitCommitStatus = previous;
    }
  });
});
