import type { Meta, StoryObj } from '@storybook/react-vite';
import React from 'react';
import DetailDrawer from '@lib/components/PromotionExplorer/DetailDrawer/DetailDrawer';
import type { CellState, CommitRow, EnvColumn } from '@lib/components/PromotionExplorer/types';
import { DRAWER_DEFAULT_WIDTH } from '@lib/components/PromotionExplorer/presentation';
import type { EnrichedBranchCommitStatus } from '@shared/types/promotion';
import '@lib/components/PromotionExplorer/index.scss';

interface TimedCommitStatusArgs {
  elapsedSeconds: number;
  requiredDurationSeconds: number;
  url?: string;
  width: number;
  isResizing: boolean;
}

const BRANCH = 'environment/staging';
const REPO_URL = 'https://github.com/argoproj-labs/gitops-promoter';
const DRY_SHA = 'a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2';
const HYDRATED_SHA = 'b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3';

const meta: Meta<TimedCommitStatusArgs> = {
  title: 'Plugins/TimedCommitStatus',
  argTypes: {
    elapsedSeconds: { control: { type: 'number', min: 0, step: 1 } },
    requiredDurationSeconds: { control: { type: 'number', min: 1, step: 1 } },
    url: { control: 'text' },
    width: { control: { type: 'number', min: 320, max: 760, step: 10 } },
    isResizing: { control: 'boolean' },
  },
  args: {
    elapsedSeconds: 4 * 60,
    requiredDurationSeconds: 10 * 60,
    width: DRAWER_DEFAULT_WIDTH,
    isResizing: false,
  },
};

export default meta;

type Story = StoryObj<TimedCommitStatusArgs>;

function timedCommitStatusManager(elapsedSeconds: number, requiredDurationSeconds: number) {
  const remainingSeconds = Math.max(requiredDurationSeconds - elapsedSeconds, 0);
  return {
    spec: {
      environments: [{ branch: BRANCH, duration: `${requiredDurationSeconds}s` }],
      promotionStrategyRef: { name: 'my-promotion-strategy' },
    },
    status: {
      environments: [
        {
          branch: BRANCH,
          commitTime: new Date(Date.now() - elapsedSeconds * 1000).toISOString(),
          phase: 'pending',
          requiredDuration: `${requiredDurationSeconds}s`,
          atMostDurationRemaining: `${remainingSeconds}s`,
          sha: DRY_SHA,
        },
      ],
    },
  };
}

function pendingTimerCheck(
  elapsedSeconds: number,
  requiredDurationSeconds: number,
  url?: string,
): EnrichedBranchCommitStatus {
  return {
    key: 'soak-timer',
    phase: 'pending',
    description: 'Waiting for the soak period to elapse',
    url,
    kind: 'TimedCommitStatus',
    manager: timedCommitStatusManager(elapsedSeconds, requiredDurationSeconds),
  };
}

const siblingChecks: EnrichedBranchCommitStatus[] = [
  {
    key: 'unit-tests',
    phase: 'success',
    description: 'All unit tests passed',
    url: `${REPO_URL}/actions/runs/1111`,
  },
  {
    key: 'integration-tests',
    phase: 'pending',
    description: 'Integration tests are running',
    url: `${REPO_URL}/actions/runs/2222`,
  },
];

const drawerCommitTime = new Date(Date.now() - 26 * 60 * 1000).toISOString();

const drawerEnvs: EnvColumn[] = [
  {
    branch: 'environment/dev',
    autoMerge: true,
    color: '#1f4f5e',
    liveStatuses: [],
    liveHealth: 'success',
    proposedStatuses: [],
    proposedHealth: 'success',
  },
  {
    branch: BRANCH,
    autoMerge: false,
    color: '#6f42c1',
    liveStatuses: [],
    liveHealth: 'pending',
    proposedStatuses: [],
    proposedHealth: 'pending',
  },
  {
    branch: 'environment/prod',
    autoMerge: false,
    color: '#9a5a0f',
    liveStatuses: [],
    liveHealth: 'unknown',
    proposedStatuses: [],
    proposedHealth: 'unknown',
  },
];

function buildCell(commitStatuses: EnrichedBranchCommitStatus[]): CellState {
  return {
    kind: 'in-flight',
    commit: {
      sha: DRY_SHA,
      author: 'Jane Doe <jane@example.com>',
      subject: 'Bump replicas to 3',
      body: 'Increases replica count for staging load testing.',
      commitTime: drawerCommitTime,
      repoURL: REPO_URL,
    },
    hydrated: {
      sha: HYDRATED_SHA,
      commitTime: drawerCommitTime,
      repoURL: `${REPO_URL}-deploy`,
    },
    references: [
      {
        sha: 'f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1',
        subject: 'Raise HPA ceiling for staging',
        author: 'John Smith <john@example.com>',
        body: 'Raises the HPA ceiling so load tests do not saturate.',
        url: `${REPO_URL}/commit/f6a1b2c`,
      },
    ],
    commitStatuses,
    health: 'pending',
    pullRequest: {
      id: '42',
      url: `${REPO_URL}/pull/42`,
      state: 'open',
      prCreationTime: drawerCommitTime,
    },
    at: drawerCommitTime,
  };
}

function buildRow(cell: CellState): CommitRow {
  return {
    id: DRY_SHA,
    dryShaFull: DRY_SHA,
    dryShaShort: DRY_SHA.slice(0, 7),
    references: [],
    subject: 'Bump replicas to 3',
    author: 'Jane Doe',
    body: 'Increases replica count for staging load testing.\n\nSigned-off-by: Jane Doe <jane@example.com>',
    prId: '42',
    prUrl: `${REPO_URL}/pull/42`,
    repoUrl: REPO_URL,
    freshestAt: Date.parse(drawerCommitTime),
    earliestAt: Date.parse(drawerCommitTime),
    cells: {
      'environment/dev': {
        kind: 'live',
        commitStatuses: [],
        health: 'success',
        at: drawerCommitTime,
      },
      [BRANCH]: cell,
      'environment/prod': { kind: 'no-changes', commitStatuses: [], health: 'unknown' },
    },
    hasLive: true,
    hasInFlight: true,
    hasFailed: false,
    hasNoop: false,
  };
}

const noop = () => {};

function renderDrawer(commitStatuses: EnrichedBranchCommitStatus[], args: TimedCommitStatusArgs) {
  const cell = buildCell(commitStatuses);
  const row = buildRow(cell);
  const rowsById = new Map<string, CommitRow>([[row.id, row]]);

  return (
    <div style={{ position: 'relative', height: 680, width: args.width + 40 }}>
      <DetailDrawer
        row={row}
        cell={cell}
        branch={BRANCH}
        envs={drawerEnvs}
        rowsById={rowsById}
        width={args.width}
        isResizing={args.isResizing}
        onResizeStart={noop}
        onResizeReset={noop}
        onResizeTo={noop}
        onClose={noop}
        onJumpToRow={noop}
        onSelectCell={noop}
      />
    </div>
  );
}

export const HistoryDrawer: Story = {
  render: (args) =>
    renderDrawer(
      [
        pendingTimerCheck(args.elapsedSeconds, args.requiredDurationSeconds, args.url),
        ...siblingChecks,
      ],
      args,
    ),
};
