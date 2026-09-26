import { describe, it, beforeEach, afterEach, expect } from 'vitest';
import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import DetailDrawer from '@lib/components/HistoryView/DetailDrawer/DetailDrawer';
import type { CellState, CommitRow, EnvColumn } from '@lib/components/HistoryView/types';
import {
  buildRevertCommitApplyCommand,
  canShowRevertCommand,
  revertCommitResourceName,
} from '@lib/components/HistoryView/revertCommand';
import { cellKindLabel } from '@lib/components/HistoryView/presentation';
import type { CommitStatusManager, EnrichedBranchCommitStatus } from '@shared/types/promotion';
import { commitStatusPlugins } from '@shared/components/plugins';
import type { CommitStatusContext } from '@shared/components/plugins';

const HYDRATED_SHA = 'abcdef0123456789abcdef0123456789abcdef01';

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

const makeCell = (
  commitStatuses: EnrichedBranchCommitStatus[],
  overrides: Partial<CellState> = {},
): CellState => ({
  kind: 'live',
  commitStatuses,
  health: 'pending',
  ...overrides,
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

const CTP_NAME = 'my-strategy-staging-a1b2c3d4';

const envs: EnvColumn[] = [
  {
    branch: 'production',
    changeTransferPolicyName: 'my-strategy-production-a1b2c3d4',
    autoMerge: false,
    color: '#1f4f5e',
    liveStatuses: [],
    liveHealth: 'unknown',
    proposedStatuses: [],
    proposedHealth: 'unknown',
  },
];

const noop = () => {};

describe('revertCommand helpers', () => {
  it('builds a kubectl apply of a RevertCommit for the environment and hydrated sha', () => {
    const cmd = buildRevertCommitApplyCommand({
      namespace: 'promoter-system',
      changeTransferPolicyName: CTP_NAME,
      branch: 'environment/staging',
      sha: HYDRATED_SHA,
    });

    expect(cmd).toBe(
      `sh -c 'kubectl apply -f - <<EOF\n${[
        'apiVersion: promoter.argoproj.io/v1alpha1',
        'kind: RevertCommit',
        'metadata:',
        '  name: revert-environment-staging-abcdef0',
        '  namespace: promoter-system',
        'spec:',
        '  changeTransferPolicyRef:',
        `    name: ${CTP_NAME}`,
        `  sha: ${HYDRATED_SHA}`,
      ].join('\n')}\nEOF'`,
    );
    expect(cmd).not.toContain('git ');
  });

  it("labels the RevertCommit with the policy's instance id so a non-default install sees it", () => {
    const cmd = buildRevertCommitApplyCommand({
      namespace: 'promoter-system',
      changeTransferPolicyName: CTP_NAME,
      instanceId: '123',
      branch: 'environment/staging',
      sha: HYDRATED_SHA,
    });

    // Quoted so a numeric-looking id stays a string label value.
    expect(cmd).toContain(
      [
        '  namespace: promoter-system',
        '  labels:',
        '    promoter.argoproj.io/instance-id: "123"',
        'spec:',
      ].join('\n'),
    );
    expect(cmd.split('\n').slice(1, -1).join('\n')).not.toContain("'");
  });

  it('wraps the apply in one POSIX sh command accepted by bash, zsh, and fish', () => {
    const cmd = buildRevertCommitApplyCommand({
      namespace: 'promoter-system',
      changeTransferPolicyName: CTP_NAME,
      branch: 'staging',
      sha: HYDRATED_SHA,
    });
    const lines = cmd.split('\n');

    expect(lines[0]).toBe("sh -c 'kubectl apply -f - <<EOF");
    expect(lines.at(-1)).toBe("EOF'");
    expect(lines.slice(1, -1).join('\n')).not.toContain("'");
  });

  it('keeps the resource name inside the DNS-1123 subdomain limit', () => {
    const name = revertCommitResourceName('environment/'.repeat(40) + 'staging', HYDRATED_SHA);
    expect(name.length).toBeLessThanOrEqual(253);
    expect(name).toMatch(/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/);
    expect(name.endsWith('-abcdef0')).toBe(true);
  });

  it('shows the command for was-here, failed, and superseded restore cells with a hydrated sha', () => {
    const hydrated = { sha: HYDRATED_SHA };
    expect(canShowRevertCommand({ kind: 'was-here', hydrated })).toBe(true);
    expect(canShowRevertCommand({ kind: 'failed', hydrated })).toBe(true);
    expect(canShowRevertCommand({ kind: 'restored', hydrated })).toBe(true);
    expect(canShowRevertCommand({ kind: 'live', hydrated })).toBe(false);
    expect(canShowRevertCommand({ kind: 'in-flight', hydrated })).toBe(false);
    expect(canShowRevertCommand({ kind: 'was-here' })).toBe(false);
  });

  it('hides the command on failed live and proposed cells', () => {
    const hydrated = { sha: HYDRATED_SHA };
    expect(canShowRevertCommand({ kind: 'failed', hydrated, isLive: true })).toBe(false);
    expect(canShowRevertCommand({ kind: 'failed', hydrated, isProposed: true })).toBe(false);
  });
});

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
      rowHeader: ({ check }: CommitStatusContext) => React.createElement('span', null, check.name),
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

describe('DetailDrawer restore command', () => {
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
          namespace: 'promoter-system',
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

  it('shows a kubectl apply of a RevertCommit for was-here cells with a hydrated sha', () => {
    render(
      makeCell([], {
        kind: 'was-here',
        health: 'success',
        hydrated: { sha: HYDRATED_SHA },
      }),
    );

    const command = container.querySelector('.hp-drawer__command');
    expect(command).not.toBeNull();
    expect(command?.textContent).toContain('kubectl apply -f -');
    expect(command?.textContent).toContain('kind: RevertCommit');
    expect(command?.textContent).toContain('name: my-strategy-production-a1b2c3d4');
    expect(command?.textContent).toContain(`sha: ${HYDRATED_SHA}`);
    expect(command?.textContent).not.toContain('git ');
    expect(container.textContent).toContain('Restore this version on production');
    expect(container.textContent).toContain('does not open a promotion pull request');
    expect(container.textContent).toContain('Delete the RevertCommit');
  });

  it('hides the restore section for live cells', () => {
    render(
      makeCell([], {
        kind: 'live',
        health: 'success',
        hydrated: { sha: HYDRATED_SHA },
      }),
    );

    expect(container.querySelector('.hp-drawer__command')).toBeNull();
    expect(container.textContent).not.toContain('Restore this version');
  });

  it('says the proposed commit is held by the RevertCommit', () => {
    render(
      makeCell([], {
        kind: 'in-flight',
        health: 'pending',
        isProposed: true,
        revertCommit: 'revert-staging',
        pullRequest: { id: '3020', url: 'https://github.com/org/repo/pull/3020', state: 'open' },
      }),
    );
    const badge = container.querySelector('.hp-drawer__kind--held');
    expect(badge?.textContent).toContain('PROPOSED');
    expect(container.querySelector('.hp-drawer__pr')?.textContent).toContain('3020');
  });

  it('labels a superseded restore cell REPLACED in the badge and the environment list', () => {
    render(
      makeCell([], {
        kind: 'restored',
        health: 'success',
        hydrated: { sha: HYDRATED_SHA },
        restoredFrom: HYDRATED_SHA,
      }),
    );

    const badge = container.querySelector('.hp-drawer__kind');
    expect(badge?.textContent).toBe('REPLACED');
    expect(badge?.classList.contains('hp-drawer__kind--was-here')).toBe(true);
    const pill = container.querySelector('.hp-drawer__presence .cell__pill');
    expect(pill?.textContent).toBe('REPLACED');
    expect(pill?.classList.contains('cell__pill--was-here')).toBe(true);
  });

  it('hides the restore section for in-flight / proposed cells', () => {
    render(
      makeCell([], {
        kind: 'in-flight',
        health: 'pending',
        isProposed: true,
        hydrated: { sha: HYDRATED_SHA },
      }),
    );

    expect(container.querySelector('.hp-drawer__command')).toBeNull();
    expect(container.textContent).not.toContain('Restore this version');
  });
});

describe('cellKindLabel', () => {
  it('gives every kind a label, with compact forms for the empty kinds', () => {
    expect(cellKindLabel({ kind: 'live' })).toBe('LIVE');
    expect(cellKindLabel({ kind: 'in-flight', isProposed: true })).toBe('PROPOSED');
    expect(cellKindLabel({ kind: 'in-flight' })).toBe('PR OPEN');
    expect(cellKindLabel({ kind: 'was-here' })).toBe('REPLACED');
    expect(cellKindLabel({ kind: 'restored' })).toBe('REPLACED');
    expect(cellKindLabel({ kind: 'failed' })).toBe('FAILED');
    expect(cellKindLabel({ kind: 'no-op' })).toBe('NO-OP');
    expect(cellKindLabel({ kind: 'no-changes' })).toBe('NO CHANGES');
    expect(cellKindLabel({ kind: 'no-changes' }, true)).toBe('—');
    expect(cellKindLabel({ kind: 'unknown-history' })).toBe('HISTORY UNAVAILABLE');
    expect(cellKindLabel({ kind: 'unknown-history' }, true)).toBe('?');
  });
});
