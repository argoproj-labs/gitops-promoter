import type { ChangeTransferPolicy, PromotionStrategyDetails } from '../types/view';
import type {
  BranchCommitStatus,
  Commit,
  EnvironmentPullRequest,
  History,
} from '../types/promotion';
import { ago } from './time';

/**
 * Stable, richly-populated mock data for the dashboard and Argo CD app-view extension.
 *
 * Authored once as PromotionStrategyDetails bundles (the CTP-carrying shape). The
 * dashboard consumes these bundles directly; the extension derives PromotionStrategy
 * objects from them via `bundleToPromotionStrategy`.
 *
 * Data model — the invariant that keeps HistoryView coherent:
 *
 *   A dry commit is a single SHA that flows through the environment sequence. Its
 *   `commitTime` (when it was authored) is FIXED; what differs per environment is when
 *   it went live there (`prMergeTime`) and its per-env checks. HistoryView's buildMatrix
 *   keys rows by dry SHA and sorts by freshest timestamp, so:
 *     - the same logical commit MUST carry the same SHA in every environment, and
 *     - a commit that is currently live/in-flight is always newer (larger commitTime)
 *       than any commit that only appears as a "replaced" history entry.
 *
 * We model each strategy as an ordered TIMELINE of dry commits (oldest first). Each
 * environment points at one timeline index as its live commit; earlier environments in
 * the sequence sit at newer indices (caught up), later ones lag. History for an env is
 * the commits older than its live index, newest-first — always beneath the live row.
 *
 * Timestamps are relative-to-now offsets (see ./time), so relative renderings read fresh.
 */

const REPO = 'https://github.com/example-org/platform-manifests';
const CI = 'https://ci.example-org.dev';
const ARGOCD = 'https://argocd.example-org.dev';

// Mirrors the CRD: per-environment history is hard-capped at 5 entries.
const HISTORY_LIMIT = 5;

// A dry commit on the shared timeline. authoredHoursAgo is fixed across every env it
// appears in; the same object is reused so buildMatrix merges the rows by SHA.
interface TimelineCommit {
  sha: string;
  subject: string;
  author: string;
  body?: string;
  authoredHoursAgo: number;
  withReference?: boolean;
}

function toCommit(tc: TimelineCommit): Commit {
  const c: Commit = {
    sha: tc.sha,
    author: tc.author,
    subject: tc.subject,
    body: tc.body ?? '',
    commitTime: ago({ hours: tc.authoredHoursAgo }),
    repoURL: REPO,
  };
  if (tc.withReference) {
    c.references = [
      {
        commit: {
          sha: tc.sha.slice(0, 6) + 'ff',
          author: tc.author,
          subject: tc.subject,
          body: tc.body ?? '',
          date: ago({ hours: tc.authoredHoursAgo + 1 }),
          repoURL: 'https://github.com/example-org/app-source',
        },
      },
    ];
  }
  return c;
}

type Phase = 'success' | 'pending' | 'failure';

// Some checks carry a details URL, others deliberately do not, to exercise both paths.
function check(
  key: string,
  phase: Phase,
  opts?: { url?: string; description?: string },
): BranchCommitStatus {
  const cs: BranchCommitStatus = { key, phase };
  if (opts?.description) cs.description = opts.description;
  if (opts?.url) cs.url = opts.url;
  return cs;
}

function greenChecks(runId: string): BranchCommitStatus[] {
  return [
    check('argocd-app-sync', 'success', {
      description: 'Application is synced and healthy',
      url: `${ARGOCD}/applications/${runId}`,
    }),
    check('ci/unit-tests', 'success', { description: '842 passed', url: `${CI}/${runId}/unit` }),
    check('ci/lint', 'success', { description: 'no issues' }), // no url — exercises no-link path
  ];
}

function pendingChecks(runId: string): BranchCommitStatus[] {
  return [
    check('argocd-app-sync', 'pending', { description: 'Progressing' }),
    check('ci/e2e', 'pending', { description: 'running', url: `${CI}/${runId}/e2e` }),
  ];
}

function failingChecks(runId: string): BranchCommitStatus[] {
  return [
    check('argocd-app-sync', 'success', { description: 'synced' }),
    check('ci/smoke', 'failure', {
      description: 'health probe timed out',
      url: `${CI}/${runId}/smoke`,
    }),
    check('security/scan', 'success', { description: 'no critical findings' }),
  ];
}

// A PR merged `mergedHoursAgo` ago (must be >= the commit's authored time going live).
function mergedPr(id: number, mergedHoursAgo: number): EnvironmentPullRequest {
  return {
    id: String(id),
    state: 'merged',
    url: `${REPO}/pull/${id}`,
    prCreationTime: ago({ hours: mergedHoursAgo + 2 }),
    prMergeTime: ago({ hours: mergedHoursAgo }),
  };
}

function openPr(id: number, createdHoursAgo: number): EnvironmentPullRequest {
  return {
    id: String(id),
    state: 'open',
    url: `${REPO}/pull/${id}`,
    prCreationTime: ago({ hours: createdHoursAgo }),
  };
}

// Externally-merged PR: no state (empty), flagged externallyMergedOrClosed.
function externallyMergedPr(id: number, mergedHoursAgo: number): EnvironmentPullRequest {
  return {
    id: String(id),
    state: '',
    externallyMergedOrClosed: true,
    url: `${REPO}/pull/${id}`,
    prCreationTime: ago({ hours: mergedHoursAgo + 3 }),
    prMergeTime: ago({ hours: mergedHoursAgo }),
  };
}

/**
 * Describes one environment against a shared timeline.
 * - liveIndex: timeline index of the commit currently live in this env.
 * - inFlightIndex: timeline index of a newer commit being promoted (proposed), if any.
 * - wentLiveHoursAgo: when liveIndex went live HERE (>= its authored time; later envs
 *   in the sequence go live later, i.e. smaller hours-ago, than upstream).
 * - liveChecks / proposedChecks: per-env check outcomes.
 */
interface EnvSpec {
  region: string;
  branch: string;
  liveIndex: number;
  inFlightIndex?: number;
  wentLiveHoursAgo: number;
  liveChecks: BranchCommitStatus[];
  proposedChecks?: BranchCommitStatus[];
  liveExternallyMerged?: boolean;
  hydratorLagToIndex?: number; // note.drySha points at this (newer) timeline commit
  prBase: number;
}

function buildCtp(
  ns: string,
  strategyName: string,
  timeline: TimelineCommit[],
  env: EnvSpec,
): ChangeTransferPolicy {
  const liveTc = timeline[env.liveIndex];
  const liveCommit = toCommit(liveTc);

  // History: commits OLDER than the live one in this env, newest-first, capped at
  // HISTORY_LIMIT to mirror the CRD (history length is hard-coded to at most 5 entries).
  // This is what lets a commit still live in a laggard env age out of a caught-up env's
  // history entirely: once an env has promoted HISTORY_LIMIT times past a commit, that
  // commit no longer appears anywhere in that env, so its matrix cell becomes no-changes.
  const history: History[] = [];
  for (let idx = env.liveIndex - 1; idx >= 0 && history.length < HISTORY_LIMIT; idx--) {
    const tc = timeline[idx];
    const stepsBelow = env.liveIndex - idx; // 1 = the entry directly replaced
    const mergedHoursAgo = env.wentLiveHoursAgo + stepsBelow * 24;
    const checks = greenChecks(`${env.region}-h${idx}`);
    history.push({
      active: {
        dry: toCommit(tc),
        hydrated: toCommit(tc),
        commitStatuses: checks,
      },
      proposed: {
        hydrated: toCommit(tc),
        commitStatuses: checks,
      },
      pullRequest: mergedPr(env.prBase + idx, mergedHoursAgo),
    });
  }

  const active: NonNullable<ChangeTransferPolicy['status']>['active'] = {
    dry: liveCommit,
    hydrated: liveCommit,
    commitStatuses: env.liveChecks,
  };

  // Proposed: an in-flight newer commit (distinct sha) or same-as-live (promoted).
  let proposed: NonNullable<ChangeTransferPolicy['status']>['proposed'];
  if (env.inFlightIndex != null) {
    const propCommit = toCommit(timeline[env.inFlightIndex]);
    proposed = {
      dry: propCommit,
      hydrated: propCommit,
      commitStatuses: env.proposedChecks ?? pendingChecks(`${env.region}-p`),
    };
    if (env.hydratorLagToIndex != null) {
      const target = timeline[env.hydratorLagToIndex];
      proposed.note = {
        drySha: target.sha,
        subject: target.subject,
        date: ago({ hours: target.authoredHoursAgo }),
      };
    }
  } else {
    proposed = {
      dry: liveCommit,
      hydrated: liveCommit,
      commitStatuses: env.liveChecks,
    };
  }

  const pullRequest = env.liveExternallyMerged
    ? externallyMergedPr(env.prBase + 900, env.wentLiveHoursAgo)
    : env.inFlightIndex != null
      ? openPr(env.prBase + 800, Math.max(1, env.wentLiveHoursAgo - 1))
      : mergedPr(env.prBase + env.liveIndex, env.wentLiveHoursAgo);

  return {
    apiVersion: 'promoter.argoproj.io/v1alpha1',
    kind: 'ChangeTransferPolicy',
    metadata: { name: `${strategyName}-${env.region}`, namespace: ns },
    spec: {
      activeBranch: env.branch,
      proposedBranch: `${env.branch}-next`,
      activeCommitStatuses: [],
      proposedCommitStatuses: [],
      gitRepositoryRef: { name: 'platform-manifests' },
    },
    status: {
      active,
      proposed,
      pullRequest,
      history,
    },
  } as ChangeTransferPolicy;
}

function bundle(
  name: string,
  namespace: string,
  timeline: TimelineCommit[],
  envs: EnvSpec[],
): PromotionStrategyDetails {
  const ctps = envs.map((env) => buildCtp(namespace, name, timeline, env));
  const ps: PromotionStrategyDetails['promotionStrategy'] = {
    apiVersion: 'promoter.argoproj.io/v1alpha1',
    kind: 'PromotionStrategy',
    metadata: { name, namespace },
    spec: {
      gitRepositoryRef: { name: 'platform-manifests' },
      environments: envs.map((env) => ({ branch: env.branch })),
    },
  };
  return {
    apiVersion: 'view.promoter.argoproj.io/v1alpha1',
    kind: 'PromotionStrategyDetails',
    metadata: { name, namespace },
    promotionStrategy: ps,
    changeTransferPolicies: ctps,
  };
}

// Distinct 40-char pseudo-SHAs, stable per timeline slot.
function hex(seed: string): string {
  let h = '';
  for (let i = 0; i < 40; i++) {
    // simple deterministic fill from the seed characters
    const code = seed.charCodeAt(i % seed.length) + i * 7;
    h += (code % 16).toString(16);
  }
  return h;
}

// ---------------------------------------------------------------------------
// Scenario 1: checkout-service, dev -> staging -> prod.
// dev promoted to the newest commit, staging progressing, prod failing.
// ---------------------------------------------------------------------------
function checkoutService(): PromotionStrategyDetails {
  const ns = 'promoter-system';
  // Timeline oldest -> newest.
  const timeline: TimelineCommit[] = [
    {
      sha: hex('co0'),
      subject: 'chore(deps): bump controller-runtime to 0.19',
      author: 'Priya Nair <priya.nair@example-org.dev>',
      authoredHoursAgo: 120,
    },
    {
      sha: hex('co1'),
      subject: 'fix(auth): refresh token rotation edge case',
      author: 'Dana Rivera <dana.rivera@example-org.dev>',
      authoredHoursAgo: 96,
      withReference: true,
    },
    {
      sha: hex('co2'),
      subject: 'feat(ui): dark-mode toggle in settings',
      author: 'Sam Okafor <sam.okafor@example-org.dev>',
      authoredHoursAgo: 72,
    },
    {
      sha: hex('co3'),
      subject: 'fix(worker): retry transient S3 errors',
      author: 'Dana Rivera <dana.rivera@example-org.dev>',
      authoredHoursAgo: 48,
      withReference: true,
    },
    {
      sha: hex('co4'),
      subject: 'feat(api): add rate-limit headers to gateway',
      author: 'Priya Nair <priya.nair@example-org.dev>',
      authoredHoursAgo: 6,
      withReference: true,
    },
  ];

  const envs: EnvSpec[] = [
    {
      region: 'dev',
      branch: 'environment/dev',
      liveIndex: 4,
      wentLiveHoursAgo: 5,
      liveChecks: greenChecks('dev'),
      prBase: 4700,
    },
    {
      region: 'staging',
      branch: 'environment/staging',
      liveIndex: 3,
      inFlightIndex: 4,
      wentLiveHoursAgo: 40,
      liveChecks: greenChecks('stg'),
      proposedChecks: pendingChecks('stg'),
      prBase: 4720,
    },
    {
      region: 'prod',
      branch: 'environment/prod',
      liveIndex: 2,
      inFlightIndex: 3,
      wentLiveHoursAgo: 64,
      liveChecks: failingChecks('prod'),
      proposedChecks: [
        check('argocd-app-sync', 'pending', { description: 'Waiting on prod gate' }),
      ],
      prBase: 4740,
    },
  ];

  return bundle('checkout-service', ns, timeline, envs);
}

// ---------------------------------------------------------------------------
// Scenario 2: inventory-service, dev -> prod. With only two environments this is the
// clearest place to see the "stranded" case: prod is so far behind that its live
// commit has aged out of dev's (capped) history entirely. Its matrix row therefore
// has a lone live cell in the prod column and no-changes in dev. Also carries the
// externally-merged PR and hydrator-lag cases on prod.
// ---------------------------------------------------------------------------
function inventoryService(): PromotionStrategyDetails {
  const ns = 'promoter-system';
  // Deep enough that dev (live on the newest) has 5 newer history entries above prod's
  // stranded live commit (index 0), pushing index 0 out of dev's window.
  const timeline: TimelineCommit[] = [
    {
      sha: hex('bi0'),
      subject: 'feat(inventory): legacy stock importer',
      author: 'Mara Diaz <mara.diaz@example-org.dev>',
      authoredHoursAgo: 360,
    },
    {
      sha: hex('bi1'),
      subject: 'feat: usage export',
      author: 'Lee Zhang <lee.zhang@example-org.dev>',
      authoredHoursAgo: 288,
    },
    {
      sha: hex('bi2'),
      subject: 'fix(inventory): reservation off-by-one',
      author: 'Mara Diaz <mara.diaz@example-org.dev>',
      authoredHoursAgo: 216,
    },
    {
      sha: hex('bi3'),
      subject: 'chore: rotate signing keys',
      author: 'Lee Zhang <lee.zhang@example-org.dev>',
      authoredHoursAgo: 144,
      withReference: true,
    },
    {
      sha: hex('bi4'),
      subject: 'feat(inventory): multi-warehouse totals',
      author: 'Lee Zhang <lee.zhang@example-org.dev>',
      authoredHoursAgo: 96,
    },
    {
      sha: hex('bi5'),
      subject: 'fix(inventory): rounding on partial units',
      author: 'Mara Diaz <mara.diaz@example-org.dev>',
      authoredHoursAgo: 48,
    },
    {
      sha: hex('bi6'),
      subject: 'feat(inventory): backorder mid-cycle restock',
      author: 'Mara Diaz <mara.diaz@example-org.dev>',
      authoredHoursAgo: 2,
    },
  ];
  const NEW = timeline.length - 1; // 6
  const STRANDED = 0; // prod's live commit — aged out of dev's capped history

  const envs: EnvSpec[] = [
    {
      region: 'dev',
      branch: 'environment/dev',
      liveIndex: NEW,
      wentLiveHoursAgo: 1,
      liveChecks: greenChecks('bdev'),
      prBase: 5400,
    },
    {
      region: 'prod',
      branch: 'environment/prod',
      // Stranded far behind on the oldest commit; a newer commit is in-flight and
      // hydration lags toward the newest, so prod is finally being dragged forward.
      liveIndex: STRANDED,
      inFlightIndex: 1,
      wentLiveHoursAgo: timeline[STRANDED].authoredHoursAgo - 4,
      liveChecks: greenChecks('bprod'),
      proposedChecks: [check('argocd-app-sync', 'pending', { description: 'Hydrating' })],
      liveExternallyMerged: true,
      hydratorLagToIndex: NEW,
      prBase: 5420,
    },
  ];

  return bundle('inventory-service', ns, timeline, envs);
}

// ---------------------------------------------------------------------------
// Scenario 3: docs-site, single environment, fully promoted.
// ---------------------------------------------------------------------------
function docsSite(): PromotionStrategyDetails {
  const ns = 'platform';
  const timeline: TimelineCommit[] = [
    {
      sha: hex('do0'),
      subject: 'docs: add migration guide',
      author: 'Ravi Menon <ravi.menon@example-org.dev>',
      authoredHoursAgo: 72,
    },
    {
      sha: hex('do1'),
      subject: 'docs: fix broken anchors',
      author: 'Ravi Menon <ravi.menon@example-org.dev>',
      authoredHoursAgo: 40,
    },
    {
      sha: hex('do2'),
      subject: 'feat(docs): publish API reference v3',
      author: 'Ravi Menon <ravi.menon@example-org.dev>',
      authoredHoursAgo: 5,
    },
  ];
  const envs: EnvSpec[] = [
    {
      region: 'prod',
      branch: 'main',
      liveIndex: 2,
      wentLiveHoursAgo: 5,
      liveChecks: greenChecks('docs'),
      prBase: 8990,
    },
  ];
  return bundle('docs-site', ns, timeline, envs);
}

// ---------------------------------------------------------------------------
// Scenario 4: service-mesh, ~12 environments in a linear pipeline.
// A rollout frontier moves down the sequence: upstream envs on the new commit,
// one env mid-flight, downstream envs still on the previous commit.
// ---------------------------------------------------------------------------
function serviceMesh(): PromotionStrategyDetails {
  const ns = 'promoter-system';
  const regions = [
    'dev',
    'qa',
    'staging',
    'canary',
    'us-east-1',
    'us-west-2',
    'eu-west-1',
    'eu-central-1',
    'ap-south-1',
    'ap-northeast-1',
    'sa-east-1',
    'prod-global',
  ];
  // Deep timeline (oldest -> newest) so a very old live commit can fall outside the
  // 5-entry history window of the caught-up environments.
  const timeline: TimelineCommit[] = [
    {
      sha: hex('me0'),
      subject: 'feat(mesh): initial traffic policy',
      author: 'Noor Haddad <noor.haddad@example-org.dev>',
      authoredHoursAgo: 480,
    },
    {
      sha: hex('me1'),
      subject: 'fix(mesh): drain timeout on rollout',
      author: 'Ivo Kral <ivo.kral@example-org.dev>',
      authoredHoursAgo: 432,
    },
    {
      sha: hex('me2'),
      subject: 'chore(mesh): pin envoy 1.29',
      author: 'Ivo Kral <ivo.kral@example-org.dev>',
      authoredHoursAgo: 384,
      withReference: true,
    },
    {
      sha: hex('me3'),
      subject: 'feat(mesh): locality-aware routing',
      author: 'Noor Haddad <noor.haddad@example-org.dev>',
      authoredHoursAgo: 288,
    },
    {
      sha: hex('me4'),
      subject: 'fix: dns ttl',
      author: 'Noor Haddad <noor.haddad@example-org.dev>',
      authoredHoursAgo: 216,
    },
    {
      sha: hex('me5'),
      subject: 'feat: retry budgets',
      author: 'Noor Haddad <noor.haddad@example-org.dev>',
      authoredHoursAgo: 144,
    },
    {
      sha: hex('me6'),
      subject: 'chore: bump envoy',
      author: 'Ivo Kral <ivo.kral@example-org.dev>',
      authoredHoursAgo: 96,
      withReference: true,
    },
    {
      sha: hex('me7'),
      subject: 'feat: circuit breaker defaults',
      author: 'Priya Nair <priya.nair@example-org.dev>',
      authoredHoursAgo: 72,
    },
    {
      sha: hex('me8'),
      subject: 'fix(mesh): sidecar memory limit',
      author: 'Ivo Kral <ivo.kral@example-org.dev>',
      authoredHoursAgo: 54,
    },
    {
      sha: hex('me9'),
      subject: 'feat(mesh): enable mTLS strict mode',
      author: 'Noor Haddad <noor.haddad@example-org.dev>',
      authoredHoursAgo: 4,
      withReference: true,
    },
  ];
  const NEW = timeline.length - 1; // 9
  const PREV = timeline.length - 2; // 8
  const frontier = 4; // envs 0..3 on NEW; env 4 in-flight; 5..11 on PREV

  const envs: EnvSpec[] = regions.map((region, idx) => {
    const prBase = 6000 + idx * 20;
    const branch = `environment/${region}`;

    if (idx < frontier) {
      return {
        region,
        branch,
        liveIndex: NEW,
        wentLiveHoursAgo: 3 + idx, // upstream went live slightly earlier
        liveChecks: greenChecks(`m-${region}`),
        prBase,
      };
    }
    if (idx === frontier) {
      return {
        region,
        branch,
        liveIndex: PREV,
        inFlightIndex: NEW,
        wentLiveHoursAgo: 50,
        liveChecks: greenChecks(`m-${region}`),
        proposedChecks: pendingChecks(`m-${region}`),
        prBase,
      };
    }
    return {
      region,
      branch,
      liveIndex: PREV,
      wentLiveHoursAgo: 50 + (idx - frontier) * 2,
      liveChecks: greenChecks(`m-${region}`),
      prBase,
    };
  });

  return bundle('service-mesh', ns, timeline, envs);
}

/** All mock bundles, in the order they should appear in list / dropdown UIs. */
export function mockBundles(): PromotionStrategyDetails[] {
  return [checkoutService(), inventoryService(), serviceMesh(), docsSite()];
}
