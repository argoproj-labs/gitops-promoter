import type { PromotionStrategy } from '@shared/types/promotion';

const NOW = Date.parse('2026-09-30T12:00:00Z');
const HOUR = 3_600_000;
const REPO = 'https://github.com/argoproj-labs/gitops-promoter';

interface DemoCommit {
  key: string;
  subject: string;
  author: string;
  hoursAgo: number;
}

const COMMITS: DemoCommit[] = [
  {
    key: '5',
    subject: 'feat: add retry budget to checkout client',
    author: 'Dana Whitfield <dana@example.com>',
    hoursAgo: 1,
  },
  {
    key: '4',
    subject: 'fix: merge upstream pipeline optimization specs',
    author: 'Bob Smith <bob@example.com>',
    hoursAgo: 3,
  },
  {
    key: '3',
    subject: 'fix: resolve login redirect issue',
    author: 'Alice Chen <alice@example.com>',
    hoursAgo: 26,
  },
  {
    key: '2',
    subject: 'chore: upgrade dependencies to latest',
    author: 'Bob Smith <bob@example.com>',
    hoursAgo: 50,
  },
  {
    key: '1',
    subject: 'feat: add organization audit log',
    author: 'Priya Shah <priya@example.com>',
    hoursAgo: 98,
  },
];

const sha = (key: string) => key.repeat(40);

function dry(key: string) {
  const commit = COMMITS.find((c) => c.key === key)!;
  return {
    sha: sha(key),
    subject: commit.subject,
    author: commit.author,
    commitTime: new Date(NOW - commit.hoursAgo * HOUR).toISOString(),
    repoURL: REPO,
  };
}

type Phase = 'success' | 'failure' | 'pending';

const checks = (phase: Phase | undefined, keys: string[]) =>
  phase
    ? keys.map((key) => ({
        key,
        phase,
        description: `${key} ${phase}`,
        url: key.startsWith('ci/') ? `https://ci.example.com/checks/${key}` : undefined,
      }))
    : [];

export interface DemoEnv {
  branch: string;
  dependsOn?: string[];
  active?: string;
  activePhase?: Phase;
  proposed?: string;
  proposedPhase?: Phase;
  history?: string[];
  failedHistory?: string[];
}

export function demoStrategy(name: string, envs: DemoEnv[]): PromotionStrategy {
  return {
    metadata: { name, namespace: 'default' },
    spec: {
      environments: envs.map((env) => ({
        branch: env.branch,
        ...(env.dependsOn ? { dependsOn: env.dependsOn } : {}),
      })),
    },
    status: {
      environments: envs.map((env) => ({
        branch: env.branch,
        active: env.active
          ? {
              dry: dry(env.active),
              hydrated: { sha: `a${sha(env.active).slice(1)}` },
              commitStatuses: checks(env.activePhase ?? 'success', ['ci/build', 'health/smoke']),
            }
          : undefined,
        proposed: env.proposed
          ? {
              dry: dry(env.proposed),
              hydrated: { sha: `b${sha(env.proposed).slice(1)}` },
              commitStatuses: checks(env.proposedPhase ?? 'pending', ['test/browser-suite']),
            }
          : undefined,
        history: (env.history ?? []).map((key) => ({
          active: {
            dry: dry(key),
            hydrated: { sha: `a${sha(key).slice(1)}` },
            commitStatuses: checks(env.failedHistory?.includes(key) ? 'failure' : 'success', [
              'ci/build',
            ]),
          },
        })),
        lastHealthyDryShas: [],
      })),
    },
  } as unknown as PromotionStrategy;
}

const env = (name: string) => `environments/${name}`;

export const linearStrategy = demoStrategy('linear-demo', [
  { branch: env('dev'), active: '5', history: ['5', '4', '3', '2', '1'] },
  { branch: env('staging'), active: '4', proposed: '5', history: ['4', '3', '2', '1'] },
  { branch: env('prod'), active: '3', history: ['3', '2', '1'] },
]);

export const dagStrategy = demoStrategy('dag-demo', [
  { branch: env('dev'), active: '5', history: ['5', '4', '3', '2', '1'] },
  {
    branch: env('e2e-us'),
    dependsOn: [env('dev')],
    active: '4',
    proposed: '5',
    history: ['4', '3', '2', '1'],
  },
  {
    branch: env('e2e-eu'),
    dependsOn: [env('dev')],
    active: '4',
    proposed: '5',
    history: ['4', '3', '2'],
  },
  { branch: env('e2e-apac'), dependsOn: [env('dev')] },
  {
    branch: env('prod-us'),
    dependsOn: [env('e2e-us')],
    active: '3',
    history: ['3', '2', '1'],
  },
  {
    branch: env('prod-eu'),
    dependsOn: [env('e2e-us'), env('e2e-eu'), env('e2e-apac')],
    active: '2',
    activePhase: 'failure',
    history: ['2', '1'],
    failedHistory: ['2'],
  },
  { branch: env('prod-apac'), dependsOn: [env('e2e-apac')] },
]);

export const DAG_PROD_EU = env('prod-eu');
export const DAG_E2E_US = env('e2e-us');
export const DAG_COMMIT = sha('4').slice(0, 7);
