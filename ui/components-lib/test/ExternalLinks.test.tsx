import React from 'react';
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import PromotionExplorer from '@lib/components/PromotionExplorer/PromotionExplorer';
import FlowCell from '@lib/components/PromotionExplorer/FlowCell/FlowCell';
import DetailDrawer from '@lib/components/PromotionExplorer/DetailDrawer/DetailDrawer';
import type { CellState, CommitRow, EnvColumn } from '@lib/components/PromotionExplorer/types';
import { linearStrategy, sha } from './fixtures';

const BRANCH = 'environments/dev';
const HYDRATED = 'f'.repeat(40);
const GOOD_REPO = 'https://github.com/acme/app';
const BAD_REPOS = ['javascript:alert(1)', 'data:text/html,<script>alert(1)</script>'];

function cellState(extra: Partial<CellState> = {}): CellState {
  return {
    kind: 'live',
    commit: { sha: sha('1'), subject: 's', repoURL: GOOD_REPO },
    hydrated: { sha: HYDRATED, repoURL: GOOD_REPO },
    commitStatuses: [],
    health: 'success',
    at: '2026-09-22T14:22:40Z',
    ...extra,
  } as CellState;
}

function rowState(extra: Partial<CommitRow> = {}): CommitRow {
  return {
    id: sha('1').slice(0, 7),
    dryShaFull: sha('1'),
    dryShaShort: sha('1').slice(0, 7),
    subject: 'feat: thing',
    author: 'Dev',
    references: [],
    repoUrl: GOOD_REPO,
    freshestAt: 1,
    earliestAt: 1,
    cells: { [BRANCH]: cellState() },
    hasLive: true,
    hasInFlight: false,
    hasFailed: false,
    hasNoop: false,
    ...extra,
  };
}

function renderFlowCell(state: CellState, row?: CommitRow) {
  const rowsById = new Map<string, CommitRow>();
  if (row) rowsById.set(row.id, row);
  return render(<FlowCell cell={state} branch={BRANCH} isSelected={false} rowsById={rowsById} />);
}

function renderDrawer(state: CellState, row: CommitRow) {
  const envs = [
    { branch: BRANCH, autoMerge: false, color: '#000', liveStatuses: [], liveHealth: 'unknown' },
  ] as unknown as EnvColumn[];
  return render(
    <DetailDrawer
      row={row}
      cell={state}
      branch={BRANCH}
      envs={envs}
      rowsById={new Map()}
      width={400}
      isResizing={false}
      onResizeStart={vi.fn()}
      onResizeReset={vi.fn()}
      onResizeTo={vi.fn()}
      onClose={vi.fn()}
      onJumpToRow={vi.fn()}
      onSelectCell={vi.fn()}
    />,
  );
}

const anchors = (root: ParentNode = document) => Array.from(root.querySelectorAll('a'));

describe('FlowCell header links', () => {
  it('renders valid https links with target and rel', () => {
    const { container } = renderFlowCell(
      cellState({ pullRequest: { id: '42', url: 'https://github.com/acme/app/pull/42' } as never }),
    );
    const hydrated = container.querySelector('a.cell__sha')!;
    expect(hydrated).toHaveAttribute('href', `${GOOD_REPO}/commit/${HYDRATED}`);
    expect(hydrated).toHaveAttribute('target', '_blank');
    expect(hydrated).toHaveAttribute('rel', 'noreferrer');
    const pr = container.querySelector('a.cell__pr')!;
    expect(pr).toHaveAttribute('href', 'https://github.com/acme/app/pull/42');
    expect(pr).toHaveAttribute('target', '_blank');
    expect(pr).toHaveAttribute('rel', 'noreferrer');
  });

  it.each(['javascript:alert(1)', 'data:text/html,hi'])(
    'renders no anchor for the unsafe PR url %s',
    (url) => {
      const { container } = renderFlowCell(cellState({ pullRequest: { id: '42', url } as never }));
      expect(container.querySelector('a.cell__pr')).toBeNull();
      expect(container.querySelector('span.cell__pr')).toHaveTextContent('#42');
    },
  );

  it.each(BAD_REPOS)('renders no anchor for the hydrated commit with repoURL %s', (repoURL) => {
    const { container } = renderFlowCell(
      cellState({ hydrated: { sha: HYDRATED, repoURL } as never }),
    );
    expect(anchors(container)).toHaveLength(0);
    expect(container.querySelector('span.cell__sha')).toHaveTextContent(HYDRATED.slice(0, 7));
  });
});

describe('DetailDrawer links', () => {
  it('renders valid https links with target and rel', () => {
    const { container } = renderDrawer(
      cellState({
        references: [
          { sha: sha('a'), subject: 'app', url: `${GOOD_REPO}/commit/${sha('a')}` } as never,
        ],
      }),
      rowState({ prId: '7', prUrl: 'https://github.com/acme/app/pull/7' }),
    );
    for (const cls of [
      'hp-drawer__sha',
      'hp-drawer__pr',
      'hp-drawer__deployed-sha',
      'hp-drawer__ref-sha',
    ]) {
      const a = container.querySelector(`a.${cls}`)!;
      expect(a).toHaveAttribute('target', '_blank');
      expect(a).toHaveAttribute('rel', 'noreferrer');
      expect(a.getAttribute('href')).toMatch(/^https:\/\//);
    }
  });

  it.each(BAD_REPOS)('renders no anchors for unsafe urls (repoURL %s)', (repoURL) => {
    const { container } = renderDrawer(
      cellState({
        hydrated: { sha: HYDRATED, repoURL } as never,
        references: [
          { sha: sha('a'), subject: 'app', url: `${repoURL}/commit/${sha('a')}` } as never,
        ],
      }),
      rowState({ repoUrl: repoURL, prId: '7', prUrl: repoURL }),
    );
    expect(anchors(container)).toHaveLength(0);
    expect(container.querySelector('.hp-drawer__sha')).toHaveTextContent(sha('1').slice(0, 7));
    expect(container.querySelector('.hp-drawer__pr')).toHaveTextContent('#7');
    expect(container.querySelector('.hp-drawer__deployed-sha')).toHaveTextContent(
      HYDRATED.slice(0, 7),
    );
    expect(container.querySelector('.hp-drawer__ref-sha')).toHaveTextContent(sha('a').slice(0, 7));
  });
});

describe('PromotionExplorer dry commit links', () => {
  it('renders anchors for a valid repo url', () => {
    const { container } = render(
      <PromotionExplorer strategy={linearStrategy()} name="linear" namespace="default" />,
    );
    const chips = Array.from(container.querySelectorAll('.hp-row__commit .hp-commit__sha'));
    expect(chips.length).toBeGreaterThan(0);
    for (const chip of chips) {
      expect(chip.tagName).toBe('A');
      expect(chip).toHaveAttribute('target', '_blank');
      expect(chip).toHaveAttribute('rel', 'noreferrer');
    }
  });

  it.each(BAD_REPOS)('renders no anchor for dry commits with repoURL %s', (repoURL) => {
    const strategy = linearStrategy();
    for (const env of strategy.status?.environments ?? []) {
      for (const c of [env.active?.dry, env.proposed?.dry]) if (c) c.repoURL = repoURL;
      const history = (
        env as unknown as { history?: { active?: { dry?: { repoURL?: string } } }[] }
      ).history;
      for (const h of history ?? []) if (h.active?.dry) h.active.dry.repoURL = repoURL;
    }
    const { container } = render(
      <PromotionExplorer strategy={strategy} name="linear" namespace="default" />,
    );
    const chips = Array.from(container.querySelectorAll('.hp-row__commit .hp-commit__sha'));
    expect(chips.length).toBeGreaterThan(0);
    for (const chip of chips) {
      expect(chip.tagName).toBe('SPAN');
      expect(chip.textContent).toMatch(/^[0-9a-f]{7}$/);
    }
    expect(container.querySelectorAll('.hp-row__commit a')).toHaveLength(0);
  });

  it('renders no app commit anchor for an unsafe reference repoURL', () => {
    const strategy = linearStrategy();
    for (const env of strategy.status?.environments ?? []) {
      const dry = env.active?.dry;
      if (dry)
        dry.references = [
          { commit: { sha: sha('9'), subject: 'app', repoURL: 'javascript:alert(1)' } },
        ] as never;
    }
    const { container } = render(
      <PromotionExplorer strategy={strategy} name="linear" namespace="default" />,
    );
    const chips = container.querySelectorAll('.hp-commit__sha');
    expect(
      Array.from(chips).filter((c) => c.textContent === sha('9').slice(0, 7)),
    ).not.toHaveLength(0);
    expect(container.querySelectorAll('a[href^="javascript"]')).toHaveLength(0);
    for (const chip of Array.from(chips))
      if (chip.textContent === sha('9').slice(0, 7)) expect(chip.tagName).toBe('SPAN');
  });
});
