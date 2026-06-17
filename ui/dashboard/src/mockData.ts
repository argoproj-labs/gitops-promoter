import type { PromotionStrategy } from '@shared/types/promotion';

export const MOCK_NAMESPACES: string[] = [
  'production',
  'staging',
  'dev',
];

const now = new Date();
const hoursAgo = (h: number) => new Date(now.getTime() - h * 3600_000).toISOString();
const daysAgo = (d: number) => new Date(now.getTime() - d * 86_400_000).toISOString();

const MOCK_STRATEGIES: Record<string, PromotionStrategy[]> = {
  production: [
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'web-app',
        namespace: 'production',
        uid: 'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
        resourceVersion: '54321',
        generation: 3,
        creationTimestamp: daysAgo(30),
        labels: { app: 'web-app', team: 'frontend' },
      },
      spec: {
        gitRepositoryRef: { name: 'web-app-repo' },
        activeCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }],
        proposedCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }],
        environments: [
          { branch: 'env/dev', autoMerge: true },
          { branch: 'env/staging', autoMerge: true },
          { branch: 'env/production', autoMerge: false },
        ],
      },
      status: {
        environments: [
          {
            branch: 'env/dev',
            active: {
              dry: {
                sha: 'a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
                author: 'Alice Chen <alice@example.com>',
                subject: 'feat: add user dashboard widgets',
                body: 'Added new dashboard widgets for user metrics.\n\nSigned-off-by: Alice Chen <alice@example.com>',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/web-app',
                references: [{
                  commit: {
                    sha: 'ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'feat: add user dashboard widgets',
                    body: 'Added new dashboard widgets',
                    date: hoursAgo(3),
                    url: 'https://github.com/example/web-app/commit/ff00ff00',
                    repoURL: 'https://github.com/example/web-app',
                  },
                }],
              },
              hydrated: {
                sha: 'b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3',
                author: 'Alice Chen <alice@example.com>',
                subject: 'feat: add user dashboard widgets',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/web-app',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: 'All 142 tests passed' },
              ],
            },
            proposed: {
              dry: {
                sha: 'a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
                author: 'Alice Chen <alice@example.com>',
                subject: 'feat: add user dashboard widgets',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/web-app',
              },
              hydrated: {
                sha: 'b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3',
                author: 'Alice Chen <alice@example.com>',
                subject: 'feat: add user dashboard widgets',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/web-app',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: 'All 142 tests passed' },
              ],
            },
            history: [
              {
                active: {
                  dry: {
                    sha: '1111111111111111111111111111111111111111',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'fix: resolve login redirect issue',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: '2222222222222222222222222222222222222222',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'fix: resolve login redirect issue',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '101', url: 'https://github.com/example/web-app/pull/101', prMergeTime: daysAgo(1) },
              },
              {
                active: {
                  dry: {
                    sha: '3333333333333333333333333333333333333333',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: upgrade dependencies to latest',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'failure' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: '4444444444444444444444444444444444444444',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: upgrade dependencies to latest',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '98', url: 'https://github.com/example/web-app/pull/98', prMergeTime: daysAgo(3) },
              },
              {
                active: {
                  dry: {
                    sha: '4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'fix: correct timezone handling in date picker',
                    commitTime: daysAgo(5),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: '4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'fix: correct timezone handling in date picker',
                    commitTime: daysAgo(5),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '95', url: 'https://github.com/example/web-app/pull/95', prMergeTime: daysAgo(5) },
              },
              // NO-OP: same active dry SHA as next entry — this commit didn't change env/dev
              {
                active: {
                  dry: {
                    sha: '5555555555555555555555555555555555555555',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: update staging config only',
                    commitTime: daysAgo(6),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: '5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: update staging config only',
                    commitTime: daysAgo(6),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '92', url: 'https://github.com/example/web-app/pull/92', prMergeTime: daysAgo(6) },
              },
              // INTERRUPTED: had pending proposed checks, but a newer commit superseded it
              {
                active: {
                  dry: {
                    sha: '5555555555555555555555555555555555555555',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'feat: initial dashboard scaffolding',
                    commitTime: daysAgo(7),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'abcdef1234567890abcdef1234567890abcdef12',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'feat: add notification preferences',
                    commitTime: daysAgo(7),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'pending', description: 'Build queued' },
                    { key: 'ci/test', phase: 'pending', description: 'Waiting for build' },
                  ],
                },
                pullRequest: { id: '91', url: 'https://github.com/example/web-app/pull/91', prMergeTime: daysAgo(7) },
              },
              {
                active: {
                  dry: {
                    sha: '6060606060606060606060606060606060606060',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'feat: project bootstrap',
                    commitTime: daysAgo(9),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: '7070707070707070707070707070707070707070',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'feat: project bootstrap',
                    commitTime: daysAgo(9),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '90', url: 'https://github.com/example/web-app/pull/90', prMergeTime: daysAgo(9) },
              },
            ],
          },
          {
            branch: 'env/staging',
            active: {
              dry: {
                sha: 'c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4',
                author: 'Bob Smith <bob@example.com>',
                subject: 'fix: resolve login redirect issue',
                body: 'Fixed the redirect loop on login page.\n\nSigned-off-by: Bob Smith <bob@example.com>',
                commitTime: hoursAgo(5),
                repoURL: 'https://github.com/example/web-app',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: 'All 142 tests passed' },
              ],
            },
            proposed: {
              dry: {
                sha: 'd4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5',
                author: 'Alice Chen <alice@example.com>',
                subject: 'feat: add user dashboard widgets',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/web-app',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'pending', description: 'Running integration tests...' },
              ],
            },
            pullRequest: { id: '42', url: 'https://github.com/example/web-app/pull/42' },
            history: [
              {
                active: {
                  dry: {
                    sha: 'aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'fix: resolve login redirect issue',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'fix: resolve login redirect issue',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '40', url: 'https://github.com/example/web-app/pull/40', prMergeTime: daysAgo(1) },
              },
              {
                active: {
                  dry: {
                    sha: 'cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'feat: migrate to new auth provider',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'pending', description: 'Integration tests queued' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'feat: migrate to new auth provider',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'pending' },
                  ],
                },
                pullRequest: { id: '38', url: 'https://github.com/example/web-app/pull/38', prMergeTime: daysAgo(4) },
              },
              {
                active: {
                  dry: {
                    sha: 'ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'fix: connection pool exhaustion under load',
                    commitTime: daysAgo(6),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Compilation error in pool.ts' },
                    { key: 'ci/test', phase: 'failure', description: 'Blocked by build failure' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'fix: connection pool exhaustion under load',
                    commitTime: daysAgo(6),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure' },
                    { key: 'ci/test', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '35', url: 'https://github.com/example/web-app/pull/35', prMergeTime: daysAgo(6) },
              },
              {
                active: {
                  dry: {
                    sha: 'aabb1122ccdd3344eeff5566aabb1122ccdd3344',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: upgrade Node.js to v20 LTS',
                    commitTime: daysAgo(10),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'ccdd3344eeff5566aabb1122ccdd3344eeff5566',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: upgrade Node.js to v20 LTS',
                    commitTime: daysAgo(10),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '30', url: 'https://github.com/example/web-app/pull/30', prMergeTime: daysAgo(10) },
              },
              // Shared SHA with env/dev — demonstrates cross-env SHA tracing
              {
                active: {
                  dry: {
                    sha: '5555555555555555555555555555555555555555',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'feat: initial dashboard scaffolding',
                    commitTime: daysAgo(12),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: '5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b',
                    author: 'Dan Rivera <dan@example.com>',
                    subject: 'feat: initial dashboard scaffolding',
                    commitTime: daysAgo(12),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '28', url: 'https://github.com/example/web-app/pull/28', prMergeTime: daysAgo(12) },
              },
              {
                active: {
                  dry: {
                    sha: 'eeff5566aabb1122ccdd3344eeff5566aabb1122',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'feat: initial staging environment setup',
                    commitTime: daysAgo(14),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'aabb1122ccdd3344eeff5566aabb1122ccdd3345',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'feat: initial staging environment setup',
                    commitTime: daysAgo(14),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '25', url: 'https://github.com/example/web-app/pull/25', prMergeTime: daysAgo(14) },
              },
            ],
          },
          {
            branch: 'env/production',
            active: {
              dry: {
                sha: 'e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6',
                author: 'Bob Smith <bob@example.com>',
                subject: 'fix: resolve login redirect issue',
                body: 'Fixed the redirect loop.\n\nSigned-off-by: Bob Smith <bob@example.com>',
                commitTime: daysAgo(1),
                repoURL: 'https://github.com/example/web-app',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: 'All 230 tests passed' },
              ],
            },
            proposed: {
              dry: {
                sha: 'e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6',
                author: 'Bob Smith <bob@example.com>',
                subject: 'fix: resolve login redirect issue',
                commitTime: daysAgo(1),
                repoURL: 'https://github.com/example/web-app',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: 'All 230 tests passed' },
              ],
            },
            history: [
              {
                active: {
                  dry: {
                    sha: 'prod1111aaaa2222bbbb3333cccc4444dddd5555',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'fix: patch XSS vulnerability in user input',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'prod2222bbbb3333cccc4444dddd5555eeee6666',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'fix: patch XSS vulnerability in user input',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '55', url: 'https://github.com/example/web-app/pull/55', prMergeTime: daysAgo(3) },
              },
              {
                active: {
                  dry: {
                    sha: 'prod3333cccc4444dddd5555eeee6666ffff7777',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'feat: enable feature flags for canary rollout',
                    commitTime: daysAgo(7),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'prod4444dddd5555eeee6666ffff7777aaaa8888',
                    author: 'Bob Smith <bob@example.com>',
                    subject: 'feat: enable feature flags for canary rollout',
                    commitTime: daysAgo(7),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '50', url: 'https://github.com/example/web-app/pull/50', prMergeTime: daysAgo(7) },
              },
              {
                active: {
                  dry: {
                    sha: 'prod5555eeee6666ffff7777aaaa8888bbbb9999',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: initial production deployment',
                    commitTime: daysAgo(21),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'prod6666ffff7777aaaa8888bbbb9999cccc0000',
                    author: 'Carol Lee <carol@example.com>',
                    subject: 'chore: initial production deployment',
                    commitTime: daysAgo(21),
                    repoURL: 'https://github.com/example/web-app',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
              },
            ],
          },
        ],
      },
    },
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'payment-service',
        namespace: 'production',
        uid: 'f6a1b2c3-d4e5-6789-abcd-0123456789ab',
        resourceVersion: '98765',
        generation: 7,
        creationTimestamp: daysAgo(60),
        labels: { app: 'payment-service', team: 'backend' },
      },
      spec: {
        gitRepositoryRef: { name: 'payment-service-repo' },
        activeCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/security-scan' }],
        environments: [
          { branch: 'env/dev', autoMerge: true },
          { branch: 'env/production', autoMerge: false },
        ],
      },
      status: {
        environments: [
          {
            branch: 'env/dev',
            active: {
              dry: {
                sha: 'aaaa1111bbbb2222cccc3333dddd4444eeee5555',
                author: 'Carlos Ruiz <carlos@example.com>',
                subject: 'feat: add Stripe webhook handler',
                body: 'Implemented webhook handler for payment confirmations.',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/payment-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/security-scan', phase: 'failure', description: 'Found 2 critical vulnerabilities' },
              ],
            },
            proposed: {
              dry: {
                sha: 'aaaa1111bbbb2222cccc3333dddd4444eeee5555',
                author: 'Carlos Ruiz <carlos@example.com>',
                subject: 'feat: add Stripe webhook handler',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/payment-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/security-scan', phase: 'failure', description: 'Found 2 critical vulnerabilities' },
              ],
            },
          },
          {
            branch: 'env/production',
            active: {
              dry: {
                sha: 'bbbb2222cccc3333dddd4444eeee5555ffff6666',
                author: 'Diana Lee <diana@example.com>',
                subject: 'chore: update payment SDK to v3.2',
                commitTime: daysAgo(3),
                repoURL: 'https://github.com/example/payment-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/security-scan', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 'bbbb2222cccc3333dddd4444eeee5555ffff6666',
                author: 'Diana Lee <diana@example.com>',
                subject: 'chore: update payment SDK to v3.2',
                commitTime: daysAgo(3),
                repoURL: 'https://github.com/example/payment-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/security-scan', phase: 'success' },
              ],
            },
            history: [
              {
                active: {
                  dry: {
                    sha: 'pay1111aaaa2222bbbb3333cccc4444dddd5555',
                    author: 'Diana Lee <diana@example.com>',
                    subject: 'fix: retry logic for failed transactions',
                    commitTime: daysAgo(5),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/security-scan', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pay2222bbbb3333cccc4444dddd5555eeee6666',
                    author: 'Diana Lee <diana@example.com>',
                    subject: 'fix: retry logic for failed transactions',
                    commitTime: daysAgo(5),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/security-scan', phase: 'success' },
                  ],
                },
                pullRequest: { id: '22', url: 'https://github.com/example/payment-service/pull/22', prMergeTime: daysAgo(5) },
              },
              {
                active: {
                  dry: {
                    sha: 'pay3333cccc4444dddd5555eeee6666ffff7777',
                    author: 'Carlos Ruiz <carlos@example.com>',
                    subject: 'feat: add refund API endpoint',
                    commitTime: daysAgo(8),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Timeout during integration tests' },
                    { key: 'ci/security-scan', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pay4444dddd5555eeee6666ffff7777aaaa8888',
                    author: 'Carlos Ruiz <carlos@example.com>',
                    subject: 'feat: add refund API endpoint',
                    commitTime: daysAgo(8),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure' },
                    { key: 'ci/security-scan', phase: 'success' },
                  ],
                },
                pullRequest: { id: '19', url: 'https://github.com/example/payment-service/pull/19', prMergeTime: daysAgo(8) },
              },
              {
                active: {
                  dry: {
                    sha: 'pay5555eeee6666ffff7777aaaa8888bbbb9999',
                    author: 'Diana Lee <diana@example.com>',
                    subject: 'fix: PCI compliance data masking',
                    commitTime: daysAgo(12),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Missing env var in CI config' },
                    { key: 'ci/security-scan', phase: 'failure', description: 'PAN data exposure detected' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pay6666ffff7777aaaa8888bbbb9999cccc0000',
                    author: 'Diana Lee <diana@example.com>',
                    subject: 'fix: PCI compliance data masking',
                    commitTime: daysAgo(12),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure' },
                    { key: 'ci/security-scan', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '15', url: 'https://github.com/example/payment-service/pull/15', prMergeTime: daysAgo(12) },
              },
              {
                active: {
                  dry: {
                    sha: 'pay7777aaaa8888bbbb9999cccc0000dddd1111',
                    author: 'Carlos Ruiz <carlos@example.com>',
                    subject: 'feat: Stripe webhook handler',
                    commitTime: daysAgo(18),
                    repoURL: 'https://github.com/example/payment-service',
                    references: [
                      {
                        commit: {
                          sha: 'payref1111222233334444555566667777',
                          author: 'Carlos Ruiz <carlos@example.com>',
                          subject: 'feat: Stripe webhook handler (source)',
                          date: daysAgo(18),
                          repoURL: 'https://github.com/example/payment-service',
                        },
                      },
                    ],
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/security-scan', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pay8888bbbb9999cccc0000dddd1111eeee2222',
                    author: 'Carlos Ruiz <carlos@example.com>',
                    subject: 'feat: Stripe webhook handler',
                    commitTime: daysAgo(18),
                    repoURL: 'https://github.com/example/payment-service',
                    references: [
                      {
                        commit: {
                          sha: 'payref2222333344445555666677778888',
                          author: 'Carlos Ruiz <carlos@example.com>',
                          subject: 'feat: Stripe webhook handler (source)',
                          date: daysAgo(18),
                          repoURL: 'https://github.com/example/payment-service',
                        },
                      },
                    ],
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/security-scan', phase: 'success' },
                  ],
                },
                pullRequest: { id: '10', url: 'https://github.com/example/payment-service/pull/10', prMergeTime: daysAgo(18) },
              },
            ],
          },
        ],
      },
    },
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'notification-service',
        namespace: 'production',
        uid: 'c3d4e5f6-a1b2-3456-cdef-abcdef123456',
        resourceVersion: '11111',
        generation: 2,
        creationTimestamp: daysAgo(14),
        labels: { app: 'notification-service', team: 'platform' },
      },
      spec: {
        gitRepositoryRef: { name: 'notification-svc-repo' },
        environments: [
          { branch: 'env/dev', autoMerge: true },
          { branch: 'env/staging', autoMerge: true },
          { branch: 'env/production', autoMerge: false },
        ],
      },
      status: {
        environments: [
          {
            branch: 'env/dev',
            active: {
              dry: {
                sha: 'dddd1111eeee2222ffff3333000044441111aaaa',
                author: 'Eve Park <eve@example.com>',
                subject: 'feat: add SMS notification channel',
                commitTime: hoursAgo(8),
                repoURL: 'https://github.com/example/notification-service',
              },
              commitStatuses: [],
            },
            proposed: {
              dry: {
                sha: 'eeee2222ffff3333000044441111aaaa2222bbbb',
                author: 'Frank Wu <frank@example.com>',
                subject: 'feat: add push notification support',
                commitTime: hoursAgo(3),
                repoURL: 'https://github.com/example/notification-service',
              },
              commitStatuses: [],
            },
            pullRequest: { id: '88', url: 'https://github.com/example/notification-service/pull/88' },
          },
          {
            branch: 'env/staging',
            active: {
              dry: {
                sha: 'ffff3333000044441111aaaa2222bbbb3333cccc',
                author: 'Eve Park <eve@example.com>',
                subject: 'feat: add email templates',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/notification-service',
              },
              commitStatuses: [],
            },
            proposed: {
              dry: {
                sha: 'ffff3333000044441111aaaa2222bbbb3333cccc',
                author: 'Eve Park <eve@example.com>',
                subject: 'feat: add email templates',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/notification-service',
              },
              commitStatuses: [],
            },
          },
          {
            branch: 'env/production',
            active: {
              dry: {
                sha: '000044441111aaaa2222bbbb3333cccc4444dddd',
                author: 'Eve Park <eve@example.com>',
                subject: 'feat: add email templates',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/notification-service',
              },
              commitStatuses: [],
            },
            proposed: {
              dry: {
                sha: '000044441111aaaa2222bbbb3333cccc4444dddd',
                author: 'Eve Park <eve@example.com>',
                subject: 'feat: add email templates',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/notification-service',
              },
              commitStatuses: [],
            },
          },
        ],
      },
    },
  ],

  staging: [
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'api-gateway',
        namespace: 'staging',
        uid: 'abcd1234-ef56-7890-abcd-ef1234567890',
        resourceVersion: '22222',
        generation: 4,
        creationTimestamp: daysAgo(20),
        labels: { app: 'api-gateway', team: 'platform' },
      },
      spec: {
        gitRepositoryRef: { name: 'api-gateway-repo' },
        activeCommitStatuses: [{ key: 'ci/build' }],
        environments: [
          { branch: 'env/staging', autoMerge: true },
          { branch: 'env/production', autoMerge: false },
        ],
      },
      status: {
        environments: [
          {
            branch: 'env/staging',
            active: {
              dry: {
                sha: '5555666677778888999900001111222233334444',
                author: 'Grace Kim <grace@example.com>',
                subject: 'feat: rate limiting middleware',
                commitTime: hoursAgo(4),
                repoURL: 'https://github.com/example/api-gateway',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
              ],
            },
            proposed: {
              dry: {
                sha: '5555666677778888999900001111222233334444',
                author: 'Grace Kim <grace@example.com>',
                subject: 'feat: rate limiting middleware',
                commitTime: hoursAgo(4),
                repoURL: 'https://github.com/example/api-gateway',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
              ],
            },
            history: [
              {
                active: {
                  dry: {
                    sha: 'gw01aaaa1111bbbb2222cccc3333dddd4444eeee',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'fix: timeout on large payloads',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw02bbbb2222cccc3333dddd4444eeee5555ffff',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'fix: timeout on large payloads',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                pullRequest: { id: '60', url: 'https://github.com/example/api-gateway/pull/60', prMergeTime: daysAgo(1) },
              },
              {
                active: {
                  dry: {
                    sha: 'gw03cccc3333dddd4444eeee5555ffff6666aaaa',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'feat: add request tracing headers',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Header format mismatch' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw04dddd4444eeee5555ffff6666aaaa7777bbbb',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'feat: add request tracing headers',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '58', url: 'https://github.com/example/api-gateway/pull/58', prMergeTime: daysAgo(3) },
              },
              {
                active: {
                  dry: {
                    sha: 'gw05eeee5555ffff6666aaaa7777bbbb8888cccc',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'chore: refactor middleware chain',
                    commitTime: daysAgo(5),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw06ffff6666aaaa7777bbbb8888cccc9999dddd',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'chore: refactor middleware chain',
                    commitTime: daysAgo(5),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                pullRequest: { id: '55', url: 'https://github.com/example/api-gateway/pull/55', prMergeTime: daysAgo(5) },
              },
              {
                active: {
                  dry: {
                    sha: 'gw07aaaa7777bbbb8888cccc9999dddd0000eeee',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'fix: memory leak in connection pool',
                    commitTime: daysAgo(7),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw08bbbb8888cccc9999dddd0000eeee1111ffff',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'fix: memory leak in connection pool',
                    commitTime: daysAgo(7),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                pullRequest: { id: '52', url: 'https://github.com/example/api-gateway/pull/52', prMergeTime: daysAgo(7) },
              },
              {
                active: {
                  dry: {
                    sha: 'gw09cccc9999dddd0000eeee1111ffff2222aaaa',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'feat: GraphQL gateway support',
                    commitTime: daysAgo(9),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Schema validation error' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw10dddd0000eeee1111ffff2222aaaa3333bbbb',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'feat: GraphQL gateway support',
                    commitTime: daysAgo(9),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '48', url: 'https://github.com/example/api-gateway/pull/48', prMergeTime: daysAgo(9) },
              },
              {
                active: {
                  dry: {
                    sha: 'gw11eeee1111ffff2222aaaa3333bbbb4444cccc',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'fix: SSL cert rotation automation',
                    commitTime: daysAgo(11),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Cert path not found' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw12ffff2222aaaa3333bbbb4444cccc5555dddd',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'fix: SSL cert rotation automation',
                    commitTime: daysAgo(11),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '45', url: 'https://github.com/example/api-gateway/pull/45', prMergeTime: daysAgo(11) },
              },
              {
                active: {
                  dry: {
                    sha: 'gw13aaaa3333bbbb4444cccc5555dddd6666eeee',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'feat: WebSocket proxy support',
                    commitTime: daysAgo(14),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw14bbbb4444cccc5555dddd6666eeee7777ffff',
                    author: 'Grace Kim <grace@example.com>',
                    subject: 'feat: WebSocket proxy support',
                    commitTime: daysAgo(14),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                pullRequest: { id: '40', url: 'https://github.com/example/api-gateway/pull/40', prMergeTime: daysAgo(14) },
              },
              {
                active: {
                  dry: {
                    sha: 'gw15cccc5555dddd6666eeee7777ffff8888aaaa',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'chore: initial gateway setup',
                    commitTime: daysAgo(20),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'gw16dddd6666eeee7777ffff8888aaaa9999bbbb',
                    author: 'Henry Zhang <henry@example.com>',
                    subject: 'chore: initial gateway setup',
                    commitTime: daysAgo(20),
                    repoURL: 'https://github.com/example/api-gateway',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                  ],
                },
                pullRequest: { id: '35', url: 'https://github.com/example/api-gateway/pull/35', prMergeTime: daysAgo(20) },
              },
            ],
          },
          {
            branch: 'env/production',
            active: {
              dry: {
                sha: '6666777788889999000011112222333344445555',
                author: 'Henry Zhang <henry@example.com>',
                subject: 'fix: CORS headers for mobile clients',
                commitTime: daysAgo(5),
                repoURL: 'https://github.com/example/api-gateway',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: '6666777788889999000011112222333344445555',
                author: 'Henry Zhang <henry@example.com>',
                subject: 'fix: CORS headers for mobile clients',
                commitTime: daysAgo(5),
                repoURL: 'https://github.com/example/api-gateway',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
              ],
            },
          },
        ],
      },
    },
  ],

  dev: [
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'experimental-ml-pipeline',
        namespace: 'dev',
        uid: 'deadbeef-cafe-babe-face-123456789abc',
        resourceVersion: '33333',
        generation: 1,
        creationTimestamp: daysAgo(2),
        labels: { app: 'ml-pipeline', team: 'data-science' },
      },
      spec: {
        gitRepositoryRef: { name: 'ml-pipeline-repo' },
        activeCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/model-validation' }],
        environments: [
          { branch: 'env/dev', autoMerge: true },
        ],
      },
      status: {
        environments: [
          {
            branch: 'env/dev',
            active: {
              dry: {
                sha: '7777888899990000aaaabbbbccccddddeeee1111',
                author: 'Ivy Patel <ivy@example.com>',
                subject: 'feat: add model v2 training pipeline',
                body: 'New training pipeline with improved accuracy.\n\nModel-accuracy: 94.2%',
                commitTime: hoursAgo(6),
                repoURL: 'https://github.com/example/ml-pipeline',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/model-validation', phase: 'pending', description: 'Validating model accuracy...' },
              ],
            },
            proposed: {
              dry: {
                sha: '8888999900001111aaaabbbbccccddddeeee2222',
                author: 'Jake Torres <jake@example.com>',
                subject: 'feat: hyperparameter tuning automation',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/ml-pipeline',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'pending', description: 'Building...' },
                { key: 'ci/model-validation', phase: 'pending', description: 'Waiting for build...' },
              ],
            },
            pullRequest: { id: '7', url: 'https://github.com/example/ml-pipeline/pull/7' },
            history: [
              {
                active: {
                  dry: {
                    sha: 'ml01aaaa1111bbbb2222cccc3333dddd4444eeee',
                    author: 'Ivy Patel <ivy@example.com>',
                    subject: 'feat: add model v1 baseline pipeline',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/ml-pipeline',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'pending', description: 'Build queued' },
                    { key: 'ci/model-validation', phase: 'pending', description: 'Waiting for build' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'ml02bbbb2222cccc3333dddd4444eeee5555ffff',
                    author: 'Ivy Patel <ivy@example.com>',
                    subject: 'feat: add model v1 baseline pipeline',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/ml-pipeline',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'pending' },
                    { key: 'ci/model-validation', phase: 'pending' },
                  ],
                },
                pullRequest: { id: '5', url: 'https://github.com/example/ml-pipeline/pull/5', prMergeTime: daysAgo(1) },
              },
              {
                active: {
                  dry: {
                    sha: 'ml03cccc3333dddd4444eeee5555ffff6666aaaa',
                    author: 'Jake Torres <jake@example.com>',
                    subject: 'chore: set up training data pipeline',
                    commitTime: daysAgo(2),
                    repoURL: 'https://github.com/example/ml-pipeline',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/model-validation', phase: 'success', description: 'Baseline accuracy 91.5%' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'ml04dddd4444eeee5555ffff6666aaaa7777bbbb',
                    author: 'Jake Torres <jake@example.com>',
                    subject: 'chore: set up training data pipeline',
                    commitTime: daysAgo(2),
                    repoURL: 'https://github.com/example/ml-pipeline',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/model-validation', phase: 'success' },
                  ],
                },
                pullRequest: { id: '3', url: 'https://github.com/example/ml-pipeline/pull/3', prMergeTime: daysAgo(2) },
              },
              {
                active: {
                  dry: {
                    sha: 'ml05eeee5555ffff6666aaaa7777bbbb8888cccc',
                    author: 'Ivy Patel <ivy@example.com>',
                    subject: 'feat: initial ML pipeline scaffold',
                    commitTime: daysAgo(2),
                    repoURL: 'https://github.com/example/ml-pipeline',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/model-validation', phase: 'success', description: 'Smoke test passed' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'ml06ffff6666aaaa7777bbbb8888cccc9999dddd',
                    author: 'Ivy Patel <ivy@example.com>',
                    subject: 'feat: initial ML pipeline scaffold',
                    commitTime: daysAgo(2),
                    repoURL: 'https://github.com/example/ml-pipeline',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/model-validation', phase: 'success' },
                  ],
                },
                pullRequest: { id: '1', url: 'https://github.com/example/ml-pipeline/pull/1', prMergeTime: daysAgo(2) },
              },
            ],
          },
        ],
      },
    },
  ],
};

export function getMockNamespaces(): string[] {
  return MOCK_NAMESPACES;
}

export function getMockStrategies(namespace: string): PromotionStrategy[] {
  return MOCK_STRATEGIES[namespace] || MOCK_STRATEGIES['production'] || [];
}
