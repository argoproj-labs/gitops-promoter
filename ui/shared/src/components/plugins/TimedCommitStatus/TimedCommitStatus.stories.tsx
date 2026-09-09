import type { Meta, StoryObj } from '@storybook/react-vite';
import { within } from 'storybook/test';
import { useEffect, useState } from 'react';
import Card from '@lib/components/Card';
import type { Environment, EnrichedBranchCommitStatus } from '../../../types/promotion';

interface TimedCommitStatusArgs {
  elapsedSeconds: number;
  requiredDurationSeconds: number;
  url?: string;
}

const meta: Meta<TimedCommitStatusArgs> = {
  title: 'Plugins/TimedCommitStatus',
  argTypes: {
    elapsedSeconds: { control: { type: 'number', min: 0, step: 1 } },
    requiredDurationSeconds: { control: { type: 'number', min: 1, step: 1 } },
    url: { control: 'text' },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const toggles = await canvas.findAllByText('Current status');
    toggles.forEach((toggle) => toggle.click());
  },
};

export default meta;

type Story = StoryObj<TimedCommitStatusArgs>;

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

const trailingCheck: EnrichedBranchCommitStatus = {
  key: 'smoke-tests',
  phase: 'pending',
  description: 'Smoke tests are queued',
  url: 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/5555',
};

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
      commitStatuses: [...siblingChecks, timerCheck, trailingCheck],
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

function buildTransitioningEnvironment(
  elapsedSeconds: number,
  requiredDurationSeconds: number,
  url?: string,
): Environment {
  return buildEnvironment({
    key: 'timer',
    phase: 'pending',
    description: 'Waiting for the soak period to elapse',
    url,
    branch: 'environment/staging',
    kind: 'TimedCommitStatus',
    manager: timedCommitStatusManager(elapsedSeconds, requiredDurationSeconds),
  });
}

function buildTransitionedSuccessEnvironment(requiredDurationSeconds: number, url?: string): Environment {
  return buildEnvironment({
    key: 'timer',
    phase: 'success',
    description: 'Soak period elapsed',
    url: url ?? 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/3333',
    branch: 'environment/staging',
    kind: 'TimedCommitStatus',
    manager: timedCommitStatusManager(requiredDurationSeconds, requiredDurationSeconds),
  });
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
    buildTransitioningEnvironment(elapsedSeconds, requiredDurationSeconds, url),
  );

  useEffect(() => {
    setEnvironment(buildTransitioningEnvironment(elapsedSeconds, requiredDurationSeconds, url));

    const remainingSeconds = requiredDurationSeconds - elapsedSeconds;
    const timeout = setTimeout(() => {
      setEnvironment(buildTransitionedSuccessEnvironment(requiredDurationSeconds, url));
    }, remainingSeconds * 1000);
    return () => clearTimeout(timeout);
  }, [elapsedSeconds, requiredDurationSeconds, url]);

  return environment;
}

export const InProgress: Story = {
  args: {
    elapsedSeconds: 4 * 60,
    requiredDurationSeconds: 10 * 60,
  },
  render: ({ elapsedSeconds, requiredDurationSeconds, url }) => {
    const environment = useTransitioningEnvironment(elapsedSeconds, requiredDurationSeconds, url);
    return <Card environments={[environment]} />;
  },
};

export const Success: Story = {
  args: {
    elapsedSeconds: 10 * 60,
    requiredDurationSeconds: 10 * 60,
    url: 'https://github.com/argoproj-labs/gitops-promoter/actions/runs/3333',
  },
  render: ({ elapsedSeconds, requiredDurationSeconds, url }) => (
    <Card
      environments={[
        buildEnvironment({
          key: 'timer',
          phase: 'success',
          description: 'Soak period elapsed',
          url,
          branch: 'environment/staging',
          kind: 'TimedCommitStatus',
          manager: timedCommitStatusManager(elapsedSeconds, requiredDurationSeconds),
        }),
      ]}
    />
  ),
};
