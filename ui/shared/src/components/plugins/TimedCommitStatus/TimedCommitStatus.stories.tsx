import type { Meta, StoryObj } from '@storybook/react-vite';
import { within } from 'storybook/test';
import { useEffect, useState } from 'react';
import Card from '@lib/components/Card';
import type { Environment, EnrichedBranchCommitStatus } from '../../../types/promotion';

const meta: Meta<typeof Card> = {
  title: 'Plugins/TimedCommitStatus',
  component: Card,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const toggles = await canvas.findAllByText('Current status');
    toggles.forEach((toggle) => toggle.click());
  },
};

export default meta;

type Story = StoryObj<typeof Card>;

const commitTime = new Date(Date.now() - 4 * 60 * 1000).toISOString();

const siblingChecks: EnrichedBranchCommitStatus[] = [
  {
    key: 'unit-tests',
    phase: 'success',
    description: 'All unit tests passed',
    url: 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/1111',
  },
  {
    key: 'integration-tests',
    phase: 'pending',
    description: 'Integration tests are running',
    url: 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/2222',
  },
];

function buildEnvironment(timerCheck: EnrichedBranchCommitStatus): Environment {
  return {
    branch: 'environment/staging',
    lastHealthyDryShas: [],
    active: {
      dry: {
        sha: 'a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
        author: 'Jane Doe',
        subject: 'Bump replicas to 3',
        body: 'Increases replica count for staging load testing.',
        commitTime,
        repoURL: 'https://github.com/argoproj-labs/gitops-promoter',
      },
      hydrated: {
        sha: 'b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3',
        commitTime,
      },
      commitStatuses: [...siblingChecks, timerCheck],
    },
    proposed: {
      dry: {
        sha: 'c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4',
        author: 'John Smith',
        subject: 'Add readiness probe',
        body: 'Adds a readiness probe to the staging deployment.',
        commitTime,
        repoURL: 'https://github.com/argoproj-labs/gitops-promoter',
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
      url: 'https://github.com/argoproj-labs/gitops-promoter/pull/42',
      state: 'open',
      prCreationTime: commitTime,
    },
  };
}

function timedCommitStatusManager(elapsedSeconds: number, requiredDurationSeconds: number) {
  const remainingSeconds = Math.max(requiredDurationSeconds - elapsedSeconds, 0);
  return {
    spec: {
      environments: [{ branch: 'environment/staging', duration: `${requiredDurationSeconds}s` }],
      promotionStrategyRef: { name: 'my-promotion-strategy' },
    },
    status: {
      environments: [
        {
          branch: 'environment/staging',
          commitTime: new Date(Date.now() - elapsedSeconds * 1000).toISOString(),
          phase: 'pending',
          requiredDuration: `${requiredDurationSeconds}s`,
          atMostDurationRemaining: `${remainingSeconds}s`,
          sha: 'a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
        },
      ],
    },
  };
}

export const InProgress: Story = {
  args: {
    environments: [
      buildEnvironment({
        key: 'timer',
        phase: 'pending',
        description: 'Waiting for the soak period to elapse',
        branch: 'environment/staging',
        kind: 'TimedCommitStatus',
        manager: timedCommitStatusManager(4 * 60, 10 * 60),
      }),
    ],
  },
};

export const InProgressWithLink: Story = {
  args: {
    environments: [
      buildEnvironment({
        key: 'timer',
        phase: 'pending',
        description: 'Waiting for the soak period to elapse',
        url: 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/4444',
        branch: 'environment/staging',
        kind: 'TimedCommitStatus',
        manager: timedCommitStatusManager(4 * 60, 10 * 60),
      }),
    ],
  },
};

const nearCompleteElapsedSeconds = 9 * 60 + 45;
const nearCompleteRequiredDurationSeconds = 10 * 60;

function buildNearCompleteEnvironment(): Environment {
  return buildEnvironment({
    key: 'timer',
    phase: 'pending',
    description: 'Waiting for the soak period to elapse',
    branch: 'environment/staging',
    kind: 'TimedCommitStatus',
    manager: timedCommitStatusManager(nearCompleteElapsedSeconds, nearCompleteRequiredDurationSeconds),
  });
}

function buildNearCompleteSuccessEnvironment(): Environment {
  return buildEnvironment({
    key: 'timer',
    phase: 'success',
    description: 'Soak period elapsed',
    url: 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/3333',
    branch: 'environment/staging',
    kind: 'TimedCommitStatus',
    manager: timedCommitStatusManager(nearCompleteRequiredDurationSeconds, nearCompleteRequiredDurationSeconds),
  });
}

export const NearComplete: Story = {
  render: () => {
    const [environment, setEnvironment] = useState<Environment>(buildNearCompleteEnvironment);

    useEffect(() => {
      const remainingSeconds = nearCompleteRequiredDurationSeconds - nearCompleteElapsedSeconds;
      const timeout = setTimeout(() => {
        setEnvironment(buildNearCompleteSuccessEnvironment());
      }, remainingSeconds * 1000);
      return () => clearTimeout(timeout);
    }, []);

    return <Card environments={[environment]} />;
  },
};

export const Success: Story = {
  args: {
    environments: [
      buildEnvironment({
        key: 'timer',
        phase: 'success',
        description: 'Soak period elapsed',
        url: 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/3333',
        branch: 'environment/staging',
        kind: 'TimedCommitStatus',
        manager: timedCommitStatusManager(10 * 60, 10 * 60),
      }),
    ],
  },
};

export const NoUrl: Story = {
  args: {
    environments: [
      buildEnvironment({
        key: 'timer',
        phase: 'success',
        description: 'Soak period elapsed',
        branch: 'environment/staging',
        kind: 'TimedCommitStatus',
        manager: timedCommitStatusManager(10 * 60, 10 * 60),
      }),
    ],
  },
};
