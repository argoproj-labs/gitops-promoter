import type { PromotionStrategy } from '@shared/types/promotion';

export const MOCK_NAMESPACES: string[] = [
  'production',
  'staging',
  'dev',
  'demo',
  'demo-scenarios',
];

const now = new Date();
const hoursAgo = (h: number) => new Date(now.getTime() - h * 3600_000).toISOString();
const daysAgo = (d: number) => new Date(now.getTime() - d * 86_400_000).toISOString();

/**
 * Releases for the catalog-service happy-path story. Each release rides a single
 * dry SHA through dev → staging → production (different merge ages per env), so
 * every row fills across all three columns in the history matrix.
 */
const STANDARD_RELEASES: Array<{
  n: number;
  sha: string;
  author: string;
  subject: string;
  body: string;
  dev: number; // days ago it landed in dev
  staging: number; // days ago it reached staging
  prod: number; // days ago it reached production
}> = [
  { n: 6, sha: 's6d00006000000000000000000000000release6', author: 'Umar Khan <umar@example.com>', subject: 'feat: bulk price import', body: 'Adds a bulk price import that accepts a CSV and updates catalog\nprices in batches, validating each row before committing.', dev: 1, staging: 1, prod: 0 },
  { n: 5, sha: 's5d00005000000000000000000000000release5', author: 'Vera Lopez <vera@example.com>', subject: 'feat: per-region catalog availability', body: 'Catalog availability can now vary by region, so a product can be\nlisted in one market and hidden in another.', dev: 3, staging: 2, prod: 2 },
  { n: 4, sha: 's4d00004000000000000000000000000release4', author: 'Tara Singh <tara@example.com>', subject: 'fix: stale cache on price updates', body: "Price updates didn't invalidate the cache, so shoppers saw stale\nprices. The cache entry is now busted on every price write.", dev: 5, staging: 4, prod: 3 },
  { n: 3, sha: 's3d00003000000000000000000000000release3', author: 'Umar Khan <umar@example.com>', subject: 'feat: product variant grouping', body: 'Groups product variants (size, colour) under a single listing so\nshoppers pick options instead of seeing duplicate products.', dev: 8, staging: 7, prod: 6 },
  { n: 2, sha: 's2d00002000000000000000000000000release2', author: 'Vera Lopez <vera@example.com>', subject: 'chore: migrate catalog to postgres 16', body: 'Moves the catalog database from Postgres 14 to 16. Includes the\nupgrade runbook and a tested rollback path.', dev: 12, staging: 11, prod: 10 },
  { n: 1, sha: 's1d00001000000000000000000000000release1', author: 'Tara Singh <tara@example.com>', subject: 'feat: initial catalog service', body: 'First cut of the catalog service: product CRUD, search and the\nbaseline schema. Enough to stand the service up.', dev: 20, staging: 19, prod: 18 },
];

// Realistic-looking commit content to draw from when synthesizing a release
// lineage longer than the fixed STANDARD_RELEASES set.
const WIDE_AUTHORS = [
  'Umar Khan <umar@example.com>',
  'Vera Lopez <vera@example.com>',
  'Tara Singh <tara@example.com>',
  'Alice Chen <alice@example.com>',
  'Ivy Patel <ivy@example.com>',
];
const WIDE_SUBJECTS = [
  { subject: 'feat: connection pool warmup', body: 'Pre-warms upstream connection pools on boot so the first requests\nafter a deploy no longer eat cold-start latency.' },
  { subject: 'fix: retry budget leak under load', body: 'The retry budget was not being released on early cancellation,\nstarving healthy routes during spikes. Now released in all paths.' },
  { subject: 'feat: per-route rate limiting', body: 'Adds configurable per-route rate limits with a token-bucket\nlimiter, so a noisy route can be capped without touching others.' },
  { subject: 'chore: bump envoy sidecar to 1.31', body: 'Routine sidecar upgrade. Picks up the upstream h2 keepalive fix\nand drops a patched CVE from the base image.' },
  { subject: 'feat: circuit breaker per upstream', body: 'Each upstream now trips independently, so one failing backend no\nlonger opens the breaker for the whole mesh.' },
  { subject: 'fix: header casing on forwarded reqs', body: 'Forwarded requests lower-cased a header some legacy backends\nrequired verbatim. Original casing is now preserved.' },
  { subject: 'feat: mTLS cert auto-rotation', body: 'Mesh certs rotate automatically ahead of expiry with a staggered\nschedule so rotations never align across the fleet.' },
  { subject: 'perf: zero-copy body streaming', body: 'Large request bodies now stream without an intermediate buffer,\ncutting p99 memory on upload-heavy routes.' },
];

/**
 * Builds a happy-path strategy with an arbitrary number of environments so the
 * history matrix can be stress-tested horizontally. Environments form a linear
 * pipeline (env-01 → env-02 → …); a synthesized release lineage — sized to the
 * pipeline so every column stays populated — rides down the chain, arriving one
 * day later per stage. The active commit walks the leading edge stage-by-stage,
 * so each env sits on a distinct release with real history depth behind it: a
 * full, wide, filled matrix with a natural waterfall front.
 */
function buildWideStrategy(name: string, envCount: number): PromotionStrategy {
  const repoURL = `https://github.com/example/${name}`;
  const envName = (i: number) => `env/${String(i + 1).padStart(2, '0')}`;
  const okStatuses = [
    { key: 'ci/build', phase: 'success' as const },
    { key: 'ci/test', phase: 'success' as const },
  ];

  // Synthesize enough releases that even the last env has history behind it.
  // Release 0 is newest (smallest dev age); later releases are progressively
  // older. Content rotates through the pools so nothing repeats back-to-back.
  const releaseCount = envCount + 4;
  const RELEASES = Array.from({ length: releaseCount }, (_, i) => {
    const c = WIDE_SUBJECTS[i % WIDE_SUBJECTS.length];
    return {
      n: releaseCount - i,
      sha: `wide${String(releaseCount - i).padStart(2, '0')}`.padEnd(40, '0'),
      author: WIDE_AUTHORS[i % WIDE_AUTHORS.length],
      subject: c.subject,
      body: c.body,
      dev: i, // days ago it landed in dev (env-01); older releases = larger i
    };
  });

  // Days-ago a given release reached a given env: dev age plus one day of
  // promotion lag per downstream env.
  const ageAt = (releaseDev: number, envIdx: number) => releaseDev + envIdx;

  const environments = Array.from({ length: envCount }, (_, envIdx) => {
    // Active = the release currently live in this env; proposed = the next one
    // promoting in from upstream (the first env leads with the newest release).
    const activeRelease = RELEASES[envIdx];
    const proposedRelease = RELEASES[Math.max(0, envIdx - 1)];
    const isLeadingEdge = envIdx === 0;

    return {
      branch: envName(envIdx),
      active: {
        dry: {
          sha: activeRelease.sha,
          author: activeRelease.author,
          subject: activeRelease.subject,
          body: activeRelease.body,
          commitTime: daysAgo(ageAt(activeRelease.dev, envIdx)),
          repoURL,
        },
        commitStatuses: okStatuses,
      },
      proposed: {
        dry: {
          sha: proposedRelease.sha,
          author: proposedRelease.author,
          subject: proposedRelease.subject,
          body: proposedRelease.body,
          commitTime: daysAgo(ageAt(proposedRelease.dev, envIdx)),
          repoURL,
        },
        commitStatuses: okStatuses,
      },
      pullRequest: isLeadingEdge
        ? undefined
        : {
            id: `${700 + envIdx}`,
            url: `${repoURL}/pull/${700 + envIdx}`,
            state: 'open' as const,
          },
      // Older releases that already flowed through this env.
      history: RELEASES.slice(envIdx + 1).map((r) => ({
        active: {
          dry: {
            sha: r.sha,
            author: r.author,
            subject: r.subject,
            body: r.body,
            commitTime: daysAgo(ageAt(r.dev, envIdx)),
            repoURL,
          },
          commitStatuses: okStatuses,
        },
        proposed: {
          hydrated: {
            sha: `hydr${r.sha.slice(4)}`,
            author: r.author,
            subject: r.subject,
            body: r.body,
            commitTime: daysAgo(ageAt(r.dev, envIdx)),
            repoURL,
          },
          commitStatuses: okStatuses,
        },
        pullRequest: {
          id: `${800 + envIdx * 100 + r.n}`,
          url: `${repoURL}/pull/${800 + envIdx * 100 + r.n}`,
          prMergeTime: daysAgo(ageAt(r.dev, envIdx)),
          state: 'merged' as const,
        },
      })),
    };
  });

  return {
    kind: 'PromotionStrategy',
    apiVersion: 'promoter.argoproj.io/v1alpha1',
    metadata: {
      name,
      namespace: 'demo-scenarios',
      uid: 'wide0001-1111-2222-3333-444455556666',
      resourceVersion: '90000',
      generation: 42,
      creationTimestamp: daysAgo(90),
      labels: { app: name, team: 'platform' },
    },
    spec: {
      gitRepositoryRef: { name: `${name}-repo` },
      activeCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }],
      proposedCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }],
      environments: environments.map((e, i) => ({ branch: e.branch, autoMerge: i < envCount - 1 })),
    },
    status: { environments },
  };
}

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
                body:
                  'Adds the first set of dashboard widgets for user-facing metrics.\n' +
                  'Layout is responsive and each widget loads independently.',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/web-app',
                references: [{
                  commit: {
                    sha: 'ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00',
                    author: 'Alice Chen <alice@example.com>',
                    subject: 'feat: add user dashboard widgets',
                    body:
                      'Adds the first set of dashboard widgets for user-facing metrics.\n' +
                      'Layout is responsive and each widget loads independently.',
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
                body:
                  'Adds the first set of dashboard widgets for user-facing metrics.\n' +
                  'Layout is responsive and each widget loads independently.',
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
                body:
                  'Adds the first set of dashboard widgets for user-facing metrics.\n' +
                  'Layout is responsive and each widget loads independently.',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/web-app',
              },
              hydrated: {
                sha: 'b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3',
                author: 'Alice Chen <alice@example.com>',
                subject: 'feat: add user dashboard widgets',
                body:
                  'Adds the first set of dashboard widgets for user-facing metrics.\n' +
                  'Layout is responsive and each widget loads independently.',
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
                    body:
                      'Login bounced users into a redirect loop when the return URL was\n' +
                      'missing. The fallback now sends them to the home page.',
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
                    body:
                      'Login bounced users into a redirect loop when the return URL was\n' +
                      'missing. The fallback now sends them to the home page.',
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
                    body:
                      'Routine dependency refresh to clear known advisories. No public\n' +
                      'API changes; the lockfile is regenerated.',
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
                    body:
                      'Routine dependency refresh to clear known advisories. No public\n' +
                      'API changes; the lockfile is regenerated.',
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
                    body:
                      'The date picker assumed local time and shifted dates by a day for\n' +
                      'some offsets. Dates are now handled in UTC consistently.',
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
                    body:
                      'The date picker assumed local time and shifted dates by a day for\n' +
                      'some offsets. Dates are now handled in UTC consistently.',
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
                    body:
                      'Tweaks staging-only tuning (log level, replica count). Does not\n' +
                      'touch application code, so other environments are unaffected.',
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
                    body:
                      'Tweaks staging-only tuning (log level, replica count). Does not\n' +
                      'touch application code, so other environments are unaffected.',
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
                    body:
                      'Scaffolds the dashboard app with routing, layout and a placeholder\n' +
                      'home view. No real data wired in yet.',
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
                    body:
                      'Lets users opt in and out of each notification channel from the\n' +
                      'account settings page. Defaults preserve current behaviour.',
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
                    body:
                      'Initial project bootstrap: repo layout, CI, linting and a hello\n' +
                      'world service so everything is wired before feature work.',
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
                    body:
                      'Initial project bootstrap: repo layout, CI, linting and a hello\n' +
                      'world service so everything is wired before feature work.',
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
                body:
                  'Login bounced users into a redirect loop when the return URL was\n' +
                  'missing. The fallback now sends them to the home page.',
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
                body:
                  'Adds the first set of dashboard widgets for user-facing metrics.\n' +
                  'Layout is responsive and each widget loads independently.',
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
                    body:
                      'Login bounced users into a redirect loop when the return URL was\n' +
                      'missing. The fallback now sends them to the home page.',
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
                    body:
                      'Login bounced users into a redirect loop when the return URL was\n' +
                      'missing. The fallback now sends them to the home page.',
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
                    body:
                      'Migrates authentication to the new identity provider. Sessions are\n' +
                      'backed by the new tokens; the old flow is removed.',
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
                    body:
                      'Migrates authentication to the new identity provider. Sessions are\n' +
                      'backed by the new tokens; the old flow is removed.',
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
                    body:
                      'The pool was leaking connections on request cancellation, so it\n' +
                      'exhausted under load. Connections are now released in all paths.',
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
                    body:
                      'The pool was leaking connections on request cancellation, so it\n' +
                      'exhausted under load. Connections are now released in all paths.',
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
                    body:
                      'Moves the build and runtime images to Node 20 LTS and updates CI\n' +
                      'to match. Node 18 reaches end of life soon.',
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
                    body:
                      'Moves the build and runtime images to Node 20 LTS and updates CI\n' +
                      'to match. Node 18 reaches end of life soon.',
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
                    body:
                      'Scaffolds the dashboard app with routing, layout and a placeholder\n' +
                      'home view. No real data wired in yet.',
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
                    body:
                      'Scaffolds the dashboard app with routing, layout and a placeholder\n' +
                      'home view. No real data wired in yet.',
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
                    body:
                      'Stands up the staging environment with its own overlay, secrets\n' +
                      'and ingress so changes can be soaked before production.',
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
                    body:
                      'Stands up the staging environment with its own overlay, secrets\n' +
                      'and ingress so changes can be soaked before production.',
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
                body:
                  'Login bounced users into a redirect loop when the return URL was\n' +
                  'missing. The fallback now sends them to the home page.',
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
                body:
                  'Login bounced users into a redirect loop when the return URL was\n' +
                  'missing. The fallback now sends them to the home page.',
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
                    body:
                      'User-supplied input was rendered without escaping, allowing stored\n' +
                      'XSS. Output is now escaped and inputs are validated.',
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
                    body:
                      'User-supplied input was rendered without escaping, allowing stored\n' +
                      'XSS. Output is now escaped and inputs are validated.',
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
                    body:
                      'Introduces feature flags so new behaviour can be canaried to a\n' +
                      'small percentage of traffic before a full rollout.',
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
                    body:
                      'Introduces feature flags so new behaviour can be canaried to a\n' +
                      'small percentage of traffic before a full rollout.',
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
                    body:
                      'First production rollout. Wires up the prod overlay, secrets and\n' +
                      'the HPA so the service can take live traffic.',
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
                    body:
                      'First production rollout. Wires up the prod overlay, secrets and\n' +
                      'the HPA so the service can take live traffic.',
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
                body:
                  'Handles Stripe webhook callbacks to confirm payments out of band,\n' +
                  'verifying signatures and ignoring replays.',
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
                body:
                  'Handles Stripe webhook callbacks to confirm payments out of band,\n' +
                  'verifying signatures and ignoring replays.',
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
                body:
                  'Bumps the payment SDK to 3.2 for the new idempotency-key API and\n' +
                  'drops two workarounds that are no longer needed.',
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
                body:
                  'Bumps the payment SDK to 3.2 for the new idempotency-key API and\n' +
                  'drops two workarounds that are no longer needed.',
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
                    body:
                      'Failed transactions weren\'t retried, so transient errors looked\n' +
                      'like hard failures. Adds bounded retries with backoff.',
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
                    body:
                      'Failed transactions weren\'t retried, so transient errors looked\n' +
                      'like hard failures. Adds bounded retries with backoff.',
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
                    body:
                      'Adds an endpoint to issue full and partial refunds against a\n' +
                      'captured payment, with an audit record for each refund.',
                    commitTime: daysAgo(8),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Tests timed out' },
                    { key: 'ci/security-scan', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pay4444dddd5555eeee6666ffff7777aaaa8888',
                    author: 'Carlos Ruiz <carlos@example.com>',
                    subject: 'feat: add refund API endpoint',
                    body:
                      'Adds an endpoint to issue full and partial refunds against a\n' +
                      'captured payment, with an audit record for each refund.',
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
                    body:
                      'Card numbers appeared unmasked in logs. Masks the PAN at the\n' +
                      'logging boundary so only the last four digits remain.',
                    commitTime: daysAgo(12),
                    repoURL: 'https://github.com/example/payment-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Build failed' },
                    { key: 'ci/security-scan', phase: 'failure', description: 'Security scan failed' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pay6666ffff7777aaaa8888bbbb9999cccc0000',
                    author: 'Diana Lee <diana@example.com>',
                    subject: 'fix: PCI compliance data masking',
                    body:
                      'Card numbers appeared unmasked in logs. Masks the PAN at the\n' +
                      'logging boundary so only the last four digits remain.',
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
                    body:
                      'Handles Stripe webhook callbacks to reconcile payment state,\n' +
                      'verifying signatures before trusting any event.',
                    commitTime: daysAgo(18),
                    repoURL: 'https://github.com/example/payment-service',
                    references: [
                      {
                        commit: {
                          sha: 'payref1111222233334444555566667777',
                          author: 'Carlos Ruiz <carlos@example.com>',
                          subject: 'feat: Stripe webhook handler (source)',
                          body:
                            'Source-repo change behind the promoted Stripe webhook handler.\n' +
                            'Verifies signatures and reconciles payment state.',
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
                    body:
                      'Handles Stripe webhook callbacks to reconcile payment state,\n' +
                      'verifying signatures before trusting any event.',
                    commitTime: daysAgo(18),
                    repoURL: 'https://github.com/example/payment-service',
                    references: [
                      {
                        commit: {
                          sha: 'payref2222333344445555666677778888',
                          author: 'Carlos Ruiz <carlos@example.com>',
                          subject: 'feat: Stripe webhook handler (source)',
                          body:
                            'Source-repo change behind the promoted Stripe webhook handler.\n' +
                            'Verifies signatures and reconciles payment state.',
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
                body:
                  'Adds SMS as a notification channel via the provider gateway, with\n' +
                  'opt-in handling and basic rate limiting.',
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
                body:
                  'Adds mobile push as a delivery channel alongside email, including\n' +
                  'device-token registration and a basic retry on failure.',
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
                body:
                  'Adds the transactional email templates (welcome, receipt, reset)\n' +
                  'with both HTML and plain-text variants.',
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
                body:
                  'Adds the transactional email templates (welcome, receipt, reset)\n' +
                  'with both HTML and plain-text variants.',
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
                body:
                  'Adds the transactional email templates (welcome, receipt, reset)\n' +
                  'with both HTML and plain-text variants.',
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
                body:
                  'Adds the transactional email templates (welcome, receipt, reset)\n' +
                  'with both HTML and plain-text variants.',
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
                body:
                  'Adds token-bucket rate limiting at the gateway, keyed by client,\n' +
                  'to protect downstream services from bursts.',
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
                body:
                  'Adds token-bucket rate limiting at the gateway, keyed by client,\n' +
                  'to protect downstream services from bursts.',
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
                    body:
                      'Large request bodies timed out at the gateway. Raises the body\n' +
                      'limit and streams the payload instead of buffering it.',
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
                    body:
                      'Large request bodies timed out at the gateway. Raises the body\n' +
                      'limit and streams the payload instead of buffering it.',
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
                    body:
                      'Propagates W3C trace-context headers through the gateway so a\n' +
                      'request can be followed across downstream services.',
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
                    body:
                      'Propagates W3C trace-context headers through the gateway so a\n' +
                      'request can be followed across downstream services.',
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
                    body:
                      'Collapses the duplicated auth and logging middleware into a single\n' +
                      'ordered chain. Behaviour is unchanged; this is purely cleanup.',
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
                    body:
                      'Collapses the duplicated auth and logging middleware into a single\n' +
                      'ordered chain. Behaviour is unchanged; this is purely cleanup.',
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
                    body:
                      'Idle connections were never reaped, so memory grew over time.\n' +
                      'Adds an idle timeout and a reaper to release them.',
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
                    body:
                      'Idle connections were never reaped, so memory grew over time.\n' +
                      'Adds an idle timeout and a reaper to release them.',
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
                    body:
                      'Adds a GraphQL endpoint at the gateway that stitches the existing\n' +
                      'REST services behind a single typed schema.',
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
                    body:
                      'Adds a GraphQL endpoint at the gateway that stitches the existing\n' +
                      'REST services behind a single typed schema.',
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
                    body:
                      'Cert rotation failed silently when the path changed. The renewer\n' +
                      'now resolves the path at runtime and alerts on failure.',
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
                    body:
                      'Cert rotation failed silently when the path changed. The renewer\n' +
                      'now resolves the path at runtime and alerts on failure.',
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
                    body:
                      'Adds WebSocket proxying at the gateway so realtime features can\n' +
                      'share the same entry point as HTTP traffic.',
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
                    body:
                      'Adds WebSocket proxying at the gateway so realtime features can\n' +
                      'share the same entry point as HTTP traffic.',
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
                    body:
                      'Bootstraps the gateway service with routing, health checks and a\n' +
                      'baseline Helm chart. No business logic yet.',
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
                    body:
                      'Bootstraps the gateway service with routing, health checks and a\n' +
                      'baseline Helm chart. No business logic yet.',
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
                body:
                  'Mobile clients were rejected by CORS on preflight. Adds the\n' +
                  'missing allowed headers and origins so requests succeed.',
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
                body:
                  'Mobile clients were rejected by CORS on preflight. Adds the\n' +
                  'missing allowed headers and origins so requests succeed.',
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
                body:
                  'Adds the v2 training pipeline with the reworked feature set.\n' +
                  'Offline accuracy is up meaningfully over the v1 baseline.',
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
                body:
                  'Automates the hyperparameter sweep so each training run explores\n' +
                  'the grid and reports the best configuration.',
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
                    body:
                      'Establishes the v1 baseline training pipeline end to end so later\n' +
                      'models have something to be measured against.',
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
                    body:
                      'Establishes the v1 baseline training pipeline end to end so later\n' +
                      'models have something to be measured against.',
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
                    body:
                      'Adds the ingestion job that pulls raw events into the feature store\n' +
                      'so model training has a reproducible dataset.',
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
                    body:
                      'Adds the ingestion job that pulls raw events into the feature store\n' +
                      'so model training has a reproducible dataset.',
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
                    body:
                      'Lays out the ML pipeline skeleton (ingest, train, evaluate stages)\n' +
                      'with stubs so the stages can be filled in incrementally.',
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
                    body:
                      'Lays out the ML pipeline skeleton (ingest, train, evaluate stages)\n' +
                      'with stubs so the stages can be filled in incrementally.',
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

  demo: [
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'checkout-service',
        namespace: 'demo',
        uid: 'demo0001-1111-2222-3333-444455556666',
        resourceVersion: '70001',
        generation: 12,
        creationTimestamp: daysAgo(45),
        labels: { app: 'checkout-service', team: 'commerce', tier: 'critical' },
        annotations: { 'promoter.argoproj.io/demo': 'true' },
      },
      spec: {
        gitRepositoryRef: { name: 'checkout-service-repo' },
        activeCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }, { key: 'ci/e2e' }],
        proposedCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }, { key: 'ci/e2e' }],
        environments: [
          { branch: 'env/dev', autoMerge: true },
          { branch: 'env/qa', autoMerge: true },
          { branch: 'env/staging', autoMerge: true },
          { branch: 'env/production', autoMerge: false },
        ],
      },
      status: {
        environments: [
          // env/dev — fully promoted and healthy
          {
            branch: 'env/dev',
            active: {
              dry: {
                sha: 'd00d1111e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
                author: 'Mei Lin <mei@example.com>',
                subject: 'feat: one-click checkout for returning customers',
                body:
                  'Returning customers can check out in one click using their saved\n' +
                  'payment method, falling back to the full flow on any error.',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/checkout-service',
                references: [{
                  commit: {
                    sha: 'src1111aaaa2222bbbb3333cccc4444dddd5555',
                    author: 'Mei Lin <mei@example.com>',
                    subject: 'feat: one-click checkout for returning customers',
                    body:
                      'Returning customers can check out in one click using their saved\n' +
                      'payment method, falling back to the full flow on any error.',
                    date: hoursAgo(2),
                    url: 'https://github.com/example/checkout-app/commit/src1111aaaa',
                    repoURL: 'https://github.com/example/checkout-app',
                  },
                }],
              },
              hydrated: {
                sha: 'd00d2222e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
                author: 'Mei Lin <mei@example.com>',
                subject: 'feat: one-click checkout for returning customers',
                body:
                  'Returning customers can check out in one click using their saved\n' +
                  'payment method, falling back to the full flow on any error.',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: '318 unit tests passed' },
                { key: 'ci/e2e', phase: 'success', description: 'All 24 e2e flows passed' },
              ],
            },
            proposed: {
              dry: {
                sha: 'd00d1111e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
                author: 'Mei Lin <mei@example.com>',
                subject: 'feat: one-click checkout for returning customers',
                body:
                  'Returning customers can check out in one click using their saved\n' +
                  'payment method, falling back to the full flow on any error.',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: '318 unit tests passed' },
                { key: 'ci/e2e', phase: 'success', description: 'All 24 e2e flows passed' },
              ],
            },
          },
          // env/qa — proposal in flight, e2e gate FAILING (blocks promotion)
          {
            branch: 'env/qa',
            active: {
              dry: {
                sha: 'qa00aaaa1111bbbb2222cccc3333dddd4444eeee',
                author: 'Omar Haddad <omar@example.com>',
                subject: 'fix: prevent double-charge on retry',
                body:
                  'A client retry could charge a customer twice. The capture path is\n' +
                  'now idempotent so a retried request reuses the first charge.',
                commitTime: hoursAgo(20),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
                { key: 'ci/e2e', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 'd00d1111e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2',
                author: 'Mei Lin <mei@example.com>',
                subject: 'feat: one-click checkout for returning customers',
                body:
                  'Returning customers can check out in one click using their saved\n' +
                  'payment method, falling back to the full flow on any error.',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: '318 unit tests passed' },
                { key: 'ci/e2e', phase: 'failure', description: 'End-to-end tests failed' },
              ],
            },
            pullRequest: { id: '204', url: 'https://github.com/example/checkout-service/pull/204', state: 'open' },
            history: [
              {
                active: {
                  dry: {
                    sha: 'qa01bbbb2222cccc3333dddd4444eeee5555ffff',
                    author: 'Omar Haddad <omar@example.com>',
                    subject: 'fix: prevent double-charge on retry',
                    body:
                      'A client retry could charge a customer twice. The capture path is\n' +
                      'now idempotent so a retried request reuses the first charge.',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'qa02cccc3333dddd4444eeee5555ffff6666aaaa',
                    author: 'Omar Haddad <omar@example.com>',
                    subject: 'fix: prevent double-charge on retry',
                    body:
                      'A client retry could charge a customer twice. The capture path is\n' +
                      'now idempotent so a retried request reuses the first charge.',
                    commitTime: daysAgo(1),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                pullRequest: { id: '200', url: 'https://github.com/example/checkout-service/pull/200', prMergeTime: daysAgo(1), state: 'merged' },
              },
            ],
          },
          // env/staging — promotion PR open, all checks still pending
          {
            branch: 'env/staging',
            active: {
              dry: {
                sha: 'st00aaaa1111bbbb2222cccc3333dddd4444eeee',
                author: 'Omar Haddad <omar@example.com>',
                subject: 'fix: prevent double-charge on retry',
                body:
                  'A client retry could charge a customer twice. The capture path is\n' +
                  'now idempotent so a retried request reuses the first charge.',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
                { key: 'ci/e2e', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 'qa00aaaa1111bbbb2222cccc3333dddd4444eeee',
                author: 'Omar Haddad <omar@example.com>',
                subject: 'fix: prevent double-charge on retry',
                body:
                  'A client retry could charge a customer twice. The capture path is\n' +
                  'now idempotent so a retried request reuses the first charge.',
                commitTime: hoursAgo(20),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'pending', description: 'Building image...' },
                { key: 'ci/test', phase: 'pending', description: 'Queued' },
                { key: 'ci/e2e', phase: 'pending', description: 'Queued' },
              ],
            },
            pullRequest: { id: '198', url: 'https://github.com/example/checkout-service/pull/198', state: 'open' },
            history: [
              {
                active: {
                  dry: {
                    sha: 'st01bbbb2222cccc3333dddd4444eeee5555ffff',
                    author: 'Priya Nair <priya@example.com>',
                    subject: 'feat: apply promo codes at checkout',
                    body:
                      'Shoppers can apply a promo code at checkout; the cart recomputes\n' +
                      'totals and validates the code server-side before applying it.',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'st02cccc3333dddd4444eeee5555ffff6666aaaa',
                    author: 'Priya Nair <priya@example.com>',
                    subject: 'feat: apply promo codes at checkout',
                    body:
                      'Shoppers can apply a promo code at checkout; the cart recomputes\n' +
                      'totals and validates the code server-side before applying it.',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                pullRequest: { id: '190', url: 'https://github.com/example/checkout-service/pull/190', prMergeTime: daysAgo(4), state: 'merged' },
              },
            ],
          },
          // env/production — manual gate, settled; history shows a hotfix merged outside the promoter
          {
            branch: 'env/production',
            active: {
              dry: {
                sha: 'pr00aaaa1111bbbb2222cccc3333dddd4444eeee',
                author: 'Priya Nair <priya@example.com>',
                subject: 'feat: apply promo codes at checkout',
                body:
                  'Shoppers can apply a promo code at checkout; the cart recomputes\n' +
                  'totals and validates the code server-side before applying it.',
                commitTime: daysAgo(3),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: '318 unit tests passed' },
                { key: 'ci/e2e', phase: 'success', description: 'All 24 e2e flows passed' },
              ],
            },
            proposed: {
              dry: {
                sha: 'st00aaaa1111bbbb2222cccc3333dddd4444eeee',
                author: 'Omar Haddad <omar@example.com>',
                subject: 'fix: prevent double-charge on retry',
                body:
                  'A client retry could charge a customer twice. The capture path is\n' +
                  'now idempotent so a retried request reuses the first charge.',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/checkout-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: '318 unit tests passed' },
                { key: 'ci/e2e', phase: 'success', description: 'All 24 e2e flows passed' },
              ],
            },
            pullRequest: { id: '205', url: 'https://github.com/example/checkout-service/pull/205', state: 'open' },
            history: [
              {
                active: {
                  dry: {
                    sha: 'pr01bbbb2222cccc3333dddd4444eeee5555ffff',
                    author: 'Priya Nair <priya@example.com>',
                    subject: 'feat: apply promo codes at checkout',
                    body:
                      'Shoppers can apply a promo code at checkout; the cart recomputes\n' +
                      'totals and validates the code server-side before applying it.',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pr02cccc3333dddd4444eeee5555ffff6666aaaa',
                    author: 'Priya Nair <priya@example.com>',
                    subject: 'feat: apply promo codes at checkout',
                    body:
                      'Shoppers can apply a promo code at checkout; the cart recomputes\n' +
                      'totals and validates the code server-side before applying it.',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                pullRequest: { id: '185', url: 'https://github.com/example/checkout-service/pull/185', prMergeTime: daysAgo(3), state: 'merged' },
              },
              // Emergency hotfix merged directly on GitHub, outside the promoter
              {
                active: {
                  dry: {
                    sha: 'pr03dddd4444eeee5555ffff6666aaaa7777bbbb',
                    author: 'On-call <oncall@example.com>',
                    subject: 'hotfix: disable broken promo banner in prod',
                    body:
                      'The promo banner was throwing on render and blanking the home page\n' +
                      'in production. Disables it while the root cause is investigated.',
                    commitTime: daysAgo(6),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'pr04eeee5555ffff6666aaaa7777bbbb8888cccc',
                    author: 'On-call <oncall@example.com>',
                    subject: 'hotfix: disable broken promo banner in prod',
                    body:
                      'The promo banner was throwing on render and blanking the home page\n' +
                      'in production. Disables it while the root cause is investigated.',
                    commitTime: daysAgo(6),
                    repoURL: 'https://github.com/example/checkout-service',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/e2e', phase: 'success' },
                  ],
                },
                pullRequest: {
                  id: '180',
                  url: 'https://github.com/example/checkout-service/pull/180',
                  prMergeTime: daysAgo(6),
                  state: 'merged',
                  externallyMergedOrClosed: true,
                },
              },
            ],
          },
        ],
      },
    },
    // A second strategy with no status yet — newly created, nothing promoted
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'inventory-sync',
        namespace: 'demo',
        uid: 'demo0002-1111-2222-3333-444455556666',
        resourceVersion: '70050',
        generation: 1,
        creationTimestamp: hoursAgo(2),
        labels: { app: 'inventory-sync', team: 'commerce' },
      },
      spec: {
        gitRepositoryRef: { name: 'inventory-sync-repo' },
        environments: [
          { branch: 'env/dev', autoMerge: true },
          { branch: 'env/production', autoMerge: false },
        ],
      },
      status: {
        environments: [],
      },
    },
  ],

  // ════════════════════════════════════════════════════════════════
  // "demo-scenarios" — three strategies in one namespace, each a distinct
  // demo story:
  //   • orders-api      — failed promotions, someone fighting to fix an env
  //   • storefront      — healthy, with commits that only touch some envs
  //   • catalog-service — the standard happy-path promotion flow
  // ════════════════════════════════════════════════════════════════
  'demo-scenarios': [
    // ────────────────────────────────────────────────────────────────
    // "orders-api": someone fighting to stabilize an env. Repeated failed
    // promotions on the same commit lineage; the gate into staging is red.
    // ────────────────────────────────────────────────────────────────
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'orders-api',
        namespace: 'demo-scenarios',
        uid: 'fail0001-1111-2222-3333-444455556666',
        resourceVersion: '80001',
        generation: 19,
        creationTimestamp: daysAgo(25),
        labels: { app: 'orders-api', team: 'fulfillment' },
      },
      spec: {
        gitRepositoryRef: { name: 'orders-api-repo' },
        activeCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }, { key: 'ci/migration-check' }],
        proposedCommitStatuses: [{ key: 'ci/build' }, { key: 'ci/test' }, { key: 'ci/migration-check' }],
        environments: [
          { branch: 'env/dev', autoMerge: true },
          { branch: 'env/staging', autoMerge: true },
          { branch: 'env/production', autoMerge: false },
        ],
      },
      status: {
        environments: [
          // env/dev — current head is a failing fix attempt (build green, migration red)
          {
            branch: 'env/dev',
            active: {
              dry: {
                sha: 'f1aa0001bbbb2222cccc3333dddd4444eeee5555',
                author: 'Sam Ortiz <sam@example.com>',
                subject: 'fix: attempt #4 — widen orders.total to numeric(12,2)',
                body:
                  'Fourth attempt at the totals migration. Widens the column and\n' +
                  'batches the backfill to stay inside the lock budget.',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/orders-api',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: '210 tests passed' },
                { key: 'ci/migration-check', phase: 'failure', description: 'Migration check failed' },
              ],
            },
            proposed: {
              dry: {
                sha: 'f1aa0001bbbb2222cccc3333dddd4444eeee5555',
                author: 'Sam Ortiz <sam@example.com>',
                subject: 'fix: attempt #4 — widen orders.total to numeric(12,2)',
                body:
                  'Fourth attempt at the totals migration. Widens the column and\n' +
                  'batches the backfill to stay inside the lock budget.',
                commitTime: hoursAgo(1),
                repoURL: 'https://github.com/example/orders-api',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success', description: 'Build passed' },
                { key: 'ci/test', phase: 'success', description: '210 tests passed' },
                { key: 'ci/migration-check', phase: 'failure', description: 'Migration check failed' },
              ],
            },
            history: [
              // attempt #3 — also failed
              {
                active: {
                  dry: {
                    sha: 'f1bb0003bbbb2222cccc3333dddd4444eeee5555',
                    author: 'Sam Ortiz <sam@example.com>',
                    subject: 'fix: attempt #3 — chunk the orders.total backfill',
                    body:
                      'Third attempt at the totals migration. Chunks the backfill to keep\n' +
                      'lock times down, but it still deadlocks under load.',
                    commitTime: hoursAgo(6),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/migration-check', phase: 'failure', description: 'Migration check failed' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'f1bb0003cccc2222cccc3333dddd4444eeee5555',
                    author: 'Sam Ortiz <sam@example.com>',
                    subject: 'fix: attempt #3 — chunk the orders.total backfill',
                    body:
                      'Third attempt at the totals migration. Chunks the backfill to keep\n' +
                      'lock times down, but it still deadlocks under load.',
                    commitTime: hoursAgo(6),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/migration-check', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '320', url: 'https://github.com/example/orders-api/pull/320', prMergeTime: hoursAgo(6), state: 'merged' },
              },
              // attempt #2 — failed on tests
              {
                active: {
                  dry: {
                    sha: 'f1cc0002bbbb2222cccc3333dddd4444eeee5555',
                    author: 'Sam Ortiz <sam@example.com>',
                    subject: 'fix: attempt #2 — add NOT NULL default to orders.total',
                    body:
                      'Second attempt at the totals migration. Adds a NOT NULL default so\n' +
                      'existing rows backfill cleanly. Tests still flag a rounding edge.',
                    commitTime: hoursAgo(20),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'failure', description: '3 tests failed' },
                    { key: 'ci/migration-check', phase: 'failure', description: 'Blocked by earlier failure' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'f1cc0002cccc2222cccc3333dddd4444eeee5555',
                    author: 'Sam Ortiz <sam@example.com>',
                    subject: 'fix: attempt #2 — add NOT NULL default to orders.total',
                    body:
                      'Second attempt at the totals migration. Adds a NOT NULL default so\n' +
                      'existing rows backfill cleanly. Tests still flag a rounding edge.',
                    commitTime: hoursAgo(20),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'failure' },
                    { key: 'ci/migration-check', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '317', url: 'https://github.com/example/orders-api/pull/317', prMergeTime: hoursAgo(20), state: 'merged' },
              },
              // attempt #1 — the original change that broke everything
              {
                active: {
                  dry: {
                    sha: 'f1dd0001bbbb2222cccc3333dddd4444eeee5555',
                    author: 'Sam Ortiz <sam@example.com>',
                    subject: 'feat: store order totals as integers (cents)',
                    body:
                      'Stores order totals as integer cents instead of floats to avoid\n' +
                      'rounding drift. Includes the migration for existing rows.',
                    commitTime: daysAgo(2),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure', description: 'Build failed' },
                    { key: 'ci/test', phase: 'failure', description: 'Blocked by build failure' },
                    { key: 'ci/migration-check', phase: 'failure', description: 'Blocked by build failure' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'f1dd0001cccc2222cccc3333dddd4444eeee5555',
                    author: 'Sam Ortiz <sam@example.com>',
                    subject: 'feat: store order totals as integers (cents)',
                    body:
                      'Stores order totals as integer cents instead of floats to avoid\n' +
                      'rounding drift. Includes the migration for existing rows.',
                    commitTime: daysAgo(2),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'failure' },
                    { key: 'ci/test', phase: 'failure' },
                    { key: 'ci/migration-check', phase: 'failure' },
                  ],
                },
                pullRequest: { id: '312', url: 'https://github.com/example/orders-api/pull/312', prMergeTime: daysAgo(2), state: 'merged' },
              },
              // last known-good before the saga began
              {
                active: {
                  dry: {
                    sha: 'f1ee0000bbbb2222cccc3333dddd4444eeee5555',
                    author: 'Lena Fox <lena@example.com>',
                    subject: 'chore: bump orders-api to go 1.23',
                    body:
                      'Updates the toolchain to Go 1.23 and regenerates vendored deps.\n' +
                      'Picks up the new loopvar semantics, so a few range captures were simplified.',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/migration-check', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'f1ee0000cccc2222cccc3333dddd4444eeee5555',
                    author: 'Lena Fox <lena@example.com>',
                    subject: 'chore: bump orders-api to go 1.23',
                    body:
                      'Updates the toolchain to Go 1.23 and regenerates vendored deps.\n' +
                      'Picks up the new loopvar semantics, so a few range captures were simplified.',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/migration-check', phase: 'success' },
                  ],
                },
                pullRequest: { id: '305', url: 'https://github.com/example/orders-api/pull/305', prMergeTime: daysAgo(3), state: 'merged' },
              },
            ],
          },
          // env/staging — stuck on the last-good commit; nothing newer has passed dev's gate
          {
            branch: 'env/staging',
            active: {
              dry: {
                sha: 'f1ee0000bbbb2222cccc3333dddd4444eeee5555',
                author: 'Lena Fox <lena@example.com>',
                subject: 'chore: bump orders-api to go 1.23',
                body:
                  'Updates the toolchain to Go 1.23 and regenerates vendored deps.\n' +
                  'Picks up the new loopvar semantics, so a few range captures were simplified.',
                commitTime: daysAgo(3),
                repoURL: 'https://github.com/example/orders-api',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
                { key: 'ci/migration-check', phase: 'success' },
              ],
            },
            // a promotion was attempted but its checks went red — visible as a failing in-flight
            proposed: {
              dry: {
                sha: 'f1dd0001bbbb2222cccc3333dddd4444eeee5555',
                author: 'Sam Ortiz <sam@example.com>',
                subject: 'feat: store order totals as integers (cents)',
                body:
                  'Stores order totals as integer cents instead of floats to avoid\n' +
                  'rounding drift. Includes the migration for existing rows.',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/orders-api',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'failure', description: 'Waiting on the previous environment' },
                { key: 'ci/test', phase: 'failure', description: 'Blocked' },
                { key: 'ci/migration-check', phase: 'failure', description: 'Blocked' },
              ],
            },
            pullRequest: { id: '313', url: 'https://github.com/example/orders-api/pull/313', state: 'open' },
            history: [
              {
                active: {
                  dry: {
                    sha: 'f1ff9999bbbb2222cccc3333dddd4444eeee5555',
                    author: 'Lena Fox <lena@example.com>',
                    subject: 'feat: add idempotency keys to order creation',
                    body:
                      'Clients can now pass an idempotency key when creating an order, so\n' +
                      'retries no longer produce duplicate orders.',
                    commitTime: daysAgo(8),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/migration-check', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'f1ff9998bbbb2222cccc3333dddd4444eeee5555',
                    author: 'Lena Fox <lena@example.com>',
                    subject: 'feat: add idempotency keys to order creation',
                    body:
                      'Clients can now pass an idempotency key when creating an order, so\n' +
                      'retries no longer produce duplicate orders.',
                    commitTime: daysAgo(8),
                    repoURL: 'https://github.com/example/orders-api',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                    { key: 'ci/migration-check', phase: 'success' },
                  ],
                },
                pullRequest: { id: '300', url: 'https://github.com/example/orders-api/pull/300', prMergeTime: daysAgo(8), state: 'merged' },
              },
            ],
          },
          // env/production — last stable release, well behind dev
          {
            branch: 'env/production',
            active: {
              dry: {
                sha: 'f1ff9999bbbb2222cccc3333dddd4444eeee5555',
                author: 'Lena Fox <lena@example.com>',
                subject: 'feat: add idempotency keys to order creation',
                body:
                  'Clients can now pass an idempotency key when creating an order, so\n' +
                  'retries no longer produce duplicate orders.',
                commitTime: daysAgo(9),
                repoURL: 'https://github.com/example/orders-api',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
                { key: 'ci/migration-check', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 'f1ff9999bbbb2222cccc3333dddd4444eeee5555',
                author: 'Lena Fox <lena@example.com>',
                subject: 'feat: add idempotency keys to order creation',
                body:
                  'Clients can now pass an idempotency key when creating an order, so\n' +
                  'retries no longer produce duplicate orders.',
                commitTime: daysAgo(9),
                repoURL: 'https://github.com/example/orders-api',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
                { key: 'ci/migration-check', phase: 'success' },
              ],
            },
          },
        ],
      },
    },
    // ════════════════════════════════════════════════════════════════
    // DEMO SET 2 — "storefront": healthy pipeline, but some commits only
    // touch a single environment (e.g. env-specific config). Those commits
    // appear in just one column and read as `not-reached` in the others,
    // while feature commits trace across all three via a shared dry SHA.
    // ════════════════════════════════════════════════════════════════
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'storefront',
        namespace: 'demo-scenarios',
        uid: 'part0001-1111-2222-3333-444455556666',
        resourceVersion: '81000',
        generation: 8,
        creationTimestamp: daysAgo(40),
        labels: { app: 'storefront', team: 'web' },
      },
      spec: {
        gitRepositoryRef: { name: 'storefront-repo' },
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
                // SHARED feature SHA — also the head in staging & prod (traces across all 3)
                sha: 'aa00fea7000000000000000000000000feature1',
                author: 'Nora Bell <nora@example.com>',
                subject: 'feat: product recommendations carousel',
                body:
                  'Adds a recommendations carousel to the product page driven by the\n' +
                  'recommendations service, with a graceful empty state.',
                commitTime: hoursAgo(4),
                repoURL: 'https://github.com/example/storefront',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 'aa00fea7000000000000000000000000feature1',
                author: 'Nora Bell <nora@example.com>',
                subject: 'feat: product recommendations carousel',
                body:
                  'Adds a recommendations carousel to the product page driven by the\n' +
                  'recommendations service, with a graceful empty state.',
                commitTime: hoursAgo(4),
                repoURL: 'https://github.com/example/storefront',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            history: [
              // DEV-ONLY config commit — never promoted onward (not-reached elsewhere)
              {
                active: {
                  dry: {
                    sha: 'dev0c0n11g0000000000000000000000devonly1',
                    author: 'Nora Bell <nora@example.com>',
                    subject: 'chore(dev): point storefront at mock payments sandbox',
                    body:
                      'Points the dev environment at the mock payments sandbox so local\n' +
                      'testing never hits the real processor. Dev-only overlay change.',
                    commitTime: hoursAgo(10),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'dev0c0n11g0000000000000000000000devonly2',
                    author: 'Nora Bell <nora@example.com>',
                    subject: 'chore(dev): point storefront at mock payments sandbox',
                    body:
                      'Points the dev environment at the mock payments sandbox so local\n' +
                      'testing never hits the real processor. Dev-only overlay change.',
                    commitTime: hoursAgo(10),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '410', url: 'https://github.com/example/storefront/pull/410', prMergeTime: hoursAgo(10), state: 'merged' },
              },
              // SHARED feature SHA, earlier promotion — ties dev row to staging/prod
              {
                active: {
                  dry: {
                    sha: 'bb11fea7000000000000000000000000feature0',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'feat: gift wrapping option at checkout',
                    body:
                      'Adds an optional gift-wrapping line item at checkout, including the\n' +
                      'extra fee and a short gift message field.',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'bb11fea7000000000000000000000000feature9',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'feat: gift wrapping option at checkout',
                    body:
                      'Adds an optional gift-wrapping line item at checkout, including the\n' +
                      'extra fee and a short gift message field.',
                    commitTime: daysAgo(3),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '402', url: 'https://github.com/example/storefront/pull/402', prMergeTime: daysAgo(3), state: 'merged' },
              },
            ],
          },
          {
            branch: 'env/staging',
            active: {
              dry: {
                // same feature SHA as dev head — traces across the row
                sha: 'aa00fea7000000000000000000000000feature1',
                author: 'Nora Bell <nora@example.com>',
                subject: 'feat: product recommendations carousel',
                body:
                  'Adds a recommendations carousel to the product page driven by the\n' +
                  'recommendations service, with a graceful empty state.',
                commitTime: hoursAgo(3),
                repoURL: 'https://github.com/example/storefront',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 'aa00fea7000000000000000000000000feature1',
                author: 'Nora Bell <nora@example.com>',
                subject: 'feat: product recommendations carousel',
                body:
                  'Adds a recommendations carousel to the product page driven by the\n' +
                  'recommendations service, with a graceful empty state.',
                commitTime: hoursAgo(3),
                repoURL: 'https://github.com/example/storefront',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            history: [
              // STAGING-ONLY config commit — appears only in this column
              {
                active: {
                  dry: {
                    sha: 'stg0c0n11g000000000000000000000stgonly1',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'chore(staging): enable verbose request logging',
                    body:
                      'Turns on verbose request logging in staging to chase down an\n' +
                      'intermittent 502. Staging-only and meant to be temporary.',
                    commitTime: hoursAgo(8),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'stg0c0n11g000000000000000000000stgonly2',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'chore(staging): enable verbose request logging',
                    body:
                      'Turns on verbose request logging in staging to chase down an\n' +
                      'intermittent 502. Staging-only and meant to be temporary.',
                    commitTime: hoursAgo(8),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '408', url: 'https://github.com/example/storefront/pull/408', prMergeTime: hoursAgo(8), state: 'merged' },
              },
              // shared feature SHA — same as dev/prod
              {
                active: {
                  dry: {
                    sha: 'bb11fea7000000000000000000000000feature0',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'feat: gift wrapping option at checkout',
                    body:
                      'Adds an optional gift-wrapping line item at checkout, including the\n' +
                      'extra fee and a short gift message field.',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'bb11fea7000000000000000000000000feature8',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'feat: gift wrapping option at checkout',
                    body:
                      'Adds an optional gift-wrapping line item at checkout, including the\n' +
                      'extra fee and a short gift message field.',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '400', url: 'https://github.com/example/storefront/pull/400', prMergeTime: daysAgo(4), state: 'merged' },
              },
            ],
          },
          {
            branch: 'env/production',
            active: {
              dry: {
                // prod still on the previous feature; the carousel hasn't been promoted here yet
                sha: 'bb11fea7000000000000000000000000feature0',
                author: 'Owen Diaz <owen@example.com>',
                subject: 'feat: gift wrapping option at checkout',
                body:
                  'Adds an optional gift-wrapping line item at checkout, including the\n' +
                  'extra fee and a short gift message field.',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/storefront',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            // carousel is in flight into prod (manual gate), checks green and awaiting merge
            proposed: {
              dry: {
                sha: 'aa00fea7000000000000000000000000feature1',
                author: 'Nora Bell <nora@example.com>',
                subject: 'feat: product recommendations carousel',
                body:
                  'Adds a recommendations carousel to the product page driven by the\n' +
                  'recommendations service, with a graceful empty state.',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/storefront',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            pullRequest: { id: '412', url: 'https://github.com/example/storefront/pull/412', state: 'open' },
            history: [
              // PROD-ONLY config commit — appears only in this column. It landed
              // before the live gift-wrapping commit, then gift wrapping was
              // promoted on top of it, so it reads as an earlier "was here".
              {
                active: {
                  dry: {
                    sha: 'prd0c0n11g00000000000000000000prodonly1',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'chore(prod): raise HPA max replicas for Black Friday',
                    body:
                      'Raises the production autoscaler ceiling ahead of the Black Friday\n' +
                      'traffic spike. Reverts to the normal ceiling afterward.',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                proposed: {
                  hydrated: {
                    sha: 'prd0c0n11g00000000000000000000prodonly2',
                    author: 'Owen Diaz <owen@example.com>',
                    subject: 'chore(prod): raise HPA max replicas for Black Friday',
                    body:
                      'Raises the production autoscaler ceiling ahead of the Black Friday\n' +
                      'traffic spike. Reverts to the normal ceiling afterward.',
                    commitTime: daysAgo(4),
                    repoURL: 'https://github.com/example/storefront',
                  },
                  commitStatuses: [
                    { key: 'ci/build', phase: 'success' },
                    { key: 'ci/test', phase: 'success' },
                  ],
                },
                pullRequest: { id: '405', url: 'https://github.com/example/storefront/pull/405', prMergeTime: daysAgo(4), state: 'merged' },
              },
            ],
          },
        ],
      },
    },
    // ════════════════════════════════════════════════════════════════
    // DEMO SET 3 — "catalog-service": the happy path. Each commit flows
    // dev → staging → production on a single shared dry SHA, so every
    // history row is filled across all three columns. No failures.
    // ════════════════════════════════════════════════════════════════
    {
      kind: 'PromotionStrategy',
      apiVersion: 'promoter.argoproj.io/v1alpha1',
      metadata: {
        name: 'catalog-service',
        namespace: 'demo-scenarios',
        uid: 'std00001-1111-2222-3333-444455556666',
        resourceVersion: '82000',
        generation: 15,
        creationTimestamp: daysAgo(60),
        labels: { app: 'catalog-service', team: 'commerce' },
      },
      spec: {
        gitRepositoryRef: { name: 'catalog-service-repo' },
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
                sha: 's7d00007000000000000000000000000release7',
                author: 'Tara Singh <tara@example.com>',
                subject: 'feat: faceted search filters',
                body:
                  'Adds faceted filtering (category, price, availability) to search\n' +
                  'so shoppers can narrow large result sets quickly.',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/catalog-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 's7d00007000000000000000000000000release7',
                author: 'Tara Singh <tara@example.com>',
                subject: 'feat: faceted search filters',
                body:
                  'Adds faceted filtering (category, price, availability) to search\n' +
                  'so shoppers can narrow large result sets quickly.',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/catalog-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            history: STANDARD_RELEASES.map((r) => ({
              active: {
                dry: {
                  sha: r.sha,
                  author: r.author,
                  subject: r.subject,
                  body: r.body,
                  commitTime: daysAgo(r.dev),
                  repoURL: 'https://github.com/example/catalog-service',
                },
                commitStatuses: [
                  { key: 'ci/build', phase: 'success' },
                  { key: 'ci/test', phase: 'success' },
                ],
              },
              proposed: {
                hydrated: {
                  sha: r.sha.replace(/release/, 'hydr8d'),
                  author: r.author,
                  subject: r.subject,
                  body: r.body,
                  commitTime: daysAgo(r.dev),
                  repoURL: 'https://github.com/example/catalog-service',
                },
                commitStatuses: [
                  { key: 'ci/build', phase: 'success' },
                  { key: 'ci/test', phase: 'success' },
                ],
              },
              pullRequest: { id: `${600 + r.n}`, url: `https://github.com/example/catalog-service/pull/${600 + r.n}`, prMergeTime: daysAgo(r.dev), state: 'merged' },
            })),
          },
          {
            branch: 'env/staging',
            active: {
              dry: {
                sha: 's6d00006000000000000000000000000release6',
                author: 'Umar Khan <umar@example.com>',
                subject: 'feat: bulk price import',
                body:
                  'Adds a bulk price import that accepts a CSV and updates catalog\n' +
                  'prices in batches, validating each row before committing.',
                commitTime: daysAgo(1),
                repoURL: 'https://github.com/example/catalog-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 's7d00007000000000000000000000000release7',
                author: 'Tara Singh <tara@example.com>',
                subject: 'feat: faceted search filters',
                body:
                  'Adds faceted filtering (category, price, availability) to search\n' +
                  'so shoppers can narrow large result sets quickly.',
                commitTime: hoursAgo(2),
                repoURL: 'https://github.com/example/catalog-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            pullRequest: { id: '607', url: 'https://github.com/example/catalog-service/pull/607', state: 'open' },
            history: STANDARD_RELEASES.slice(1).map((r) => ({
              active: {
                dry: {
                  sha: r.sha,
                  author: r.author,
                  subject: r.subject,
                  body: r.body,
                  commitTime: daysAgo(r.staging),
                  repoURL: 'https://github.com/example/catalog-service',
                },
                commitStatuses: [
                  { key: 'ci/build', phase: 'success' },
                  { key: 'ci/test', phase: 'success' },
                ],
              },
              proposed: {
                hydrated: {
                  sha: r.sha.replace(/release/, 'hydr8d'),
                  author: r.author,
                  subject: r.subject,
                  body: r.body,
                  commitTime: daysAgo(r.staging),
                  repoURL: 'https://github.com/example/catalog-service',
                },
                commitStatuses: [
                  { key: 'ci/build', phase: 'success' },
                  { key: 'ci/test', phase: 'success' },
                ],
              },
              pullRequest: { id: `${620 + r.n}`, url: `https://github.com/example/catalog-service/pull/${620 + r.n}`, prMergeTime: daysAgo(r.staging), state: 'merged' },
            })),
          },
          {
            branch: 'env/production',
            active: {
              dry: {
                sha: 's5d00005000000000000000000000000release5',
                author: 'Vera Lopez <vera@example.com>',
                subject: 'feat: per-region catalog availability',
                body:
                  'Catalog availability can now vary by region, so a product can be\n' +
                  'listed in one market and hidden in another.',
                commitTime: daysAgo(2),
                repoURL: 'https://github.com/example/catalog-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            proposed: {
              dry: {
                sha: 's6d00006000000000000000000000000release6',
                author: 'Umar Khan <umar@example.com>',
                subject: 'feat: bulk price import',
                body:
                  'Adds a bulk price import that accepts a CSV and updates catalog\n' +
                  'prices in batches, validating each row before committing.',
                commitTime: daysAgo(1),
                repoURL: 'https://github.com/example/catalog-service',
              },
              commitStatuses: [
                { key: 'ci/build', phase: 'success' },
                { key: 'ci/test', phase: 'success' },
              ],
            },
            pullRequest: { id: '626', url: 'https://github.com/example/catalog-service/pull/626', state: 'open' },
            history: STANDARD_RELEASES.slice(2).map((r) => ({
              active: {
                dry: {
                  sha: r.sha,
                  author: r.author,
                  subject: r.subject,
                  body: r.body,
                  commitTime: daysAgo(r.prod),
                  repoURL: 'https://github.com/example/catalog-service',
                },
                commitStatuses: [
                  { key: 'ci/build', phase: 'success' },
                  { key: 'ci/test', phase: 'success' },
                ],
              },
              proposed: {
                hydrated: {
                  sha: r.sha.replace(/release/, 'hydr8d'),
                  author: r.author,
                  subject: r.subject,
                  body: r.body,
                  commitTime: daysAgo(r.prod),
                  repoURL: 'https://github.com/example/catalog-service',
                },
                commitStatuses: [
                  { key: 'ci/build', phase: 'success' },
                  { key: 'ci/test', phase: 'success' },
                ],
              },
              pullRequest: { id: `${640 + r.n}`, url: `https://github.com/example/catalog-service/pull/${640 + r.n}`, prMergeTime: daysAgo(r.prod), state: 'merged' },
            })),
          },
        ],
      },
    },
    // ════════════════════════════════════════════════════════════════
    // DEMO SET 4 — "mesh-gateway": many-environment stress test. A single
    // service promoted across 15 environments in a linear pipeline, so the
    // history matrix has to render a wide column set and a waterfall front.
    // ════════════════════════════════════════════════════════════════
    buildWideStrategy('mesh-gateway', 15),
  ],
};

export function getMockNamespaces(): string[] {
  return MOCK_NAMESPACES;
}

export function getMockStrategies(namespace: string): PromotionStrategy[] {
  return MOCK_STRATEGIES[namespace] || MOCK_STRATEGIES['production'] || [];
}

// The exact three strategies built for the Red Hat demo — orders-api (failed
// promotions), storefront (partial-env commits) and catalog-service (happy
// path) — all under the "demo-scenarios" namespace. Surfaced through the
// extension's strategy dropdown so each demo story can be flipped through.
export function getDemoStrategies(): PromotionStrategy[] {
  return MOCK_STRATEGIES['demo-scenarios'] || [];
}
