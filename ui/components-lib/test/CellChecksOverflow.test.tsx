import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import FlowCell from '@lib/components/PromotionExplorer/FlowCell/FlowCell';
import {
  computeScrollOverflow,
  hiddenRowsBelow,
} from '@lib/components/PromotionExplorer/FlowCell/useScrollOverflow';
import type { CellState } from '@lib/components/PromotionExplorer/types';

const ROW = 28;
const VIEWPORT = 112;
const BRANCH = 'environments/dev';

let scrollTop = 0;
let rows = 10;
const scrollTo = vi.fn();

function mockLayout() {
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
    configurable: true,
    get() {
      return this.classList.contains('cell__checks') ? rows * ROW : 0;
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get() {
      return this.classList.contains('cell__checks') ? Math.min(VIEWPORT, rows * ROW) : 0;
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get() {
      return this.classList.contains('cell__checks') ? scrollTop : 0;
    },
    set(v: number) {
      scrollTop = v;
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
    configurable: true,
    writable: true,
    value: scrollTo,
  });
}

function cellWith(count: number) {
  return {
    kind: 'live',
    commit: { sha: 'abcdef1234567', subject: 's' },
    commitStatuses: Array.from({ length: count }, (_, i) => ({
      key: `check-${i}`,
      phase: 'success',
    })),
    health: 'unknown',
    at: '2026-09-22T14:22:40Z',
  } as CellState;
}

function renderCell(count: number, onSelect = vi.fn()) {
  rows = count;
  render(
    <FlowCell
      cell={cellWith(count)}
      branch={BRANCH}
      isSelected={false}
      rowsById={new Map()}
      onSelect={onSelect}
    />,
  );
  return { list: document.querySelector('.cell__checks') as HTMLElement, onSelect };
}

function scrollList(list: HTMLElement, top: number) {
  scrollTop = top;
  act(() => {
    fireEvent.scroll(list);
  });
}

const hint = () => document.querySelector('.cell__checks-more') as HTMLElement | null;

describe('hiddenRowsBelow', () => {
  const m = (scrollTop: number, scrollHeight = 280, clientHeight = 112) => ({
    scrollTop,
    scrollHeight,
    clientHeight,
  });

  it('counts rows below the visible area', () => {
    expect(hiddenRowsBelow(m(0), 10)).toBe(6);
  });

  it('decreases as the list scrolls', () => {
    expect(hiddenRowsBelow(m(56), 10)).toBe(4);
    expect(hiddenRowsBelow(m(60), 10)).toBe(4);
  });

  it('counts a partially visible row as hidden', () => {
    expect(hiddenRowsBelow(m(14), 10)).toBe(6);
  });

  it('is zero at the bottom, with no overflow, or without rows', () => {
    expect(hiddenRowsBelow(m(168), 10)).toBe(0);
    expect(hiddenRowsBelow(m(0, 84, 84), 3)).toBe(0);
    expect(hiddenRowsBelow(m(0), 0)).toBe(0);
  });
});

describe('computeScrollOverflow', () => {
  it('derives overflow and edge flags', () => {
    expect(
      computeScrollOverflow({ scrollTop: 0, clientHeight: 112, scrollHeight: 280 }),
    ).toMatchObject({
      hasOverflow: true,
      atTop: true,
      atBottom: false,
    });
    expect(
      computeScrollOverflow({ scrollTop: 168, clientHeight: 112, scrollHeight: 280 }),
    ).toMatchObject({
      hasOverflow: true,
      atTop: false,
      atBottom: true,
    });
    expect(
      computeScrollOverflow({ scrollTop: 0, clientHeight: 84, scrollHeight: 84 }),
    ).toMatchObject({
      hasOverflow: false,
      atTop: true,
      atBottom: true,
    });
  });
});

describe('CellChecks overflow indicator', () => {
  beforeEach(() => {
    scrollTop = 0;
    scrollTo.mockReset();
    mockLayout();
  });

  afterEach(() => {
    delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollHeight;
    delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientHeight;
    delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollTop;
    delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollTo;
  });

  it('shows nothing when the checks fit', () => {
    const { list } = renderCell(3);
    expect(hint()).toBeNull();
    expect(list).not.toHaveClass('cell__checks--overflow');
    expect(list).not.toHaveClass('cell__checks--fade-bottom');
    expect(list).not.toHaveAttribute('tabindex');
  });

  it('shows the count of rows out of view when overflowing', () => {
    const { list } = renderCell(10);
    expect(hint()).toHaveTextContent('6 more');
    expect(hint()).toHaveAttribute('aria-hidden', 'true');
    expect(list).toHaveClass('cell__checks--overflow', 'cell__checks--fade-bottom');
    expect(list).not.toHaveClass('cell__checks--fade-top');
  });

  it('exposes a labelled, focusable list', () => {
    const { list } = renderCell(10);
    expect(list).toHaveAttribute('tabindex', '0');
    expect(list).toHaveAccessibleName(`Checks in ${BRANCH}, 10 total`);
  });

  it('updates the count after scrolling and enables the top fade', () => {
    const { list } = renderCell(10);
    scrollList(list, 56);
    expect(hint()).toHaveTextContent('4 more');
    expect(list).toHaveClass('cell__checks--fade-top', 'cell__checks--fade-bottom');
  });

  it('hides the hint and bottom fade at the bottom', () => {
    const { list } = renderCell(10);
    scrollList(list, 168);
    expect(hint()).toBeNull();
    expect(list).not.toHaveClass('cell__checks--fade-bottom');
    expect(list).toHaveClass('cell__checks--fade-top');
  });

  it('scrolls down about a page when the hint is clicked without selecting the cell', () => {
    const { list, onSelect } = renderCell(10);
    fireEvent.click(hint()!);
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith({ top: VIEWPORT - ROW });
    expect(list).toBeInTheDocument();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('does not let keyboard scrolling on the list select the cell', () => {
    const { list, onSelect } = renderCell(10);
    fireEvent.keyDown(list, { key: ' ' });
    fireEvent.keyDown(list, { key: 'Enter' });
    expect(onSelect).not.toHaveBeenCalled();
    expect(screen.getAllByRole('listitem')).toHaveLength(10);
  });
});
