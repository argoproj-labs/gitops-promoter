import React from 'react';
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import FlowCell from '@lib/components/PromotionExplorer/FlowCell/FlowCell';
import type { CellState, CommitRow } from '@lib/components/PromotionExplorer/types';
import { sha } from './fixtures';

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

function renderFlowCell(state: CellState, row?: CommitRow) {
  const rowsById = new Map<string, CommitRow>();
  if (row) rowsById.set(row.id, row);
  return render(<FlowCell cell={state} branch={BRANCH} isSelected={false} rowsById={rowsById} />);
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
