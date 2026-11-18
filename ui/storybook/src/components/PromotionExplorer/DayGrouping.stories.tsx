import type { Meta, StoryObj } from '@storybook/react-vite';
import PromotionExplorer from '@lib/components/PromotionExplorer/PromotionExplorer';
import type { PromotionExplorerProps } from '@lib/components/PromotionExplorer/PromotionExplorer';
import type { PromotionStrategy } from '@shared/types/promotion';

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const REPO = 'https://github.com/argoproj-labs/gitops-promoter';

type Phase = 'success' | 'failure' | 'pending';

function at(daysAgo: number, hour: number, minute = 0): number {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - daysAgo);
  d.setHours(hour, minute);
  return Math.min(d.getTime(), Date.now() - MINUTE);
}

function onDate(year: number, month: number, day: number, hour: number, minute = 0): number {
  return new Date(year, month, day, hour, minute).getTime();
}

const SUBJECTS = [
  'feat: add retry budget to checkout client',
  'fix: resolve login redirect issue',
  'chore: upgrade dependencies to latest',
  'feat: add organization audit log',
  'fix: correct currency rounding in invoices',
  'chore: bump version',
  'refactor: split payment service config',
  'feat: enable canary for search api',
  'fix: raise memory limit for worker pods',
  'chore: rotate image pull secret reference',
];

const AUTHORS = [
  'Dana Whitfield <dana@example.com>',
  'Bob Smith <bob@example.com>',
  'Alice Chen <alice@example.com>',
  'Priya Shah <priya@example.com>',
];

interface MockCommit {
  key: number;
  subject: string;
  author: string;
  at: number | null;
  mergedAt?: number;
}

function commitsAt(times: (number | null)[]): MockCommit[] {
  return times.map((t, key) => ({
    key,
    subject: `${SUBJECTS[key % SUBJECTS.length]} (#${1000 + key})`,
    author: AUTHORS[key % AUTHORS.length],
    at: t,
  }));
}

interface RestoreItem {
  key: number;
  restoredAt: number;
}

type HistoryItem = number | RestoreItem;

interface MockEnv {
  branch: string;
  history: HistoryItem[];
  activePhase?: Phase;
  failed?: number[];
  proposed?: number;
  proposedPhase?: Phase;
  openPr?: boolean;
}

const sha = (key: number) =>
  `${(0x5f3a1c0 + key * 0x1b7d).toString(16).padStart(7, '0').slice(-7)}${'e'.repeat(33)}`;

const hydratedSha = (key: number, envIdx: number, restore = false) =>
  `${(0xa000000 + envIdx * 0x10000 + key * 2 + (restore ? 1 : 0)).toString(16)}${'0'.repeat(33)}`;

const iso = (t: number | null | undefined) => (t ? new Date(t).toISOString() : undefined);

const checks = (phase: Phase, keys: string[]) =>
  keys.map((key) => ({
    key,
    phase,
    description: `${key} ${phase}`,
    url: `https://ci.example.com/checks/${key}`,
  }));

function dayStrategy(name: string, commits: MockCommit[], envs: MockEnv[]): PromotionStrategy {
  const byKey = new Map(commits.map((c) => [c.key, c]));
  const dry = (key: number) => {
    const c = byKey.get(key)!;
    return {
      sha: sha(key),
      subject: c.subject,
      author: c.author,
      commitTime: iso(c.at),
      repoURL: REPO,
    };
  };

  const historyEntry = (item: HistoryItem, envIdx: number, env: MockEnv) => {
    if (typeof item !== 'number') {
      return {
        active: {
          dry: dry(item.key),
          hydrated: {
            sha: hydratedSha(item.key, envIdx, true),
            subject: `Restore ${env.branch} to ${sha(item.key).slice(0, 7)}`,
            commitTime: iso(item.restoredAt),
            repoURL: REPO,
          },
          commitStatuses: checks('success', ['ci/build']),
        },
        restoredFrom: hydratedSha(item.key, envIdx),
      };
    }
    const c = byKey.get(item)!;
    const mergedAt =
      c.mergedAt ?? (c.at ? Math.min(c.at + (envIdx + 1) * 15 * MINUTE, Date.now()) : undefined);
    return {
      active: {
        dry: dry(item),
        hydrated: { sha: hydratedSha(item, envIdx) },
        commitStatuses: checks(env.failed?.includes(item) ? 'failure' : 'success', ['ci/build']),
      },
      pullRequest: {
        id: `${2000 + envIdx * 100 + item}`,
        url: `${REPO}/pull/${2000 + envIdx * 100 + item}`,
        state: 'merged',
        prMergeTime: iso(mergedAt),
      },
    };
  };

  return {
    metadata: { name, namespace: 'default' },
    spec: { environments: envs.map((env) => ({ branch: env.branch })) },
    status: {
      environments: envs.map((env, envIdx) => {
        const history = env.history.map((item) => historyEntry(item, envIdx, env));
        const tip = history.at(0);
        return {
          branch: env.branch,
          active: tip
            ? {
                dry: tip.active.dry,
                hydrated: tip.active.hydrated,
                commitStatuses: checks(env.activePhase ?? 'success', ['ci/build', 'health/smoke']),
              }
            : {},
          proposed:
            env.proposed !== undefined
              ? {
                  dry: dry(env.proposed),
                  hydrated: { sha: hydratedSha(env.proposed, envIdx + 50) },
                  commitStatuses: checks(env.proposedPhase ?? 'pending', ['test/browser-suite']),
                }
              : {},
          pullRequest:
            env.openPr && env.proposed !== undefined
              ? {
                  id: `${3000 + envIdx}`,
                  url: `${REPO}/pull/${3000 + envIdx}`,
                  state: 'open',
                }
              : undefined,
          history,
          lastHealthyDryShas: [],
        };
      }),
    },
  } as unknown as PromotionStrategy;
}

const env = (name: string) => `environments/${name}`;

function cascade(name: string, commits: MockCommit[], envNames: string[]): PromotionStrategy {
  const keys = [...commits].sort((a, b) => (b.at ?? 0) - (a.at ?? 0)).map((c) => c.key);
  return dayStrategy(
    name,
    commits,
    envNames.map((n, i) => ({
      branch: env(n),
      history: keys.slice(i),
      proposed: i > 0 ? keys[i - 1] : undefined,
      openPr: i > 0,
    })),
  );
}

const THREE_ENVS = ['dev', 'staging', 'prod'];

const meta: Meta<PromotionExplorerProps> = {
  title: 'Promotion Explorer/Day grouping',
  component: PromotionExplorer,
  parameters: { layout: 'fullscreen' },
  render: (args) => (
    <div style={{ height: '100vh' }}>
      <PromotionExplorer {...args} fillViewport />
    </div>
  ),
};

export default meta;

type Story = StoryObj<PromotionExplorerProps>;

export const ManyRowsOneDay: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'single-day',
      commitsAt([
        at(1, 18, 40),
        at(1, 17, 5),
        at(1, 15, 30),
        at(1, 14, 12),
        at(1, 12, 0),
        at(1, 10, 45),
        at(1, 9, 20),
        at(1, 8, 3),
      ]),
      THREE_ENVS,
    ),
  },
};

export const SeveralDaysMultipleRows: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'several-days',
      commitsAt([
        Date.now() - 20 * MINUTE,
        Date.now() - 2 * HOUR,
        at(1, 16, 30),
        at(1, 13, 10),
        at(1, 11, 0),
        at(1, 9, 45),
        at(2, 15, 0),
        at(2, 10, 20),
        at(3, 17, 55),
        at(3, 14, 0),
        at(3, 12, 30),
        at(3, 11, 15),
        at(3, 10, 0),
        at(3, 9, 5),
        at(4, 16, 40),
        at(4, 9, 30),
      ]),
      THREE_ENVS,
    ),
  },
};

export const OneRowPerDay: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'one-per-day',
      commitsAt(Array.from({ length: 14 }, (_, i) => at(i + 1, 10 + (i % 6), (i * 7) % 60))),
      THREE_ENVS,
    ),
  },
};

export const MidnightBoundary: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'midnight',
      commitsAt([at(0, 0, 10), at(0, 0, 0), at(1, 23, 59), at(1, 23, 50), at(1, 22, 30)]),
      THREE_ENVS,
    ),
  },
};

const now = new Date();
const newYear =
  now.getMonth() === 0 && now.getDate() < 4 ? now.getFullYear() - 1 : now.getFullYear();

export const YearBoundary: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'year-boundary',
      commitsAt([
        onDate(newYear, 0, 2, 11, 0),
        onDate(newYear, 0, 1, 9, 30),
        onDate(newYear, 0, 1, 0, 5),
        onDate(newYear - 1, 11, 31, 23, 55),
        onDate(newYear - 1, 11, 31, 14, 0),
        onDate(newYear - 1, 11, 30, 16, 20),
        onDate(newYear - 1, 10, 30, 12, 0),
      ]),
      THREE_ENVS,
    ),
  },
};

const mixedCommits = commitsAt([
  Date.now() - 45 * MINUTE,
  Date.now() - 3 * HOUR,
  at(1, 15, 0),
  at(1, 10, 30),
  null,
  at(4, 11, 0),
]);
mixedCommits[4].mergedAt = at(2, 14, 15);

export const MixedStatesAndTimeSources: Story = {
  args: {
    namespace: 'default',
    strategy: dayStrategy('mixed-states', mixedCommits, [
      {
        branch: env('dev'),
        history: [1, 2, 3, 4, 5],
        failed: [3],
        proposed: 0,
        proposedPhase: 'failure',
      },
      {
        branch: env('staging'),
        history: [2, 4, 5],
        proposed: 1,
        proposedPhase: 'pending',
        openPr: true,
      },
      {
        branch: env('prod'),
        history: [{ key: 5, restoredAt: Date.now() - 90 * MINUTE }, 2, 5],
        activePhase: 'pending',
      },
    ]),
  },
};

export const UnknownDateRow: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'unknown-date',
      commitsAt([at(0, 9, 0), at(1, 14, 0), at(1, 9, 0), at(3, 12, 0), null]),
      THREE_ENVS,
    ),
  },
};

export const ManyEnvironments: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'wide-matrix',
      commitsAt([
        Date.now() - 30 * MINUTE,
        at(1, 16, 0),
        at(1, 11, 30),
        at(1, 9, 0),
        at(2, 15, 45),
        at(2, 10, 0),
        at(3, 13, 20),
        at(4, 17, 0),
        at(4, 12, 10),
        at(4, 8, 30),
      ]),
      ['dev', 'qa', 'e2e-us', 'e2e-eu', 'staging-us', 'staging-eu', 'prod-us', 'prod-eu'],
    ),
  },
};

export const LargeDataset: Story = {
  args: {
    namespace: 'default',
    strategy: cascade(
      'large-dataset',
      commitsAt(
        Array.from({ length: 66 }, (_, i) =>
          at(Math.floor(i / 3), 17 - (i % 3) * 3, (i * 13) % 60),
        ),
      ),
      ['dev', 'staging', 'prod-us', 'prod-eu'],
    ),
  },
};

export const NoHistory: Story = {
  args: {
    namespace: 'default',
    strategy: dayStrategy(
      'no-history',
      [],
      [
        { branch: env('dev'), history: [] },
        { branch: env('staging'), history: [] },
      ],
    ),
  },
};
