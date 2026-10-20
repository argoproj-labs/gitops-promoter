import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import FlowCell from '@lib/components/PromotionExplorer/FlowCell/FlowCell';
import type { CellKind, CellState, CommitRow } from '@lib/components/PromotionExplorer/types';

const BRANCH = 'environments/dev';

const status = (key: string, phase: string, description?: string, url?: string) => ({
  key,
  phase,
  description,
  url,
});

function cell(
  kind: CellKind,
  statuses: CellState['commitStatuses'],
  extra: Partial<CellState> = {},
) {
  return {
    kind,
    commit: { sha: 'abcdef1234567', subject: 's' },
    commitStatuses: statuses,
    health: 'unknown',
    at: '2026-09-22T14:22:40Z',
    ...extra,
  } as CellState;
}

function renderCell(state: CellState) {
  return render(<FlowCell cell={state} branch={BRANCH} isSelected={false} rowsById={new Map()} />);
}

const checkItems = () => screen.queryAllByRole('listitem');

const rowNames = () =>
  Array.from(document.querySelectorAll('.cell__checks > li')).map(
    (li) => li.querySelector('.cell-check__name')?.textContent,
  );

describe('FlowCell commit checks', () => {
  it('renders a passing check with sr-only status and no spinner', () => {
    renderCell(cell('live', [status('ci/build', 'success', 'ok')]));
    const item = screen.getByTitle('ci/build: passed - ok');
    expect(item).toHaveClass('cell-check--pass');
    expect(within(item).getByText('ci/build')).toBeInTheDocument();
    expect(within(item).getByText('passed')).toHaveClass('hp-sr-only');
    expect(screen.queryByTestId('cell-check-spinner')).not.toBeInTheDocument();
  });

  it('renders a pending check with a spinner', () => {
    renderCell(cell('in-flight', [status('ci/e2e', 'pending')], { isProposed: true }));
    const item = screen.getByTitle('ci/e2e: running');
    expect(item).toHaveClass('cell-check--run');
    expect(within(item).getByTestId('cell-check-spinner')).toBeInTheDocument();
    expect(within(item).getByText('running')).toHaveClass('hp-sr-only');
  });

  it('renders a failing check as failed', () => {
    renderCell(cell('failed', [status('ci/lint', 'failure')]));
    const item = screen.getByTitle('ci/lint: failed');
    expect(item).toHaveClass('cell-check--fail');
    expect(within(item).getByText('failed')).toHaveClass('hp-sr-only');
    expect(screen.queryByTestId('cell-check-spinner')).not.toBeInTheDocument();
  });

  it.each([
    ['success', 'pass', 'ci/build: passed'],
    ['failure', 'fail', 'ci/build: failed'],
    ['pending', 'run', 'ci/build: running'],
  ])('draws the %s check icon as a decorative inline svg', (phase, tone, title) => {
    renderCell(cell('live', [status('ci/build', phase)]));
    const icon = screen.getByTitle(title).querySelector('.cell-check__icon');
    expect(icon).not.toBeNull();
    expect(icon?.tagName.toLowerCase()).toBe('svg');
    expect(icon).toHaveClass(`cell-check__icon--${tone}`);
    expect(icon).toHaveAttribute('aria-hidden', 'true');
    expect(icon).toHaveAttribute('viewBox', '0 0 17 17');
    expect(icon).toHaveAttribute('width', '17');
    expect(icon).toHaveAttribute('height', '17');
    expect(icon?.querySelector('i')).toBeNull();
    expect(icon?.querySelectorAll('.cell-check__mark')).toHaveLength(tone === 'run' ? 0 : 1);
    expect(icon?.querySelectorAll('.cell-check__arc')).toHaveLength(tone === 'run' ? 1 : 0);
  });

  it('lists failures and running checks before passing ones', () => {
    renderCell(
      cell('failed', [
        status('a-pass', 'success'),
        status('b-run', 'pending'),
        status('c-fail', 'failure'),
      ]),
    );
    expect(rowNames()).toEqual(['c-fail', 'b-run', 'a-pass']);
  });

  it('lists passed checks individually with exactly 3 checks', () => {
    renderCell(
      cell('live', [status('a', 'success'), status('b', 'success'), status('c', 'success')]),
    );
    expect(rowNames()).toEqual(['a', 'b', 'c']);
  });

  it('renders every check as its own row with failing and running first', () => {
    const passed = ['p1', 'p2', 'p3', 'p4', 'p5', 'p6'].map((k) => status(k, 'success'));
    renderCell(cell('live', [...passed, status('f1', 'failure'), status('r1', 'pending')]));
    expect(rowNames()).toEqual(['f1', 'r1', 'p1', 'p2', 'p3', 'p4', 'p5', 'p6']);
    expect(screen.queryByText(/checks succeeded/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /checks/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
  });

  it('renders each of 4 passed checks individually', () => {
    renderCell(
      cell(
        'live',
        ['a', 'b', 'c', 'd'].map((k) => status(k, 'success')),
      ),
    );
    expect(rowNames()).toEqual(['a', 'b', 'c', 'd']);
  });

  it('puts the checks in a scroll container', () => {
    renderCell(
      cell(
        'live',
        ['a', 'b', 'c'].map((k) => status(k, 'success')),
      ),
    );
    const list = document.querySelector('.cell__checks') as HTMLElement;
    expect(list).toBeInTheDocument();
    expect(list.closest('.cell')).not.toHaveClass('cell--has-checks');
  });

  it.each([
    ['previously live', cell('was-here', [status('a', 'success'), status('b', 'failure')])],
    ['restored', cell('restored', [status('a', 'success')], { restoredFrom: 'abc1234' })],
    ['no-op', cell('no-op', [status('a', 'success')])],
  ])('shows no checks on a %s cell', (_name, state) => {
    renderCell(state);
    expect(document.querySelector('.cell__checks')).toBeNull();
    expect(document.querySelector('.cell__body')).toBeNull();
  });

  it('shows no check list when there are no statuses', () => {
    renderCell(cell('live', []));
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
    expect(document.querySelector('.cell__body')).toBeNull();
  });

  it('shows no checks on empty cells', () => {
    renderCell(cell('no-changes', []));
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
  });

  it('hides checks on a cell held by a RestoreActiveCommit', () => {
    renderCell(
      cell('in-flight', [status('ci/e2e', 'pending')], {
        isProposed: true,
        restoreActiveCommit: 'hold',
      }),
    );
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
  });
});

describe('FlowCell header', () => {
  const pr = {
    id: '42',
    url: 'https://github.com/org/repo/pull/42',
    state: 'merged',
    prMergeTime: '2026-09-22T14:00:00Z',
    prCreationTime: '2026-09-22T12:00:00Z',
  };
  const hydrated = { sha: 'fedcba9876543', repoURL: 'https://github.com/org/repo' };

  const timeEl = () => document.querySelector('.cell__head time') as HTMLElement;

  it('shows state, hydrated sha, PR link and merged time on a live cell', () => {
    renderCell(cell('live', [status('ci/build', 'success')], { pullRequest: pr, hydrated }));
    const head = document.querySelector('.cell__head') as HTMLElement;
    expect(within(head).getByText('LIVE')).toBeInTheDocument();
    const sha = within(head).getByLabelText(/Hydrated commit fedcba9/);
    expect(sha).toHaveAttribute('href', 'https://github.com/org/repo/commit/fedcba9876543');
    expect(within(head).getByLabelText(/pull request #42/)).toHaveAttribute('href', pr.url);
    expect(timeEl().textContent).toMatch(/^merged /);
    expect(timeEl()).toHaveAttribute('dateTime', pr.prMergeTime);
    expect(timeEl().getAttribute('title')).toMatch(/^merged /);
    const order = Array.from(
      head.querySelectorAll('.cell__pill, .cell__sha, .cell__pr, .cell__time'),
    ).map((el) => el.className.split(' ')[0]);
    expect(order).toEqual(['cell__pill', 'cell__sha', 'cell__pr', 'cell__time']);
    const prLink = within(head).getByLabelText(/pull request #42/);
    expect(prLink.textContent).toBe('#42');
    expect(prLink.textContent).not.toContain('\u2197');
    expect(prLink.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(prLink).toHaveAccessibleName(
      'Promotion pull request #42 into environments/dev, opens in new tab',
    );
    expect(head.querySelector('.cell__hydrated')).toContainElement(timeEl());
    expect(screen.getByTitle('ci/build: passed')).toBeInTheDocument();
  });

  it('shows the opened time on a proposed cell', () => {
    renderCell(
      cell('in-flight', [status('ci/e2e', 'pending')], {
        isProposed: true,
        hydrated,
        pullRequest: { ...pr, state: 'open', prMergeTime: undefined },
      }),
    );
    expect(screen.getByText('PROPOSED')).toBeInTheDocument();
    expect(timeEl().textContent).toMatch(/^opened /);
    expect(timeEl().getAttribute('title')).toMatch(/^opened /);
    expect(timeEl()).toHaveAttribute('dateTime', pr.prCreationTime);
  });

  it('shows merged time on a failed historical cell and no duplicate reason text', () => {
    renderCell(
      cell('failed', [status('ci/lint', 'failure', 'lint broke')], { pullRequest: pr, hydrated }),
    );
    expect(screen.getByText('FAILED')).toBeInTheDocument();
    expect(timeEl().textContent).toMatch(/^merged /);
    expect(screen.queryByText('ci/lint: lint broke')).not.toBeInTheDocument();
    expect(screen.getByTitle('ci/lint: failed - lint broke')).toBeInTheDocument();
  });

  it('shows a previously live cell with sha, PR, time and a Replaced jump', () => {
    const onJump = vi.fn();
    const rowsById = new Map([['next', { id: 'next', subject: 'newer' } as unknown as CommitRow]]);
    render(
      <FlowCell
        cell={cell('was-here', [status('ci/build', 'success')], {
          pullRequest: pr,
          hydrated,
          liveDurationMs: 2 * 3600 * 1000,
          supersededById: 'next',
        })}
        branch={BRANCH}
        isSelected={false}
        rowsById={rowsById}
        onJumpToRow={onJump}
      />,
    );
    const head = document.querySelector('.cell__head') as HTMLElement;
    expect(within(head).getByText('Previously live')).toBeInTheDocument();
    expect(screen.queryByText('REPLACED')).not.toBeInTheDocument();
    expect(within(head).queryByText('FAILED')).not.toBeInTheDocument();
    expect(within(head).getByLabelText(/Hydrated commit fedcba9/)).toBeInTheDocument();
    expect(within(head).getByLabelText(/pull request #42/)).toBeInTheDocument();
    expect(timeEl().textContent).toMatch(/^merged /);
    expect(document.querySelector('.cell__checks')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: /Replaced/ }));
    expect(onJump).toHaveBeenCalledWith('next');
  });

  it('does not mark a previously live cell as failed from historical checks', () => {
    renderCell(
      cell('was-here', [status('a', 'success'), status('b', 'failure')], { pullRequest: pr }),
    );
    expect(screen.queryByText('FAILED')).not.toBeInTheDocument();
    expect(document.querySelector('.cell--was-here')).not.toHaveClass('cell--has-failure');
    expect(rowNames()).toEqual([]);
  });

  it('renders an icon and number without a link when the PR has no url', () => {
    renderCell(cell('live', [], { pullRequest: { id: '42' }, hydrated }));
    const head = document.querySelector('.cell__head') as HTMLElement;
    const pill = head.querySelector('span.cell__pr') as HTMLElement;
    expect(pill.textContent).toBe('Pull request #42');
    expect(pill.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(pill).toHaveAttribute('title', 'Pull request #42');
    expect(head.querySelector('a.cell__pr')).toBeNull();
  });

  it('falls back to a committed label when the PR has no merge time', () => {
    renderCell(cell('live', [], { pullRequest: { id: '42', url: pr.url }, hydrated }));
    expect(timeEl().textContent).toMatch(/^committed /);
  });

  it('shows restored time and the restore commit on a restore cell without a PR', () => {
    renderCell(
      cell('restored', [], {
        restoredFrom: 'abc1234',
        hydrated,
        at: '2026-09-23T10:00:00Z',
      }),
    );
    expect(timeEl().textContent).toMatch(/^restored /);
    expect(screen.getByLabelText(/Hydrated commit fedcba9/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/pull request/)).not.toBeInTheDocument();
  });

  it('keeps the header on a cell held by a RestoreActiveCommit but hides checks', () => {
    renderCell(
      cell('in-flight', [status('ci/e2e', 'pending')], {
        isProposed: true,
        restoreActiveCommit: 'hold',
        pullRequest: { ...pr, state: 'open' },
        hydrated,
      }),
    );
    expect(screen.getByLabelText(/pull request #42/)).toBeInTheDocument();
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
  });

  it('renders no-changes compactly without a header', () => {
    renderCell(cell('no-changes', []));
    expect(screen.getByText('No changes')).toHaveClass('cell--no-changes');
    expect(document.querySelector('.cell__head')).toBeNull();
  });
});

describe('FlowCell check links', () => {
  const onSelect = vi.fn();
  const renderSelectable = (state: CellState) =>
    render(
      <FlowCell
        cell={state}
        branch={BRANCH}
        isSelected={false}
        rowsById={new Map()}
        onSelect={onSelect}
      />,
    );

  it.each([
    ['failure', 'failed'],
    ['pending', 'running'],
    ['success', 'passed'],
  ])('links the name of a %s check with an http(s) url', (phase) => {
    renderCell(
      cell('live', [status('ci/build', phase, undefined, 'https://ci.example.com/job/1')]),
    );
    const link = screen.getByRole('link', { name: 'ci/build (opens details)' });
    expect(link).toHaveAttribute('href', 'https://ci.example.com/job/1');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(link).toHaveTextContent('ci/build');
    expect(link.parentElement).toHaveClass('cell-check');
    expect(link.children).toHaveLength(0);
    expect(link.textContent).toBe('ci/build');
  });

  it.each([
    ['missing', undefined],
    ['javascript:', 'javascript:alert(1)'],
    ['data:', 'data:text/html,hi'],
    ['relative', '/job/1'],
    ['empty', ''],
  ])('renders plain text for a %s url', (_name, url) => {
    renderCell(cell('live', [status('ci/build', 'success', undefined, url)]));
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('ci/build')).toBeInTheDocument();
  });

  it('does not select the cell when the link is clicked or activated by keyboard', () => {
    onSelect.mockClear();
    renderSelectable(cell('live', [status('ci/build', 'failure', undefined, 'https://x.test/1')]));
    const link = screen.getByRole('link');
    fireEvent.click(link);
    fireEvent.keyDown(link, { key: 'Enter' });
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('renders linked check names without an arrow glyph', () => {
    renderCell(
      cell('live', [
        status('a', 'success', undefined, 'https://ci.example.com/a'),
        status('b', 'failure', undefined, 'https://ci.example.com/b'),
      ]),
    );
    const list = document.querySelector('.cell__checks') as HTMLElement;
    expect(list.textContent).not.toContain('\u2197');
    expect(within(list).getAllByRole('link')).toHaveLength(2);
    expect(list.querySelector('a svg')).toBeNull();
  });
});
