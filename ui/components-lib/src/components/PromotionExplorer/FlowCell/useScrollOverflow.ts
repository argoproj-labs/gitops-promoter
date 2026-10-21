import { useCallback, useLayoutEffect, useState } from 'react';
import type { RefObject } from 'react';

const EDGE_TOLERANCE = 1;

export interface ScrollMetrics {
  scrollTop: number;
  clientHeight: number;
  scrollHeight: number;
}

export interface ScrollOverflow extends ScrollMetrics {
  hasOverflow: boolean;
  atTop: boolean;
  atBottom: boolean;
}

const INITIAL: ScrollOverflow = {
  scrollTop: 0,
  clientHeight: 0,
  scrollHeight: 0,
  hasOverflow: false,
  atTop: true,
  atBottom: true,
};

export function computeScrollOverflow({
  scrollTop,
  clientHeight,
  scrollHeight,
}: ScrollMetrics): ScrollOverflow {
  const hasOverflow = scrollHeight - clientHeight > EDGE_TOLERANCE;
  return {
    scrollTop,
    clientHeight,
    scrollHeight,
    hasOverflow,
    atTop: scrollTop <= EDGE_TOLERANCE,
    atBottom: scrollHeight - scrollTop - clientHeight <= EDGE_TOLERANCE,
  };
}

export function hiddenRowsBelow(metrics: ScrollMetrics, totalRows: number): number {
  if (totalRows <= 0) return 0;
  const remaining = metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight;
  if (remaining <= EDGE_TOLERANCE) return 0;
  const rowPitch = metrics.scrollHeight / totalRows;
  return Math.min(totalRows, Math.ceil(remaining / rowPitch));
}

const same = (a: ScrollOverflow, b: ScrollOverflow) =>
  a.scrollTop === b.scrollTop &&
  a.clientHeight === b.clientHeight &&
  a.scrollHeight === b.scrollHeight;

export function useScrollOverflow(
  ref: RefObject<HTMLElement | null>,
  contentKey: unknown,
): ScrollOverflow {
  const [state, setState] = useState<ScrollOverflow>(INITIAL);

  const measure = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    const next = computeScrollOverflow({
      scrollTop: el.scrollTop,
      clientHeight: el.clientHeight,
      scrollHeight: el.scrollHeight,
    });
    setState((prev) => (same(prev, next) ? prev : next));
  }, [ref]);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    measure();
    el.addEventListener('scroll', measure, { passive: true });
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => {
      el.removeEventListener('scroll', measure);
      observer.disconnect();
    };
  }, [ref, measure, contentKey]);

  return state;
}
