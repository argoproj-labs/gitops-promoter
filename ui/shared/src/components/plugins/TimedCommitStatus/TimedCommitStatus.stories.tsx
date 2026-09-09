import type { Meta, StoryObj } from '@storybook/react-vite';
import { within } from 'storybook/test';
import React, { useEffect, useState } from 'react';
import Card from '@lib/components/Card';
import DetailDrawer from '@lib/components/HistoryView/DetailDrawer/DetailDrawer';
import type { CellState, CommitRow, EnvColumn } from '@lib/components/HistoryView/types';
import { DRAWER_DEFAULT_WIDTH } from '@lib/components/HistoryView/presentation';
import type { Environment, EnrichedBranchCommitStatus } from '../../../types/promotion';
import '@lib/components/HistoryView/index.scss';

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

const commitTime = new Date(Date.now() - 4 * 60 * 1000).toISOString();

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

function successTimerCheck(
  requiredDurationSeconds: number,
  url?: string,
): EnrichedBranchCommitStatus {
  return {
    key: 'soak-timer',
    phase: 'success',
    description: 'Soak period elapsed',
    url: url ?? `${REPO_URL}/actions/runs/3333`,
    kind: 'TimedCommitStatus',
    manager: timedCommitStatusManager(requiredDurationSeconds, requiredDurationSeconds),
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

const trailingCheck: EnrichedBranchCommitStatus = {
  key: 'smoke-tests',
  phase: 'pending',
  description: 'Smoke tests are queued',
  url: `${REPO_URL}/actions/runs/5555`,
};

function buildEnvironment(timerCheck: EnrichedBranchCommitStatus): Environment {
  return {
    branch: BRANCH,
    lastHealthyDryShas: [],
    active: {
      dry: {
        sha: DRY_SHA,
        author: 'Jane Doe',
        subject: 'Bump replicas to 3',
        body: 'Increases replica count for staging load testing.',
        commitTime,
        repoURL: REPO_URL,
      },
      hydrated: {
        sha: HYDRATED_SHA,
        commitTime,
      },
      commitStatuses: [...siblingChecks, timerCheck, trailingCheck],
    },
    proposed: {
      dry: {
        sha: 'c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4',
        author: 'John Smith',
        subject: 'Add readiness probe',
        body: 'Adds a readiness probe to the staging deployment.',
        commitTime,
        repoURL: REPO_URL,
      },
      hydrated: {
        sha: 'd4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5',
        commitTime,
      },
      commitStatuses: [
        {
          key: 'unit-tests',
          phase: 'success',
          description: 'All unit tests passed',
        },
      ],
    },
    pullRequest: {
      id: '42',
      url: `${REPO_URL}/pull/42`,
      state: 'open',
      prCreationTime: commitTime,
    },
  };
}

// Mirrors what the real controller does: once the required duration elapses since
// commitTime, it flips the check to success. `args`-based stories never simulate
// that transition, so the countdown would otherwise reach 0 and get stuck there.
// Re-seeds from scratch whenever the inputs change (e.g. via Storybook Controls),
// so editing elapsedSeconds/requiredDurationSeconds live restarts the timer.
function useTransitioningEnvironment(
  elapsedSeconds: number,
  requiredDurationSeconds: number,
  url?: string,
): Environment {
  const [environment, setEnvironment] = useState<Environment>(() =>
    buildEnvironment(pendingTimerCheck(elapsedSeconds, requiredDurationSeconds, url)),
  );

  useEffect(() => {
    setEnvironment(buildEnvironment(pendingTimerCheck(elapsedSeconds, requiredDurationSeconds, url)));

    const remainingSeconds = requiredDurationSeconds - elapsedSeconds;
    const timeout = setTimeout(() => {
      setEnvironment(buildEnvironment(successTimerCheck(requiredDurationSeconds, url)));
    }, remainingSeconds * 1000);
    return () => clearTimeout(timeout);
  }, [elapsedSeconds, requiredDurationSeconds, url]);

  return environment;
}

const expandHealthSummary: Story['play'] = async ({ canvasElement }) => {
  const canvas = within(canvasElement);
  const toggles = await canvas.findAllByText('Current status');
  toggles.forEach((toggle) => toggle.click());
};

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

export const OverviewCard: Story = {
  play: expandHealthSummary,
  render: ({ elapsedSeconds, requiredDurationSeconds, url }) => {
    const environment = useTransitioningEnvironment(elapsedSeconds, requiredDurationSeconds, url);
    return <Card environments={[environment]} />;
  },
};

export const OverviewCardSuccess: Story = {
  args: {
    elapsedSeconds: 10 * 60,
    requiredDurationSeconds: 10 * 60,
    url: `${REPO_URL}/actions/runs/3333`,
  },
  play: expandHealthSummary,
  render: ({ requiredDurationSeconds, url }) => (
    <Card environments={[buildEnvironment(successTimerCheck(requiredDurationSeconds, url))]} />
  ),
};

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
