import type { PromotionStrategy } from '@shared/types/promotion';

const BASE = Date.parse('2026-09-01T00:00:00Z');
const HOUR = 3_600_000;

export const sha = (c: string) => c.repeat(40);
export const rowId = (c: string) => sha(c).slice(0, 7);

export const SUBJECTS: Record<string, string> = {
  '1': 'chore: first release',
  '2': 'fix: login redirect',
  '3': 'feat: audit log',
  '4': 'feat: organization settings',
  '5': 'fix: flaky health probe',
};

export function dry(c: string) {
  return {
    sha: sha(c),
    subject: SUBJECTS[c] ?? `commit ${c}`,
    author: 'Dev One <dev@example.com>',
    commitTime: new Date(BASE + Number(c) * HOUR).toISOString(),
    repoURL: 'https://github.com/acme/app',
  };
}

type Phase = 'success' | 'failure' | 'pending';

const statuses = (phase: Phase | undefined) =>
  phase ? [{ key: 'ci/build', phase, description: `build ${phase}` }] : [];

export interface EnvSpec {
  branch: string;
  dependsOn?: string[];
  active?: string;
  activePhase?: Phase;
  proposed?: string;
  proposedPhase?: Phase;
  history?: string[];
  failedHistory?: string[];
}

export function makeStrategy(envs: EnvSpec[], name = 'demo'): PromotionStrategy {
  return {
    metadata: { name, namespace: 'default' },
    spec: {
      environments: envs.map((e) => ({
        branch: e.branch,
        ...(e.dependsOn ? { dependsOn: e.dependsOn } : {}),
      })),
    },
    status: {
      environments: envs.map((e) => ({
        branch: e.branch,
        active: e.active
          ? {
              dry: dry(e.active),
              hydrated: { sha: `h${sha(e.active).slice(1)}` },
              commitStatuses: statuses(e.activePhase ?? 'success'),
            }
          : undefined,
        proposed: e.proposed
          ? {
              dry: dry(e.proposed),
              hydrated: { sha: `p${sha(e.proposed).slice(1)}` },
              commitStatuses: statuses(e.proposedPhase ?? 'pending'),
            }
          : undefined,
        history: (e.history ?? []).map((c) => ({
          active: {
            dry: dry(c),
            hydrated: { sha: `h${sha(c).slice(1)}` },
            commitStatuses: statuses(e.failedHistory?.includes(c) ? 'failure' : 'success'),
          },
        })),
        lastHealthyDryShas: [],
      })),
    },
  } as unknown as PromotionStrategy;
}

export const LINEAR_ENVS: EnvSpec[] = [
  { branch: 'environments/dev', active: '4', history: ['4', '3', '2', '1'] },
  { branch: 'environments/staging', active: '3', proposed: '4', history: ['3', '2', '1'] },
  { branch: 'environments/prod', active: '2', history: ['2', '1'] },
];

export const DAG_ENVS: EnvSpec[] = [
  { branch: 'environments/dev', active: '4', history: ['4', '3', '2', '1'] },
  {
    branch: 'environments/e2e-us',
    dependsOn: ['environments/dev'],
    active: '3',
    proposed: '4',
    history: ['3', '2', '1'],
  },
  {
    branch: 'environments/e2e-eu',
    dependsOn: ['environments/dev'],
    active: '3',
    proposed: '4',
    history: ['3', '2'],
  },
  { branch: 'environments/e2e-apac', dependsOn: ['environments/dev'] },
  {
    branch: 'environments/prod-us',
    dependsOn: ['environments/e2e-us'],
    active: '2',
    history: ['2', '1'],
  },
  {
    branch: 'environments/prod-eu',
    dependsOn: ['environments/e2e-us', 'environments/e2e-eu', 'environments/e2e-apac'],
    active: '1',
    activePhase: 'failure',
    history: ['1'],
  },
  { branch: 'environments/prod-apac', dependsOn: ['environments/e2e-apac'] },
];

export const linearStrategy = () => makeStrategy(LINEAR_ENVS, 'linear');
export const dagStrategy = () => makeStrategy(DAG_ENVS, 'dag');
