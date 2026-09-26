import type { Meta, StoryObj } from '@storybook/react-vite';
import HistoryView from '@lib/components/HistoryView/HistoryView';
import type { Environment, PromotionStrategy } from '@shared/types/promotion';

const BRANCH = 'environments/staging';
const LIVE = 'e077303c44639639a42140b19cf5e1b66731f781';
const REVERTED = '23966a8b70c6e6ff08a5849e2e8458679947b0e3';
const NEWER = 'fb294e4cc39e838f2816eccd50d82827a3258c02';
const REVERT_COMMIT = 'revert-environments-staging-22d5a51';

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
          revertCommit: { name: REVERT_COMMIT, blockedDrySha: REVERTED },
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

/** Proposed is still the commit the RevertCommit moved off active; no pull request opens. */
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

/** A newer commit opened a pull request, which does not auto-merge while the RevertCommit exists. */
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
