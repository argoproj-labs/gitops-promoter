import type { Meta, StoryObj } from '@storybook/react-vite';
import HistoryView from '@lib/components/HistoryView/HistoryView';
import type { Environment, PromotionStrategy } from '@shared/types/promotion';

const BRANCH = 'environments/staging';
const LIVE = 'e077303c44639639a42140b19cf5e1b66731f781';
const REVERTED = '23966a8b70c6e6ff08a5849e2e8458679947b0e3';
const NEWER = 'fb294e4cc39e838f2816eccd50d82827a3258c02';
const REVERT_ACTIVE_COMMIT = 'revert-environments-staging-22d5a51';

const dry = (sha: string, subject: string) => ({
  sha,
  subject,
  commitTime: '2026-09-25T16:00:00Z',
  author: 'Zach Aller <zach@example.com>',
  repoURL: 'https://github.com/argoproj-labs/gitops-promoter',
});

function strategyWith(
  proposedSha: string,
  subject: string,
  pullRequest?: Environment['pullRequest'],
) {
  return {
    metadata: { name: 'argocon-demo', namespace: 'default' },
    spec: { environments: [{ branch: BRANCH }] },
    status: {
      environments: [
        {
          branch: BRANCH,
          active: {
            dry: dry(LIVE, 'chore: bump version to v1.0.2006'),
            hydrated: { sha: '256c2953acb6f30a0fdec506b494cab4c3345978' },
            commitStatuses: [],
          },
          proposed: {
            dry: dry(proposedSha, subject),
            hydrated: { sha: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' },
            commitStatuses: [],
          },
          pullRequest,
          revertActiveCommit: { name: REVERT_ACTIVE_COMMIT, blockedDrySha: REVERTED },
          history: [
            {
              active: {
                dry: dry(LIVE, 'chore: bump version to v1.0.2006'),
                hydrated: { sha: '256c2953acb6f30a0fdec506b494cab4c3345978' },
                commitStatuses: [],
              },
            },
          ],
          lastHealthyDryShas: [],
        },
      ],
    },
  } as unknown as PromotionStrategy;
}

const meta: Meta = {
  title: 'History/Held by revert',
  parameters: { layout: 'fullscreen' },
};

export default meta;

type Story = StoryObj;

const render = (strategy: PromotionStrategy, rowSha: string) => (
  <div style={{ height: '100vh' }}>
    <HistoryView
      strategy={strategy}
      namespace="default"
      fillViewport
      initialSelection={{ rowId: rowSha.slice(0, 7), branch: BRANCH }}
    />
  </div>
);

/** Proposed is still the commit the RevertActiveCommit moved off active; no pull request opens. */
export const RevertedCommit: Story = {
  render: () =>
    render(
      strategyWith(REVERTED, 'chore: bump version to v1.0.2004', {
        id: '3017',
        url: 'https://github.com/argoproj-labs/gitops-promoter/pull/3017',
        state: 'merged',
      }),
      REVERTED,
    ),
};

const DEV = 'environments/development';
const STAGING = 'environments/staging';
const RESTORED_DRY = '2b329f8e0a1b2c3d4e5f60718293a4b5c6d7e8f9';
const DEV_RESTORE = '6d1a639aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const STAGING_RESTORE = '7de7f61bbbbbbbbbbbbbbbbbbbbbbbbbbbbb';

function restoreEnv(branch: string, hydratedSha: string, restoredFrom: string) {
  const dryCommit = dry(RESTORED_DRY, 'chore: bump version to v1.0.2027');
  const hydrated = {
    sha: hydratedSha,
    subject: `Revert ${branch} to ${restoredFrom.slice(0, 7)}`,
    commitTime: '2026-09-28T18:00:00Z',
    repoURL: 'https://github.com/argoproj-labs/gitops-promoter',
  };
  return {
    branch,
    active: { dry: dryCommit, hydrated, commitStatuses: [] },
    proposed: { dry: dryCommit, hydrated: {}, commitStatuses: [] },
    history: [
      {
        active: { dry: dryCommit, hydrated, commitStatuses: [] },
        restoredFrom,
      },
    ],
    lastHealthyDryShas: [],
  };
}

/** Development and staging both restored the same dry commit, so they share one amber row. */
export const CollapsedRestore: Story = {
  render: () => (
    <div style={{ height: '100vh' }}>
      <HistoryView
        strategy={
          {
            metadata: { name: 'argocon-demo', namespace: 'default' },
            spec: { environments: [{ branch: DEV }, { branch: STAGING }] },
            status: {
              environments: [
                restoreEnv(DEV, DEV_RESTORE, '041fe9eaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'),
                restoreEnv(STAGING, STAGING_RESTORE, 'c027caabbbbbbbbbbbbbbbbbbbbbbbbbbbbb'),
              ],
            },
          } as unknown as PromotionStrategy
        }
        namespace="default"
        fillViewport
      />
    </div>
  ),
};

/** A newer commit opened a pull request, which does not auto-merge while the RevertActiveCommit exists. */
export const NewerCommit: Story = {
  render: () =>
    render(
      strategyWith(NEWER, 'chore: bump version to v1.0.2007', {
        id: '3020',
        url: 'https://github.com/argoproj-labs/gitops-promoter/pull/3020',
        state: 'open',
      }),
      NEWER,
    ),
};
